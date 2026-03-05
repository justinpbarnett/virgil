# Pipeline: build

## Metadata

type: `pipeline`
task_id: `pipeline-build`
prompt: `Build a pipeline that takes a feature spec and produces a pull request ready for human review. Decompose specs into sub-tasks with a dependency graph, execute independent tasks in parallel via sub-agents, then verify, publish, and review. Two phases: setup (worktree + parallel study) and a development cycle (decompose → build-graph → verify → fix loop, publish, review) that iterates until the work passes or limits are reached.`

## Description

The `build` pipeline is Virgil's fully automated feature development cycle. A user submits a spec — inline text or a path to a spec file — and receives a PR URL when done. No other human input is required during execution.

The pipeline operates in two phases:

**Setup** — creates an isolated git worktree, then runs two study branches in parallel: one from the perspective of a builder (what to create and how) and one from the perspective of a reviewer (what patterns the codebase enforces). Both branches run concurrently against the same codebase and feed their combined context into the decompose step.

**Development cycle** — the spec is decomposed into discrete sub-tasks with a dependency graph. Independent tasks are executed in parallel via sub-agents, each building a focused piece of the feature. An inner verify-fix loop handles build correctness. When that passes, publish commits, pushes, and creates or updates the PR. A reviewer then reads the PR diff from GitHub and either passes (done) or returns structured findings that cycle back to re-decompose. The outer cycle repeats up to `max` times.

---

## Termination Conditions

| Condition | Trigger | Output |
|---|---|---|
| **Review passes** | `review.outcome == "pass"` | PR URL + review summary |
| **Inner loop exhausted** | `verify` fails after N fix attempts | Error envelope with last verify failure and attempt count |
| **Outer cycle exhausted** | `review.outcome == "fail"` after N full cycles | Last PR state + accumulated findings + warning |
| **Graph task failure** | A task in the graph fails and `on_task_failure: halt` | Error envelope with failed task details; no publish |
| **Fatal error** | Missing credentials, unreachable API, worktree creation fails | Diagnostic error envelope; no partial publish |

---

## Graph

```
user prompt (spec or spec reference)
  → worktree (creates a safe copy of the repo)
  → study --role=builder  ┐ parallel
    study --role=reviewer ┘
  → decompose (breaks spec into sub-tasks with dependency DAG)
  → build-tasks (executes task graph: independent tasks parallel, dependent tasks sequential)
  → verify (lint + tests + plan-check) ┐ inner loop, max 5
  → fix (on failure; attempt N)        ┘ until verify.error == null
  → publish (commit, push, create/update PR)
  → review (reads PR diff via GitHub, criteria: dev-review)
  → pass: done
    fail: cycle back to decompose (outer cycle, max 3)
```

---

## Relevant Files

### Existing — no changes needed

- `internal/pipes/worktree/pipe.yaml` — creates an isolated git worktree; already outputs `path` and `branch` as structured content
- `internal/pipes/study/pipe.yaml` — already has `--role=builder` and `--role=reviewer`
- `internal/pipes/verify/pipe.yaml` — already has `--cwd` flag
- `internal/pipes/publish/pipe.yaml` — already has `--cwd` flag; already outputs `pr_number` and `pr_url`
- `internal/pipes/review/pipe.yaml` — already has `--source=pr-diff`, `--criteria=dev-review`, `--pr` flag, and `dev-review` prompt template
### Existing — modified

- `internal/pipes/build/pipe.yaml` + `build.go` — add `--cwd` flag; handler must write files into the worktree path, not the repo root
- `internal/pipes/fix/pipe.yaml` + `fix.go` — add `--cwd` flag; same reason
- `internal/config/config.go` — extend config loader to scan `internal/pipelines/` for `pipeline.yaml` files alongside the existing `internal/pipes/` scan; add `PipelineConfig` struct and related types

### New

- `internal/pipes/decompose/` — new pipe that takes a spec + codebase context and outputs a structured task graph (DAG)
- `internal/pipelines/build/pipeline.yaml` — the pipeline definition (this spec)
- `internal/runtime/pipeline.go` — `PipelineExecutor` that walks steps, fans parallel branches, executes task graphs, manages loop and cycle counters
- `internal/runtime/graph.go` — graph executor: topological sort, level-based parallel dispatch, sub-agent coordination
- `internal/slug/slug.go` — shared `Slugify` function extracted from `internal/pipes/worktree/worktree.go`; used by both the worktree handler and the pipeline template resolver

---

## Pipeline Definition

```yaml
name: build
description: Takes a feature spec and produces a pull request ready for human review.
category: dev

triggers:
  exact:
    - "build a feature"
    - "implement a feature"
  keywords:
    - feature
    - implement
    - add
  patterns:
    - "add {feature} to {target}"
    - "implement {feature} in {target}"
    - "build {feature} for {target}"

steps:
  - name: worktree
    pipe: worktree
    args:
      branch: "feat/{{feature | slugify}}"

  - name: study
    parallel:
      - pipe: study
        args:
          source: codebase
          role: builder
          budget: "8000"
      - pipe: study
        args:
          source: codebase
          role: reviewer
          budget: "8000"
    on_branch_failure: halt

  - name: decompose
    pipe: decompose
    args:
      cwd: "{{worktree.path}}"
      spec: "{{signal}}"
      findings: "{{findings}}"
      max_tasks: "8"

  - name: build-tasks
    graph:
      source: decompose.tasks
      pipe: build
      args:
        cwd: "{{worktree.path}}"
        spec: "{{task.spec}}"
        style: tdd
      on_task_failure: halt
      max_parallel: 4

  - name: verify
    pipe: verify
    args:
      cwd: "{{worktree.path}}"
      lint: "true"
      plan-check: "true"

  - name: fix
    pipe: fix
    args:
      cwd: "{{worktree.path}}"
      attempt: "{{loop.iteration}}"
    condition: verify.error

  - name: publish
    pipe: publish
    args:
      cwd: "{{worktree.path}}"
      base: main
      update-strategy: force-push

  - name: review
    pipe: review
    args:
      source: pr-diff
      pr: "{{publish.pr_number}}"
      criteria: dev-review
      strictness: normal

loops:
  - name: verify-fix
    steps: [verify, fix]
    until: verify.error == null
    max: 5

cycles:
  - name: review-rework
    from: review
    to: decompose
    condition: review.outcome == "fail"
    carry: findings
    max: 3
```

---

## Task Decomposition

### The `decompose` pipe

The decompose pipe analyzes a feature spec and codebase context to produce a structured task graph (DAG). Each task is a self-contained unit of work that can be built independently by a sub-agent.

**Input**: Feature spec (from `{{signal}}`), codebase context (from study step's merged output), and optionally reviewer findings (from cycle carry).

**Output**: Structured envelope with a `tasks` field containing the DAG:

```json
{
  "pipe": "decompose",
  "content": {
    "tasks": [
      {
        "id": "t1",
        "name": "pricing-table",
        "spec": "Create internal/bridge/pricing.go with a pricing table mapping (provider, model) to cost per million tokens...",
        "files": ["internal/bridge/pricing.go", "internal/bridge/pricing_test.go"],
        "depends_on": []
      },
      {
        "id": "t2",
        "name": "anthropic-usage",
        "spec": "Update internal/bridge/anthropic.go to extract token usage from response headers...",
        "files": ["internal/bridge/anthropic.go", "internal/bridge/anthropic_test.go"],
        "depends_on": ["t1"]
      },
      {
        "id": "t3",
        "name": "openai-usage",
        "spec": "Update internal/bridge/openai.go to extract token counts from response JSON...",
        "files": ["internal/bridge/openai.go", "internal/bridge/openai_test.go"],
        "depends_on": ["t1"]
      },
      {
        "id": "t4",
        "name": "envelope-cost",
        "spec": "Add Cost field to envelope.Envelope struct...",
        "files": ["internal/envelope/envelope.go"],
        "depends_on": ["t2", "t3"]
      }
    ]
  },
  "content_type": "structured"
}
```

**Decomposition rules**:
- Each task must list the files it will create or modify. No two tasks at the same dependency level may share files — this guarantees safe parallel execution.
- Tasks are ordered by dependency: a task's `depends_on` lists the IDs of tasks whose outputs it reads or builds upon.
- Task specs are self-contained mini-specs: they include enough context for a build agent to implement without seeing the full feature spec.
- When `findings` is non-empty (rework cycle), the decompose pipe creates tasks that address only the findings, not the entire feature. It reads the existing code in the worktree to understand what's already been built.
- `max_tasks` caps the decomposition granularity. The pipe must not produce more tasks than this limit. When a spec is small enough, a single task is fine.

**`pipe.yaml`**:

```yaml
name: decompose
description: Breaks a feature spec into sub-tasks with a dependency graph for parallel execution.
category: dev
streaming: false
timeout: 60s
provider: anthropic
model: claude-sonnet-4-20250514

triggers:
  exact:
    - "decompose feature"
    - "break down feature"
  keywords:
    - decompose
    - breakdown
    - split
    - subtask
  patterns:
    - "decompose {topic}"
    - "break down {topic} into tasks"

flags:
  cwd:
    description: Working directory (worktree path).
    default: ""
  spec:
    description: The feature spec to decompose.
    default: ""
  findings:
    description: Reviewer findings from a previous cycle (JSON string). When present, decompose for rework.
    default: ""
  max_tasks:
    description: Maximum number of sub-tasks to produce.
    default: "8"

prompts:
  system: |
    You are a software architect who decomposes feature specs into discrete,
    implementable sub-tasks. Each task must be self-contained and buildable
    independently.

    Rules:
    - Analyze the spec and codebase context to identify natural task boundaries.
    - Each task gets its own mini-spec with enough detail for an implementer
      who has NOT read the full feature spec.
    - List all files each task will create or modify. No two tasks at the same
      dependency level may share files.
    - Order tasks by dependency: foundational types/interfaces first, then
      implementations that depend on them, then integration/wiring last.
    - Keep tasks focused. One task = one logical concern. A task that touches
      5+ files is probably too broad.
    - When findings are present, create targeted tasks that address each
      finding. Do not re-decompose the entire feature.

  templates:
    initial: |
      Decompose the following feature into sub-tasks.

      ## Feature Spec
      {{.Spec}}

      ## Codebase Context
      {{.Context}}

      Produce a JSON task graph. Each task needs: id, name, spec (mini-spec),
      files (list of files to create/modify), depends_on (list of task IDs).

      Maximum {{.MaxTasks}} tasks.

    rework: |
      The following reviewer findings need to be addressed.

      ## Original Feature Spec
      {{.Spec}}

      ## Codebase Context
      {{.Context}}

      ## Reviewer Findings
      {{range .Findings}}
      - [{{.Category}}/{{.Severity}}] {{.File}}:{{.Line}} — {{.Issue}}
        Action: {{.Action}}
      {{end}}

      Create targeted sub-tasks that address each finding. Do not re-implement
      the entire feature — only fix what the reviewer flagged.

      Produce a JSON task graph. Maximum {{.MaxTasks}} tasks.

vocabulary:
  verbs:
    decompose: decompose
    breakdown: decompose
    split: decompose
  types: {}
  sources: {}
  modifiers: {}
```

---

## Condition Syntax

Conditions appear in three places: step `condition`, loop `until`, and cycle `condition`. All three use the same minimal two-pattern grammar:

**Truthy check** — `field` — true if the named field is non-null and non-empty string.
```
condition: verify.error         # run fix only if verify produced an error
```

**Equality check** — `field == value` — true if the field equals the literal value.
```
condition: review.outcome == "fail"    # cycle if review failed
```

**Negation** — `field == null` — false if the field is non-null (syntactic sugar for the until condition).
```
until: verify.error == null     # exit loop when no error
```

Field names are dot-separated step-name + field-name paths. `verify.error` refers to the `error` field of the last `verify` step output. `review.outcome` refers to the `outcome` field of the `review` step output.

No other operators (AND, OR, comparisons, arithmetic) are supported. If a condition can't be expressed with these two patterns, it belongs in the pipe's handler, not in the pipeline definition.

---

## Envelope Flow

### After `worktree`

The worktree handler already outputs `content_type: structured` with a `WorktreeOutput` struct:

```json
{
  "pipe": "worktree",
  "content": { "path": ".worktrees/feat-oauth-login", "branch": "feat/oauth-login", "base_commit": "abc123", "created": true },
  "content_type": "structured"
}
```

`{{worktree.path}}` resolves to `.worktrees/feat-oauth-login` in all subsequent steps.

### After `study` (parallel group → merged envelope)

Both branches receive the same pre-study input envelope and run concurrently. The runtime merges their text outputs into a single combined content field before passing it to the next step:

```
[Builder perspective]
<builder study content>

[Reviewer perspective]
<reviewer study content>
```

This becomes the `{{.Context}}` in the `decompose` pipe's prompt.

### After `decompose`

Structured envelope with a `tasks` field containing the DAG (see Task Decomposition section for full schema). `decompose.tasks` is stored in the context map and referenced by the `build-tasks` graph step.

### After `build-tasks` (graph execution)

The graph executor runs tasks in topological order (see Graph executor section for the algorithm). Each task invokes the `build` pipe with `spec` set to the task's mini-spec. All tasks share the same worktree via `cwd`.

### After `publish`

```json
{
  "pipe": "publish",
  "content": { "pr_url": "https://github.com/owner/repo/pull/47", "pr_number": 47, "branch": "feat/oauth-login", "commit": "abc1234" },
  "content_type": "structured"
}
```

`{{publish.pr_number}}` resolves to `47` for the `review` step's `--pr` flag.

### Cycle carry

When `review.outcome == "fail"`, the `findings` array carries back to the `decompose` step:

```json
{
  "findings": [
    {
      "category": "logic",
      "severity": "major",
      "file": "internal/auth/handler.go",
      "line": 42,
      "issue": "Token not validated before use",
      "action": "Call ValidateToken() before accessing token.Claims"
    }
  ]
}
```

`{{findings}}` resolves to the JSON-serialized findings array. The `decompose` pipe selects its `rework` prompt template when `findings` is non-empty, producing targeted fix tasks instead of re-decomposing the entire feature.

---

## Modified Pipes

### `build` — add `--cwd` flag

**Why**: The handler writes files. Without `cwd` it writes relative to the virgil server's working directory — the main repo. All writes must happen inside the worktree.

**`pipe.yaml` addition**:
```yaml
flags:
  cwd:
    description: Working directory (worktree path). Required when running inside a pipeline.
    default: ""
```

**Handler change**: When `cwd` is non-empty, resolve all file read and write paths relative to `cwd`. The handler must not escape the worktree by following `..` references or absolute paths in the spec.

### `fix` — add `--cwd` flag

Same motivation as `build`.

**`pipe.yaml` addition**:
```yaml
flags:
  cwd:
    description: Working directory (worktree path).
    default: ""
```

**Handler change**: Resolve all file paths relative to `cwd` when provided.

---

## New Infrastructure

### Pipeline discovery: `internal/pipelines/`

Pipelines live at `internal/pipelines/{name}/pipeline.yaml`. No executable — configuration only. Extend `config.Load()` to scan `internal/pipelines/` for `pipeline.yaml` files after scanning `internal/pipes/` for `pipe.yaml` files.

**`PipelineConfig` struct**:

```go
type PipelineConfig struct {
    Name        string        `yaml:"name"`
    Description string        `yaml:"description"`
    Category    string        `yaml:"category"`
    Triggers    TriggerConfig `yaml:"triggers"`
    Steps       []StepConfig  `yaml:"steps"`
    Loops       []LoopConfig  `yaml:"loops"`
    Cycles      []CycleConfig `yaml:"cycles"`
}

type StepConfig struct {
    Name            string            `yaml:"name"`
    Pipe            string            `yaml:"pipe"`
    Pipeline        string            `yaml:"pipeline"`   // not implemented; startup fails if set
    Parallel        []ParallelBranch  `yaml:"parallel"`
    Graph           *GraphConfig      `yaml:"graph"`
    Args            map[string]string `yaml:"args"`
    Condition       string            `yaml:"condition"`
    OnBranchFailure string            `yaml:"on_branch_failure"`
}

type ParallelBranch struct {
    Pipe string            `yaml:"pipe"`
    Args map[string]string `yaml:"args"`
}

type GraphConfig struct {
    Source        string            `yaml:"source"`          // context map field containing task DAG
    Pipe          string            `yaml:"pipe"`            // pipe to invoke per task
    Args          map[string]string `yaml:"args"`            // template args, can use {{task.*}}
    OnTaskFailure string            `yaml:"on_task_failure"` // "halt" or "continue-independent"
    MaxParallel   int               `yaml:"max_parallel"`    // max concurrent sub-agents (0 = unlimited)
}

type LoopConfig struct {
    Name  string   `yaml:"name"`
    Steps []string `yaml:"steps"`
    Until string   `yaml:"until"`
    Max   int      `yaml:"max"`
    Carry string   `yaml:"carry"`
}

type CycleConfig struct {
    Name      string `yaml:"name"`
    From      string `yaml:"from"`
    To        string `yaml:"to"`
    Condition string `yaml:"condition"`
    Carry     string `yaml:"carry"`
    Max       int    `yaml:"max"`
}
```

Config loader validation at startup:
- All `pipe:` names must resolve to registered pipes
- `pipeline:` in any step fails startup with "nested pipeline references not yet supported"
- `graph.pipe` must resolve to a registered pipe
- `graph.source` must be a valid dot-path reference
- Step names within a pipeline must be unique
- Loop `steps` must reference valid step names in the same pipeline
- Cycle `from` and `to` must reference valid step names in the same pipeline
- All conditions must parse as valid two-pattern expressions (see Condition Syntax)

### `PipelineExecutor` in `internal/runtime/pipeline.go`

The runtime needs a `PipelineExecutor` that:

1. **Sequential steps** — executes steps in order; each step receives the previous step's output envelope as input
2. **Parallel groups** — fans out branches concurrently; all branches receive the same input envelope; merges text outputs with role headers; on `on_branch_failure: halt`, cancels remaining branches and propagates the fatal error
3. **Graph steps** — reads the task DAG from the context map, topologically sorts, executes tasks level-by-level with up to `max_parallel` concurrent sub-agents; collects results into a merged output envelope
4. **Loops** — after each full iteration evaluates the `until` condition against the context map; if false, runs the next iteration; on `max` exhaustion, exits with a fatal error envelope; the loop counter resets at the start of each outer cycle — each cycle gets a fresh `max` attempt budget
5. **Cycles** — after the `from` step, evaluates the `condition`; if true, extracts `carry` fields from the step's structured output and merges them into the context map as top-level keys, then resumes from the `to` step; on `max` exhaustion, completes with the last output and a warning
6. **Template variable resolution** — before each step executes, resolves `{{stepname.fieldname}}` references in `args` against the context map; resolves built-in variables: `{{signal}}` (raw user input), `{{feature}}`, `{{target}}`, and other signal-parsed components; resolves `{{loop.iteration}}` (1-indexed) and `{{task.*}}` (within graph steps); applies built-in filters: `slugify`, `lower`, `upper`; unresolved variables (key absent from context map) produce empty string — never an error or the literal `<no value>`
7. **Fatal propagation** — a fatal error in any step halts the pipeline; no further steps execute; no publish occurs

### Graph executor: `internal/runtime/graph.go`

The graph executor handles the `graph` step type:

```go
type TaskNode struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Spec      string   `json:"spec"`
    Files     []string `json:"files"`
    DependsOn []string `json:"depends_on"`
}

type TaskResult struct {
    ID       string        `json:"id"`
    Name     string        `json:"name"`
    Status   string        `json:"status"` // "pass" or "fail"
    Duration time.Duration `json:"duration"`
    Error    string        `json:"error,omitempty"`
}
```

**Algorithm**:

1. Parse `[]TaskNode` from the context map field specified by `graph.source`
2. Build adjacency lists and compute in-degrees
3. Validate: reject cycles (DAG only), reject tasks with unknown dependencies
4. Group into levels via Kahn's algorithm (topological sort)
5. For each level:
   a. Spawn up to `max_parallel` sub-agents (pipe subprocesses)
   b. Each sub-agent receives an envelope with `{{task.*}}` variables resolved from the task node
   c. Wait for all sub-agents at this level to complete
   d. On failure: if `on_task_failure: halt`, cancel remaining and return error; if `continue-independent`, log the failure and proceed with tasks at the next level that don't depend on the failed task
6. Collect all `TaskResult`s into the output envelope

**File conflict detection**: At startup (config validation time), file conflicts can't be checked — they're runtime data. At execution time, the graph executor validates that no two tasks at the same dependency level share files in their `files` lists. If a conflict is detected, it's a fatal error — the decompose pipe produced an invalid graph.

**Sub-agent isolation**: Each task runs as a separate pipe subprocess. Since tasks at the same dependency level are guaranteed not to share files (enforced by the decompose pipe), parallel writes to the same worktree are safe. Tasks at different levels run sequentially, so later tasks see the files created by earlier ones.

**Graph step output**:

```json
{
  "pipe": "build-tasks",
  "content": {
    "tasks_completed": 4,
    "tasks_failed": 0,
    "levels": 3,
    "results": [
      {"id": "t1", "name": "pricing-table", "status": "pass", "duration": "4.2s"},
      {"id": "t2", "name": "anthropic-usage", "status": "pass", "duration": "6.1s"},
      {"id": "t3", "name": "openai-usage", "status": "pass", "duration": "5.8s"},
      {"id": "t4", "name": "envelope-cost", "status": "pass", "duration": "3.4s"}
    ]
  },
  "content_type": "structured"
}
```

### Context map

The executor maintains a context map of structured fields only. Text-type envelope outputs flow through the envelope chain naturally and are never stored in the context map — no pipeline arg references them by name.

What gets stored after each step:
- The top-level `error` field stored as `ctx["stepname.error"]` (null or an error object) — this is how `verify.error` is accessible even though `error` is not a content field
- For structured envelopes: each field of `envelope.content` stored as `ctx["stepname.fieldname"]` — this is how `review.outcome`, `publish.pr_number`, and `decompose.tasks` are accessible
- After a cycle carry: the named carry field extracted from the `from` step's context entry and stored as a top-level key (`ctx["findings"]` from `ctx["review.findings"]`)
- `{{loop.iteration}}` is a synthetic key updated by the loop executor at the start of each iteration
- `{{task.*}}` variables are synthetic keys set by the graph executor for each task invocation

What is never stored: text envelope content (study output, build output, fix output). These flow as envelope content to the next step — nothing references them by name in `args`.

The context map stays small across the full run: worktree (4 fields), decompose (tasks array), publish (4 fields), review outcome + findings, loop iteration counter. No trimming needed.

### Template resolver and `internal/slug`

The template resolver uses Go's `text/template` with a function map of built-in filters. The `slugify` implementation is extracted from `internal/pipes/worktree/worktree.go` (which already has a private `slugify` function) into `internal/slug/slug.go`. Both the worktree handler and the template resolver import from there.

The filter does not add a `feat/` prefix — that is the pipeline arg's explicit job (`branch: "feat/{{feature | slugify}}"`). The worktree handler's auto-prefix logic still applies when `branch` is empty, but the pipeline always passes an explicit branch.

### Router disambiguation: `build` pipeline vs `build` pipe

The router gives pipeline definitions precedence over pipes with the same name. The `build` pipe continues to exist as a subprocess invoked directly by the graph executor — it is never reached by the router once the `build` pipeline is registered.

The router uses stems-based keyword matching (see `internal/router/stems.go`). Pipeline triggers feed the same layers as pipe triggers — exact match, keyword index (with stemming and synonym expansion), category narrowing. The router doesn't distinguish between pipes and pipelines during matching; precedence is resolved after the match.

---

## TUI Behavior

The stream uses the pipeline name as the step prefix. Internal step names and task graph details are surfaced only in the detail panel.

**Stream (top level)**:
```
❯ add OAuth login to Keep

  ▸ build: creating worktree feat/oauth-login
  ▸ build: studying codebase (2 parallel branches)
  ▸ build: decomposing into 4 sub-tasks
  ▸ build: executing task graph (3 levels, max 4 parallel)
    ▸ level 0: pricing-table
    ▸ level 1: anthropic-usage, openai-usage (parallel)
    ▸ level 2: envelope-cost
  ▸ build: verify failed, fixing (attempt 1)...
  ▸ build: verify passed
  ▸ build: publishing PR
  ▸ build: reviewing PR #47...
  ▸ build: review found 2 issues, cycling...
  ▸ build: re-decomposing with findings (2 targeted tasks)
  ▸ build: executing task graph (1 level, 2 parallel)
  ▸ build: verify passed, updating PR #47
  ▸ build: reviewing PR #47...

  build complete. PR #47 ready for human review.
  2 cycles, 4 tasks + 2 rework tasks, 42.8s total.
```

**Detail panel** (expanded):
```
pipeline: build — 42.8s (2 cycles)
  step: worktree — 0.4s
  step: study/parallel — 3.4s
    branch: study[builder] — 2.9s
    branch: study[reviewer] — 3.4s
  step: decompose — 2.1s → 4 tasks, 3 levels
  step: build-tasks/graph — 18.3s
    level 0: pricing-table (4.2s)
    level 1: anthropic-usage (6.1s) | openai-usage (5.8s)
    level 2: envelope-cost (3.4s)
  loop: verify-fix — cycle 1 — 8.7s (2 iterations)
    iteration 1: verify fail → fix (5.8s)
    iteration 2: verify pass (2.9s)
  step: publish — 0.8s → PR #47 created
  step: review — 4.2s → fail, 2 major findings
  step: decompose — 1.4s → 2 targeted tasks, 1 level
  step: build-tasks/graph — 5.2s
    level 0: fix-token-validation (2.8s) | fix-error-handling (3.1s)
  loop: verify-fix — cycle 2 — 2.4s (1 iteration)
    iteration 1: verify pass (2.4s)
  step: publish — 0.6s → PR #47 updated
  step: review — 3.8s → pass
```

---

## Testing Strategy

### Pipeline graph tests (mock all pipes)

- **Happy path** — all pipes return passing envelopes; assert output has `pr_url` and `error == null`
- **Decompose produces valid DAG** — mock decompose returns a multi-level task graph; assert build-tasks executes tasks in correct order
- **Parallel task execution** — decompose returns 3 tasks at level 0; assert all 3 run concurrently (within `max_parallel`)
- **Task dependency ordering** — decompose returns tasks with dependencies; assert level 1 tasks don't start until level 0 completes
- **Task failure halts graph** — one task fails with `on_task_failure: halt`; assert remaining tasks at that level are cancelled
- **Task failure continue-independent** — one task fails with `continue-independent`; assert independent tasks at the next level still run; dependent tasks are skipped
- **File conflict detection** — decompose returns two tasks at the same level sharing a file; assert fatal error
- **Inner loop retry** — `verify` fails once then passes; assert `fix` ran once, `verify` ran twice, `build-tasks` ran only once, no error in output
- **Inner loop exhaustion** — `verify` always fails; assert pipeline exits after max=5 with a fatal error envelope containing the last verify output; assert `build-tasks` was not re-run
- **Outer cycle** — `review` fails once with findings then passes; assert `decompose` ran twice (initial + rework); assert second decompose received `findings` in args; assert `publish` ran twice
- **Outer cycle exhaustion** — `review` always fails; assert pipeline exits after max=3 cycles with last PR state and a warning
- **Rework decomposition** — on cycle, assert decompose receives findings and produces targeted tasks (fewer than initial decomposition)
- **Parallel branch failure** — one `study` branch returns fatal; assert pipeline halts, no `decompose` step runs
- **Worktree fatal** — `worktree` returns fatal; assert nothing else runs
- **Findings carry** — assert `findings` from a failed `review` appear in the `decompose` step's args on the next cycle
- **Condition parsing** — valid conditions parse correctly; invalid conditions (unsupported operators) are rejected at startup
- **Single-task decomposition** — decompose returns 1 task; assert graph executor handles the degenerate case correctly

### Graph executor unit tests

- **Topological sort** — given a DAG, assert correct level assignment
- **Cycle detection** — given a graph with cycles, assert rejection
- **Max parallel** — given 6 tasks at level 0 and `max_parallel: 4`, assert only 4 run concurrently
- **Empty graph** — given 0 tasks, assert no-op with empty output
- **Single task** — given 1 task with no dependencies, assert it runs and completes
- **Diamond dependency** — A→B, A→C, B→D, C→D; assert D runs only after both B and C complete
- **File conflict at same level** — two tasks at level 0 both list `foo.go`; assert fatal error before execution

### Pipe unit tests

**`decompose`**:
- Given a feature spec with no findings, produces a valid task graph with unique IDs
- Given findings, produces targeted rework tasks
- Given `max_tasks: 3`, output has at most 3 tasks
- Given a simple spec, may produce a single task (no over-decomposition)
- All tasks have non-empty `id`, `name`, `spec`, `files`
- No two tasks at the same dependency level share files

**`build` with `--cwd`**:
- Given `cwd=/tmp/worktree`, all file writes are prefixed with `/tmp/worktree`
- Given non-empty `findings`, the `rework` prompt template is selected
- Given empty `findings`, the `initial` prompt template is selected
- Given empty `cwd`, behavior is unchanged (backwards compatible)

**`fix` with `--cwd`**:
- Given `cwd=/tmp/worktree`, all file reads and writes use that prefix
- Given empty `cwd`, behavior is unchanged

### `internal/slug` tests

- `"OAuth login"` → `"oauth-login"`
- `"add OAuth login to Keep"` → `"add-oauth-login-to-keep"` (no `feat/` prefix from the filter)
- Special characters, leading/trailing hyphens, consecutive hyphens all handled correctly
- Empty string → empty string

### Config tests

- `PipelineConfig` parses correctly from a valid `pipeline.yaml`
- `GraphConfig` parses correctly within a step
- Startup validation rejects: missing pipe reference, duplicate step names, `pipeline:` step field set, graph with invalid source reference, graph with unregistered pipe, cycle referencing non-existent step, loop `steps` referencing non-existent step
- Startup validation rejects conditions that use unsupported operators
- `build` pipeline and `build` pipe coexist without conflict; pipeline takes routing precedence

---

## Implementation Order

1. Extract `Slugify` from `internal/pipes/worktree/worktree.go` into `internal/slug/slug.go`; update worktree handler to import from there
2. Add `PipelineConfig`, `GraphConfig`, and related structs to `internal/config/config.go`; extend `Load()` to scan `internal/pipelines/`; add startup validation including condition parsing and graph validation
3. Add `--cwd` flag to `internal/pipes/build/pipe.yaml` and update `build.go` handler
4. Add `--cwd` flag to `internal/pipes/fix/pipe.yaml` and update `fix.go` handler
5. Create `internal/pipes/decompose/` — pipe.yaml, decompose.go handler, cmd/main.go, tests
6. Implement graph executor in `internal/runtime/graph.go` (topological sort, level grouping, parallel dispatch, file conflict detection)
7. Implement `PipelineExecutor` in `internal/runtime/pipeline.go` (sequential, parallel, graph, loop, cycle, template resolution with filter set using `internal/slug`)
8. Write `internal/pipelines/build/pipeline.yaml`
9. Register pipeline discovery and executor in `cmd/virgil/main.go`; integrate with router for pipeline precedence
10. Write pipeline graph tests
11. Write graph executor unit tests
12. Write decompose pipe unit tests
13. Write updated pipe unit tests for `build` and `fix`

Steps 1, 3, 4, and 5 are independent and can go first. Step 6 is independent of pipes. Step 7 depends on steps 1, 2, and 6. Step 8 depends on step 2. Step 9 depends on steps 7 and 8.

---

## Validation Commands

```bash
go build ./...
go vet ./...
go test ./internal/slug/... -v
go test ./internal/config/... -v
go test ./internal/runtime/... -v
go test ./internal/pipes/decompose/... -v
go test ./internal/pipes/build/... -v
go test ./internal/pipes/fix/... -v
go test ./internal/pipes/worktree/... -v
go test ./... -count=1
```

---

## Resolved Decisions

- **`on_branch_failure: halt` for the study parallel group**: The decompose pipe requires both perspectives. A partial study context would produce lower-quality task decomposition with no warning. Halting surfaces the problem immediately.
- **`update-strategy: force-push` for publish**: Each rework cycle rewrites history on the feature branch. Force-push keeps the PR diff clean and avoids accumulating fixup commits.
- **`publish` runs every cycle**: The reviewer reads the live PR diff on GitHub. Publishing every cycle ensures the reviewer always sees what was actually built.
- **`strictness: normal` for review**: Only "major" findings fail. Style nits don't cause cycles.
- **`criteria: dev-review` for review**: The existing `dev-review` template already evaluates architectural fit, logic correctness, testing quality, style, and security. No new template needed.
- **Single flat pipeline, no nested pipeline**: The inner loop is defined inline via `loops:`. Inlining is simpler than a separate `build-verify-loop` pipeline file — one file to read, zero cross-pipeline resolution, same behavior. The executor's loop logic is generic and independently testable without a named pipeline.
- **No `metrics` in pipeline YAML**: The metrics system is not yet implemented. Adding it now means dead config parsing. Add when the system exists.
- **No `then` in `StepConfig`**: Unused in this pipeline; `publish` already handles commit+push+PR atomically. YAGNI.
- **Flat context map, structured fields only**: Text-type outputs (study, decompose prompt, build, fix) are never referenced by name in `args` — they flow as envelope content. The context map stores only structured fields and carry values, which are small. No trimming needed.
- **Two-pattern condition grammar**: All conditions in this pipeline are truthy checks (`verify.error`) or equality checks (`review.outcome == "fail"`). A minimal parser handles both. No general expression evaluator needed.
- **`pipeline:` in `StepConfig` is declared but unimplemented**: The field exists for future cross-pipeline composition. Startup fails with a clear message if set. No resolution logic to build now.
- **`study --role` not `--type`**: The existing `study` pipe uses `--role` for perspective selection. All references in this spec use the correct flag name.
- **`{{signal}}` not `{{spec}}`**: The raw user input is the feature spec. `{{signal}}` is the built-in variable for it. `{{spec}}` was undefined — no step or parsed component sets it.
- **Context map stores top-level `error` field**: `error` is a top-level envelope field, not a content field. The rule "store structured content fields" would leave `verify.error` unresolvable. The fix: store `ctx["stepname.error"]` from the top-level field for every step, alongside content fields.
- **Loop counter resets on outer cycle**: Each outer cycle gets a fresh inner loop counter. Without this being explicit, the executor is ambiguous — the inner `max: 5` could be interpreted as global across all cycles.
- **Unresolved template variables produce empty string**: `{{findings}}` has no value in the context map on the first pass. The executor uses `text/template` with `Option("missingkey=zero")` so missing keys render as empty string, never as `<no value>` or an error.
- **`slugify` filter does not add `feat/` prefix**: The pipeline arg handles the prefix explicitly (`branch: "feat/{{feature | slugify}}"`). The worktree handler's auto-prefix (`feat/` when `branch` is empty) is not triggered because the pipeline always passes an explicit branch.
- **Worktree output fields are already correct**: The handler already outputs `content_type: structured` with `path`, `branch`, `base_commit`, and `created`. `{{worktree.path}}` resolves correctly with no changes to the worktree pipe.
- **Decompose before build, not build-then-decompose**: The decompose step runs before any code is written. This produces a plan that can be parallelized. The alternative — build monolithically then split for rework — misses the parallelism opportunity on the first pass.
- **Cycle back to decompose, not build-tasks**: On rework, the decompose pipe re-analyzes with findings context and produces targeted fix tasks. This is smarter than re-running the original task graph, which would redo work that already passed review. The decompose pipe's `rework` template is specifically designed for this.
- **`max_tasks: 8` default**: Caps decomposition granularity. More than 8 tasks for a single feature spec indicates the spec itself should be split into multiple features. The decompose pipe is allowed to produce fewer tasks — a 1-task graph is valid for small specs.
- **File conflict detection at runtime, not config time**: The task graph is produced by the decompose pipe at runtime, not declared in config. File conflicts between tasks at the same dependency level are validated by the graph executor before dispatching sub-agents. This is a fatal error — it means the decompose pipe produced an invalid graph.
- **`on_task_failure: halt` for build-tasks**: If one sub-task fails, the combined output is likely broken. Continuing independent tasks would waste compute on code that can't be verified as a whole. Halt immediately and let the verify-fix loop handle recovery.
- **Sub-agents share the worktree**: Parallel tasks at the same dependency level are guaranteed not to share files (enforced by decompose). Different dependency levels run sequentially. This means parallel sub-agents can safely write to the same worktree without file locks or separate branches.
- **Graph executor uses pipe subprocesses, not goroutines**: Each task runs as a separate pipe subprocess (same mechanism as regular pipe execution). This provides process isolation, independent timeouts, and clean error boundaries. The overhead is acceptable — tasks are typically 5-30 seconds each.
- **`{{task.*}}` variables are graph-scoped**: These template variables only resolve inside `graph.args`. They are not available in regular step args. This prevents confusion between task-level and pipeline-level variables.
- **Inner loop is `[verify, fix]`, not `[build-tasks, verify, fix]`**: Re-running the entire task graph after a fix would overwrite the fix pipe's patches. The fix pipe works directly on worktree files. Verify checks the result. Build-tasks runs once per cycle, before the loop.
- **Decompose uses a mid-tier model**: The decompose pipe produces structured output (task graph JSON), not code. A fast model like Sonnet is sufficient and keeps decomposition latency low. The build pipe uses a more capable model for implementation.
- **PipelineExecutor wraps Runtime, not replaces it**: The existing `Runtime` with `Execute`/`ExecuteStream` stays as the engine for simple planner-built plans (inline template chains). The `PipelineExecutor` is a separate struct that holds a `*Runtime` reference and delegates individual pipe invocations to `runtime.runStep()`. This reuses the existing handler dispatch, envelope validation, observer notifications, and memory injection. The two executors serve different complexity tiers — proportional complexity. Simple queries ("what's on my calendar") never touch the pipeline executor. Complex pipelines get the full graph.
- **Decompose handler extracts JSON defensively**: The handler tries three strategies in order: (1) parse the full response as JSON, (2) extract content between ``` fences, (3) find the first `[` or `{` and parse from there. If all fail, return a fatal error. This is the handler's responsibility — it's defensive parsing of model output, same as any non-deterministic pipe that expects structured output. No shared utility needed yet.
- **Single-task graphs run through the graph executor without short-circuit**: A 1-task DAG is one level with one subprocess invocation. The overhead is negligible (one goroutine, one process spawn — identical cost to a direct pipe call). A special case would add a code path that's harder to test and produces subtly different behavior. The uniform path is simpler.
- **Study context flows to decompose via envelope content**: The parallel study group merges text outputs into a single envelope. This envelope flows to the decompose step as its input per standard sequential semantics. The decompose handler reads `envelope.Content` for codebase context (populating `{{.Context}}` in prompt templates) and reads `flags["spec"]` for the feature spec (from the pipeline arg `spec: "{{signal}}"`). These are independent channels — signal-level template variables vs envelope content — and require no special wiring.
- **Pipeline routing via definition map overwrite**: Pipelines register as `pipe.Definition` entries in the router (they have the same structural fields: name, description, category, triggers). When loading, pipeline definitions are inserted into the definition map after pipe definitions. A pipeline with the same name as a pipe overwrites the pipe's router entry. The pipe remains in the handler registry for direct invocation by the graph executor — only the router's view changes. No new data structures or lookup logic needed.
- **`continue-independent` propagates failure transitively through the DAG**: When a task fails, its ID is added to a `failed` set. Before spawning any task, the executor checks whether any of its `depends_on` IDs are in the `failed` set. If yes, the task is added to the `failed` set (skipped) without running. Because levels are processed sequentially and the `failed` set accumulates, transitive propagation happens naturally: t1 fails → t2 (depends on t1) skipped → t4 (depends on t2) skipped, while t3 (independent of t1) still runs.
- **Fix pipe doesn't need task provenance**: The fix pipe receives verify's error output (test failures with file paths, lint errors with locations) and patches the specific files mentioned. It doesn't need to know which task created those files. The worktree contains all code; verify tested all of it; fix patches what's broken. If fix needed task-level provenance, it would be doing decompose's job. The boundary stays clean: decompose plans, build writes, verify checks, fix patches.
- **Context map stores `any` values, graph executor type-asserts**: The context map stores whatever the envelope's `Content` field contains. For structured envelopes deserialized from JSON subprocess output, this is typically `map[string]any`. The graph executor reads `ctx["decompose.tasks"]` and JSON-round-trips it into `[]TaskNode` (marshal the `any` value back to JSON bytes, then unmarshal into the typed slice). This is defensive — it works regardless of whether Content arrived as a Go struct, `map[string]any`, or `[]any`.
- **Pipeline executor emits one new SSE event type**: `SSEEventPipelineProgress` carries a JSON payload with a `type` discriminator. Four subtypes: `{"type":"loop","name":"verify-fix","iteration":2,"max":5}`, `{"type":"cycle","name":"review-rework","cycle":1,"max":3}`, `{"type":"graph_level","name":"build-tasks","level":0,"tasks":2,"parallel":4}`, `{"type":"graph_task","name":"build-tasks","task":"pricing-table","status":"pass","duration":"4.2s"}`. Individual pipe transitions continue to use the existing `SSEEventStep`. The TUI handles `SSEEventPipelineProgress` for the richer status lines shown in the stream and detail panel. Observability is infrastructure — the executor emits these automatically, pipes don't know about them.
