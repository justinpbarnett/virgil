# Pipeline: build

## Metadata

type: `pipeline`
task_id: `pipeline-build`
prompt: `Build a pipeline that takes a feature spec and produces a pull request ready for human review. Two phases: setup (worktree + parallel study) and a development cycle (build → verify → fix loop, publish, review) that iterates until the work passes or limits are reached.`

## Description

The `build` pipeline is Virgil's fully automated feature development cycle. A user submits a spec — inline text or a path to a spec file — and receives a PR URL when done. No other human input is required during execution.

The pipeline operates in two phases:

**Setup** — creates an isolated git worktree, then runs two study branches in parallel: one from the perspective of a builder (what to create and how) and one from the perspective of a reviewer (what patterns the codebase enforces). Both branches run concurrently against the same codebase and feed their combined context into the first build step.

**Development cycle** — an inner verify-fix loop handles build correctness. When that passes, publish commits, pushes, and creates or updates the PR. A reviewer then reads the PR diff from GitHub and either passes (done) or returns structured findings that cycle back to rebuild. The outer cycle repeats up to `max` times.

---

## Termination Conditions

| Condition | Trigger | Output |
|---|---|---|
| **Review passes** | `review.outcome == "pass"` | PR URL + review summary |
| **Inner loop exhausted** | `verify` fails after N fix attempts | Error envelope with last verify failure and attempt count |
| **Outer cycle exhausted** | `review.outcome == "fail"` after N full cycles | Last PR state + accumulated findings + warning |
| **Fatal error** | Missing credentials, unreachable API, worktree creation fails | Diagnostic error envelope; no partial publish |

---

## Graph

```
user prompt (spec or spec reference)
  → worktree (creates a safe copy of the repo)
  → study --role=builder  ┐ parallel
    study --role=reviewer ┘
  → build (plans, writes code, TDD)
  → verify (lint + tests + plan-check)
  → fix (on failure; attempt N)       ┐ inner loop, max 5
  → verify again …                    ┘ until verify.error == null
  → publish (commit, push, create/update PR)
  → review (reads PR diff via GitHub, criteria: dev-review)
  → pass: done
    fail: cycle back to build (outer cycle, max 3)
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

### New

- `internal/pipelines/build/pipeline.yaml` — the pipeline definition (this spec)
- `internal/config/config.go` — extend config loader to scan `internal/pipelines/` for `pipeline.yaml` files alongside the existing `internal/pipes/` scan; add `PipelineConfig` struct and related types
- `internal/runtime/` — `PipelineExecutor` that walks steps, fans parallel branches, manages loop and cycle counters
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

  - name: build
    pipe: build
    args:
      cwd: "{{worktree.path}}"
      spec: "{{signal}}"
      style: tdd
      findings: "{{findings}}"

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
    steps: [build, verify, fix]
    until: verify.error == null
    max: 5

cycles:
  - name: review-rework
    from: review
    to: build
    condition: review.outcome == "fail"
    carry: findings
    max: 3
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

This becomes the `{{.Context}}` in the `build` pipe's prompt.

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

When `review.outcome == "fail"`, the `findings` array carries back to the `build` step:

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

`{{findings}}` resolves to the JSON-serialized findings array. The `build` pipe selects its `rework` prompt template when `findings` is non-empty, and `initial` otherwise.

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
    Args            map[string]string `yaml:"args"`
    Condition       string            `yaml:"condition"`
    OnBranchFailure string            `yaml:"on_branch_failure"`
}

type ParallelBranch struct {
    Pipe string            `yaml:"pipe"`
    Args map[string]string `yaml:"args"`
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
- Step names within a pipeline must be unique
- Loop `steps` must reference valid step names in the same pipeline
- Cycle `from` and `to` must reference valid step names in the same pipeline
- All conditions must parse as valid two-pattern expressions (see Condition Syntax)

### `PipelineExecutor` in `internal/runtime/`

The runtime needs a `PipelineExecutor` that:

1. **Sequential steps** — executes steps in order; each step receives the previous step's output envelope as input
2. **Parallel groups** — fans out branches concurrently; all branches receive the same input envelope; merges text outputs with role headers; on `on_branch_failure: halt`, cancels remaining branches and propagates the fatal error
3. **Loops** — after each full iteration evaluates the `until` condition against the context map; if false, runs the next iteration; on `max` exhaustion, exits with a fatal error envelope; the loop counter resets at the start of each outer cycle — each cycle gets a fresh `max` attempt budget
4. **Cycles** — after the `from` step, evaluates the `condition`; if true, extracts `carry` fields from the step's structured output and merges them into the context map as top-level keys, then resumes from the `to` step; on `max` exhaustion, completes with the last output and a warning
5. **Template variable resolution** — before each step executes, resolves `{{stepname.fieldname}}` references in `args` against the context map; resolves built-in variables: `{{signal}}` (raw user input), `{{feature}}`, `{{target}}`, and other signal-parsed components; resolves `{{loop.iteration}}` (1-indexed); applies built-in filters: `slugify`, `lower`, `upper`; unresolved variables (key absent from context map) produce empty string — never an error or the literal `<no value>`
6. **Fatal propagation** — a fatal error in any step halts the pipeline; no further steps execute; no publish occurs

### Context map

The executor maintains a context map of structured fields only. Text-type envelope outputs flow through the envelope chain naturally and are never stored in the context map — no pipeline arg references them by name.

What gets stored after each step:
- The top-level `error` field stored as `ctx["stepname.error"]` (null or an error object) — this is how `verify.error` is accessible even though `error` is not a content field
- For structured envelopes: each field of `envelope.content` stored as `ctx["stepname.fieldname"]` — this is how `review.outcome` and `publish.pr_number` are accessible
- After a cycle carry: the named carry field extracted from the `from` step's context entry and stored as a top-level key (`ctx["findings"]` from `ctx["review.findings"]`)
- `{{loop.iteration}}` is a synthetic key updated by the loop executor at the start of each iteration

What is never stored: text envelope content (study output, build output, fix output). These flow as envelope content to the next step — nothing references them by name in `args`.

The context map stays small across the full run: worktree (4 fields), publish (4 fields), review outcome + findings, loop iteration counter. No trimming needed.

### Template resolver and `internal/slug`

The template resolver uses Go's `text/template` with a function map of built-in filters. The `slugify` implementation is extracted from `internal/pipes/worktree/worktree.go` (which already has a private `slugify` function) into `internal/slug/slug.go`. Both the worktree handler and the template resolver import from there.

The filter does not add a `feat/` prefix — that is the pipeline arg's explicit job (`branch: "feat/{{feature | slugify}}"`). The worktree handler's auto-prefix logic still applies when `branch` is empty, but the pipeline always passes an explicit branch.

### Router disambiguation: `build` pipeline vs `build` pipe

The router gives pipeline definitions precedence over pipes with the same name. The `build` pipe continues to exist as a subprocess invoked directly by the pipeline executor — it is never reached by the router once the `build` pipeline is registered.

---

## TUI Behavior

The stream uses the pipeline name as the step prefix. Internal step names are surfaced only in the detail panel.

**Stream (top level)**:
```
❯ add OAuth login to Keep

  ▸ build: creating worktree feat/oauth-login
  ▸ build: studying codebase (2 parallel branches)
  ▸ build: building (TDD)...
  ▸ build: verify failed, fixing (attempt 2)...
  ▸ build: verify passed, publishing PR
  ▸ build: reviewing PR #47...
  ▸ build: review found 2 issues, cycling...
  ▸ build: rebuilding with findings...
  ▸ build: verify passed, updating PR #47
  ▸ build: reviewing PR #47...

  build complete. PR #47 ready for human review.
  2 cycles, 37.2s total.
```

**Detail panel** (expanded):
```
pipeline: build — 37.2s (2 cycles)
  step: worktree — 0.4s
  step: study/parallel — 3.4s
    branch: study[builder] — 2.9s
    branch: study[reviewer] — 3.4s
  loop: verify-fix — cycle 1 — 14.9s (2 iterations)
    iteration 1: build (9.1s) → verify fail → fix (5.8s)
    iteration 2: build (8.2s) → verify pass
  step: publish — 0.8s → PR #47 created
  step: review — 4.2s → fail, 2 major findings
  loop: verify-fix — cycle 2 — 8.3s (1 iteration)
    iteration 1: build (6.1s) → verify pass
  step: publish — 0.6s → PR #47 updated
  step: review — 3.8s → pass
```

---

## Testing Strategy

### Pipeline graph tests (mock all pipes)

- **Happy path** — all pipes return passing envelopes; assert output has `pr_url` and `error == null`
- **Inner loop retry** — `verify` fails once then passes; assert `fix` ran once, `verify` ran twice, no error in output
- **Inner loop exhaustion** — `verify` always fails; assert pipeline exits after max=5 with a fatal error envelope containing the last verify output
- **Outer cycle** — `review` fails once with findings then passes; assert `build` ran twice; assert second `build` invocation received `findings` in args; assert `publish` ran twice
- **Outer cycle exhaustion** — `review` always fails; assert pipeline exits after max=3 cycles with last PR state and a warning
- **Parallel branch failure** — one `study` branch returns fatal; assert pipeline halts, no `build` step runs
- **Worktree fatal** — `worktree` returns fatal; assert nothing else runs
- **Findings carry** — assert `findings` from a failed `review` appear in the `build` step's args on the next cycle
- **Condition parsing** — valid conditions parse correctly; invalid conditions (unsupported operators) are rejected at startup

### Pipe unit tests

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
- Startup validation rejects: missing pipe reference, duplicate step names, `pipeline:` step field set, cycle referencing non-existent step, loop `steps` referencing non-existent step
- Startup validation rejects conditions that use unsupported operators
- `build` pipeline and `build` pipe coexist without conflict; pipeline takes routing precedence

---

## Implementation Order

1. Extract `Slugify` from `internal/pipes/worktree/worktree.go` into `internal/slug/slug.go`; update worktree handler to import from there
2. Add `PipelineConfig` and related structs to `internal/config/config.go`; extend `Load()` to scan `internal/pipelines/`; add startup validation including condition parsing
3. Add `--cwd` flag to `internal/pipes/build/pipe.yaml` and update `build.go` handler
4. Add `--cwd` flag to `internal/pipes/fix/pipe.yaml` and update `fix.go` handler
5. Implement `PipelineExecutor` in `internal/runtime/` (sequential, parallel, loop, cycle, template resolution with filter set using `internal/slug`)
6. Write `internal/pipelines/build/pipeline.yaml`
7. Register pipeline discovery and executor in `cmd/virgil/main.go`
8. Write pipeline graph tests
9. Write updated pipe unit tests for `build` and `fix`

Steps 1, 3, and 4 are independent and can go first. Step 5 depends on steps 1 and 2. Step 6 depends on step 2. Step 7 depends on steps 5 and 6.

---

## Validation Commands

```bash
go build ./...
go vet ./...
go test ./internal/slug/... -v
go test ./internal/config/... -v
go test ./internal/runtime/... -v
go test ./internal/pipes/build/... -v
go test ./internal/pipes/fix/... -v
go test ./internal/pipes/worktree/... -v
go test ./... -count=1
```

---

## Resolved Decisions

- **`on_branch_failure: halt` for the study parallel group**: The `build` pipe requires both perspectives. A partial study context would produce lower-quality output with no warning. Halting surfaces the problem immediately.
- **`update-strategy: force-push` for publish**: Each rework cycle rewrites history on the feature branch. Force-push keeps the PR diff clean and avoids accumulating fixup commits.
- **`publish` runs every cycle**: The reviewer reads the live PR diff on GitHub. Publishing every cycle ensures the reviewer always sees what was actually built.
- **`strictness: normal` for review**: Only "major" findings fail. Style nits don't cause cycles.
- **`criteria: dev-review` for review**: The existing `dev-review` template already evaluates architectural fit, logic correctness, testing quality, style, and security. No new template needed.
- **Single flat pipeline, no nested pipeline**: The inner loop is defined inline via `loops:`. Inlining is simpler than a separate `build-verify-loop` pipeline file — one file to read, zero cross-pipeline resolution, same behavior. The executor's loop logic is generic and independently testable without a named pipeline.
- **No `metrics` in pipeline YAML**: The metrics system is not yet implemented. Adding it now means dead config parsing. Add when the system exists.
- **No `then` in `StepConfig`**: Unused in this pipeline; `publish` already handles commit+push+PR atomically. YAGNI.
- **Flat context map, structured fields only**: Text-type outputs (study, build, fix) are never referenced by name in `args` — they flow as envelope content. The context map stores only structured fields and carry values, which are small. No trimming needed.
- **Two-pattern condition grammar**: All conditions in this pipeline are truthy checks (`verify.error`) or equality checks (`review.outcome == "fail"`). A minimal parser handles both. No general expression evaluator needed.
- **`pipeline:` in `StepConfig` is declared but unimplemented**: The field exists for future cross-pipeline composition. Startup fails with a clear message if set. No resolution logic to build now.
- **`study --role` not `--type`**: The existing `study` pipe uses `--role` for perspective selection. All references in this spec use the correct flag name.
- **`{{signal}}` not `{{spec}}`**: The raw user input is the feature spec. `{{signal}}` is the built-in variable for it. `{{spec}}` was undefined — no step or parsed component sets it.
- **Context map stores top-level `error` field**: `error` is a top-level envelope field, not a content field. The rule "store structured content fields" would leave `verify.error` unresolvable. The fix: store `ctx["stepname.error"]` from the top-level field for every step, alongside content fields.
- **`carry: error` removed from loop**: The fix pipe reads verify's output directly from its input envelope (the prior sequential step). Nothing in the pipeline args references `{{error}}`. The field was dead.
- **Loop counter resets on outer cycle**: Each outer cycle gets a fresh inner loop counter. Without this being explicit, the executor is ambiguous — the inner `max: 5` could be interpreted as global across all cycles.
- **Unresolved template variables produce empty string**: `{{findings}}` has no value in the context map on the first pass. The executor uses `text/template` with `Option("missingkey=zero")` so missing keys render as empty string, never as `<no value>` or an error.
- **Flat inherited context for nested pipelines** (future-proofing): When `pipeline:` is eventually implemented, nested pipelines will inherit the full parent context with no scoping boundary. The two-level nesting cap keeps collision risk negligible.
- **`slugify` filter does not add `feat/` prefix**: The pipeline arg handles the prefix explicitly (`branch: "feat/{{feature | slugify}}"`). The worktree handler's auto-prefix (`feat/` when `branch` is empty) is not triggered because the pipeline always passes an explicit branch.
- **Worktree output fields are already correct**: The handler already outputs `content_type: structured` with `path`, `branch`, `base_commit`, and `created`. `{{worktree.path}}` resolves correctly with no changes to the worktree pipe.
