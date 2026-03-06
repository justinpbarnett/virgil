# Feature: Decompose Pipe

## Metadata

type: `feat`
task_id: `decompose-pipe`
prompt: `Add a decompose pipe that takes a feature spec (or task description) and produces a task DAG suitable for the GraphExecutor. The pipe calls an AI provider via the bridge abstraction to analyze the spec and codebase context, then outputs a validated directed acyclic graph of sub-tasks. Each task carries a mini-spec, file list, and dependency edges. A file-disjoint constraint guarantees no two tasks at the same dependency level share files, enabling safe parallel execution. When reviewer findings are provided from a previous cycle, the pipe produces targeted rework tasks instead of full re-decomposition.`

## Description

The `decompose` pipe is the planning stage of the build pipeline. It sits between the study step (which produces codebase context) and the graph executor (which dispatches sub-agents). Its job is to break a feature spec into discrete, independently-buildable sub-tasks arranged as a DAG.

The pipe is non-deterministic — it calls an AI provider to perform the decomposition. The handler is responsible for prompt construction, JSON extraction from model output, and rigorous validation of the resulting graph before returning it as structured envelope content.

Two modes of operation:

**Initial decomposition** — given a feature spec and codebase context, produce a full task DAG from scratch. The AI analyzes the spec to identify natural boundaries (types, interfaces, implementations, integration wiring) and orders them by dependency.

**Rework decomposition** — given a feature spec, codebase context, and structured reviewer findings from a failed review cycle, produce targeted fix tasks. The AI reads the existing code in the worktree and creates tasks that address each finding specifically, without re-implementing the entire feature.

## User Story

As the build pipeline's decompose step
I want to break a feature spec into a validated task DAG with file-disjoint guarantees
So that the graph executor can dispatch independent tasks in parallel without file conflicts

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/build/build.go` — reference for agentic pipe handler pattern (prompt prep, provider call, structured output)
- `internal/pipes/build/pipe.yaml` — reference for pipe.yaml structure (flags, prompts, templates, vocabulary)
- `internal/pipes/build/cmd/main.go` — reference for subprocess entry point
- `internal/bridge/bridge.go` — `Provider` interface, `Complete()` method
- `internal/bridge/agentic.go` — `RunAgenticLoop`, not needed here (decompose is single-turn)
- `internal/pipehost/host.go` — subprocess harness, `Run()`, `BuildProviderFromEnv()`
- `internal/pipeutil/pipeutil.go` — `CompileTemplates`, `ExecuteTemplate`, `StripMarkdownFences`, `FlagOrDefault`
- `internal/envelope/envelope.go` — `Envelope`, `ContentStructured`, error helpers
- `internal/pipe/pipe.go` — `Handler`, `Definition`
- `internal/config/config.go` — `PipeConfig`, `UnmarshalPipeConfig`
- `specs/pipeline-build.md` — pipeline spec that consumes decompose output

### New Files

- `internal/pipes/decompose/pipe.yaml` — pipe definition
- `internal/pipes/decompose/decompose.go` — handler: prompt construction, provider call, JSON extraction, DAG validation
- `internal/pipes/decompose/decompose_test.go` — unit tests
- `internal/pipes/decompose/cmd/main.go` — subprocess entry point

---

## Data Structures

### Task DAG types (`decompose.go`)

```go
// Task represents a single node in the decomposition DAG.
type Task struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Spec      string   `json:"spec"`
    Files     []string `json:"files"`
    DependsOn []string `json:"depends_on"`
}

// DecomposeOutput is the structured content of the output envelope.
type DecomposeOutput struct {
    Tasks []Task `json:"tasks"`
}

// ReviewFinding represents a single finding from the review pipe.
// Matches the structure used by the build and review pipes.
type ReviewFinding struct {
    Category string `json:"category"`
    Severity string `json:"severity"`
    File     string `json:"file"`
    Line     int    `json:"line"`
    Issue    string `json:"issue"`
    Action   string `json:"action"`
}

// templateData is the input to prompt templates.
type templateData struct {
    Spec     string
    Context  string
    MaxTasks string
    Findings []ReviewFinding
}
```

These types are internal to the decompose package. The graph executor in `internal/runtime/graph.go` defines its own `TaskNode` type with the same JSON shape and unmarshals from the envelope content via JSON round-trip (see pipeline-build.md resolved decisions).

---

## Pipe Configuration

### `internal/pipes/decompose/pipe.yaml`

```yaml
name: decompose
description: Breaks a feature spec into sub-tasks with a dependency graph for parallel execution.
category: dev
streaming: false
timeout: 60s
provider: anthropic
model: sonnet

triggers:
  exact: []
  keywords: []
  patterns: []

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

vocabulary:
  verbs: {}
  types: {}
  sources: {}
  modifiers: {}

prompts:
  system: |
    You are a software architect who decomposes feature specs into discrete,
    implementable sub-tasks. Each task must be self-contained and buildable
    independently by a developer who has NOT read the full feature spec.

    Rules:
    - Analyze the spec and codebase context to identify natural task boundaries.
    - Each task gets its own mini-spec with enough detail for an implementer.
    - List ALL files each task will create or modify.
    - CRITICAL CONSTRAINT: No two tasks at the same dependency level may share
      files. This is a hard requirement for safe parallel execution. If two
      pieces of work touch the same file, they must be in a dependency
      relationship (one depends on the other), not siblings.
    - Order tasks by dependency: foundational types/interfaces first, then
      implementations that depend on them, then integration/wiring last.
    - Keep tasks focused. One task = one logical concern. A task that touches
      5+ files is probably too broad — consider splitting.
    - When findings are present, create targeted tasks that address each
      finding. Do not re-decompose the entire feature.
    - Task IDs must be short, unique strings (e.g. "t1", "t2", "t3").
    - depends_on must only reference IDs of other tasks in the same graph.
    - The graph must be a DAG — no circular dependencies.

    Respond with ONLY a JSON object. No explanation, no markdown fences,
    no text before or after. The JSON must have this exact shape:

    {
      "tasks": [
        {
          "id": "t1",
          "name": "short-kebab-name",
          "spec": "Detailed mini-spec...",
          "files": ["path/to/file.go", "path/to/file_test.go"],
          "depends_on": []
        }
      ]
    }

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
```

---

## Handler Logic

### `decompose.go` — `preparePrompt`

1. Read `spec` from flags. Fatal error if empty — the build pipeline always passes spec via flags.
2. Read `max_tasks` from flags; default `"8"`.
3. Read `findings` from flags. If non-empty, unmarshal as `[]ReviewFinding`. Select `rework` template. Otherwise select `initial` template.
4. Read codebase context from `envelope.ContentToText(input.Content, input.ContentType)` — this is the merged study output that flows as envelope content from the parallel study step.
5. Execute the selected template with `templateData{Spec, Context, MaxTasks, Findings}`.
6. Return `(systemPrompt, userPrompt, nil)`.

### `decompose.go` — `runDecompose`

1. Call `preparePrompt`. On error, return error envelope.
2. Call `provider.Complete(ctx, systemPrompt, userPrompt)`. This is a single-turn call — decompose does not use agentic tool loops.
3. Extract JSON from the response using a three-strategy approach:
   a. Try `json.Unmarshal` on the full response.
   b. Try `pipeutil.StripMarkdownFences` then unmarshal.
   c. Find the first `{` and parse from there.
   d. If all fail, return fatal error: `"failed to extract task graph from provider response"`.
4. Validate the parsed `DecomposeOutput` (see Validation section).
5. On validation failure, return fatal error with the validation message.
6. Return structured envelope with `DecomposeOutput` as content.

### `decompose.go` — Exports

Follow the build pipe pattern:

```go
// CompileTemplates re-exports pipeutil.CompileTemplates for use by cmd/main.go.
var CompileTemplates = pipeutil.CompileTemplates

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler
func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler
```

Note: decompose uses `bridge.Provider` (single-turn `Complete`), not `bridge.AgenticProvider`. No tool use needed — the AI reasons about task decomposition purely from the prompt.

### `cmd/main.go`

```go
package main

import (
    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/decompose"
)

func main() {
    logger := pipehost.NewPipeLogger("decompose")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("decompose", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("decompose", err.Error())
    }

    compiled := decompose.CompileTemplates(pc)

    logger.Info("initialized")
    pipehost.Run(
        decompose.NewHandlerWith(provider, pc, compiled, logger),
        nil, // no streaming — decompose is synchronous
    )
}
```

---

## Validation Algorithm

All validation runs in the handler after JSON extraction, before returning the envelope. Validation is a pure function: `func validateDAG(output DecomposeOutput, maxTasks int) error`.

### Step 1: Required fields

For each task in `output.Tasks`:
- `id` must be non-empty
- `name` must be non-empty
- `spec` must be non-empty
- `files` must be non-empty (a task with no files has no observable effect)

Return on first violation: `"task %q: missing required field %s"`.

### Step 2: Unique IDs

Collect all task IDs into a set. If any duplicates, return: `"duplicate task ID: %s"`.

### Step 3: Max tasks cap

If `len(output.Tasks) > maxTasks`, return: `"task count %d exceeds max_tasks %d"`.

### Step 4: Valid dependency references

For each task, every entry in `depends_on` must reference an ID that exists in the task set. Return: `"task %q depends on unknown task %q"`.

### Step 5: Cycle detection (Kahn's algorithm)

1. Build in-degree map and adjacency list from `depends_on` edges.
2. Initialize queue with all tasks having in-degree 0.
3. Process queue: for each dequeued task, decrement in-degree of its dependents; enqueue any that reach 0.
4. If processed count != total task count, the graph has a cycle. Return: `"task graph contains a cycle"`.

### Step 6: File-disjoint validation per dependency level

1. During the Kahn's traversal in step 5, assign each task a level (depth from root).
2. Group tasks by level.
3. For each level, collect all files across tasks at that level. If any file appears in more than one task at the same level, return: `"file %q appears in multiple tasks at level %d: %s, %s"`.

### Implementation note

Steps 5 and 6 are combined into a single pass. Kahn's algorithm naturally produces level assignments (each task's level = max level of its dependencies + 1). The file-disjoint check runs per level as tasks are grouped.

---

## Output Envelope

```json
{
  "pipe": "decompose",
  "action": "decompose",
  "content": {
    "tasks": [
      {
        "id": "t1",
        "name": "pricing-table",
        "spec": "Create internal/bridge/pricing.go with a pricing table mapping (provider, model) to cost per million tokens. Define a PricingEntry struct with InputCostPerMillion and OutputCostPerMillion float64 fields. Include entries for claude-sonnet-4-20250514, gpt-4o, gemini-2.5-pro. Add a LookupCost(provider, model string) function that returns the entry or a zero-value entry for unknown models. Write tests in pricing_test.go covering known lookups and unknown model fallback.",
        "files": ["internal/bridge/pricing.go", "internal/bridge/pricing_test.go"],
        "depends_on": []
      },
      {
        "id": "t2",
        "name": "anthropic-usage",
        "spec": "Update internal/bridge/anthropic.go to extract token usage from the API response...",
        "files": ["internal/bridge/anthropic.go", "internal/bridge/anthropic_test.go"],
        "depends_on": ["t1"]
      },
      {
        "id": "t3",
        "name": "openai-usage",
        "spec": "Update internal/bridge/openai.go to extract token counts from the response JSON...",
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

On error:

```json
{
  "pipe": "decompose",
  "action": "decompose",
  "error": {
    "message": "task graph contains a cycle",
    "severity": "fatal",
    "retryable": false
  },
  "content_type": ""
}
```

---

## Rework Mode

When `findings` is non-empty in flags:

1. The handler selects the `rework` prompt template instead of `initial`.
2. The system prompt's instruction "When findings are present, create targeted tasks that address each finding" guides the AI to produce focused fix tasks.
3. The AI reads the existing code context (from the study step's merged output flowing as envelope content) plus the specific findings, and produces tasks that surgically address each issue.
4. Rework tasks are typically fewer than the initial decomposition — a 4-task initial graph might produce 1-2 rework tasks.
5. Rework tasks still go through the same validation (DAG validity, file-disjoint, required fields). A rework graph is a fresh DAG, not a patch on the previous one.

Example rework output for 2 findings:

```json
{
  "tasks": [
    {
      "id": "r1",
      "name": "fix-token-validation",
      "spec": "In internal/auth/handler.go at line 42, call ValidateToken() before accessing token.Claims. Add a test case in handler_test.go that verifies invalid tokens are rejected before claim extraction.",
      "files": ["internal/auth/handler.go", "internal/auth/handler_test.go"],
      "depends_on": []
    },
    {
      "id": "r2",
      "name": "fix-error-handling",
      "spec": "In internal/auth/middleware.go at line 18, wrap the error from AuthService.Verify() with context before returning...",
      "files": ["internal/auth/middleware.go", "internal/auth/middleware_test.go"],
      "depends_on": []
    }
  ]
}
```

Both tasks are at level 0 (no dependencies), so they execute in parallel. They touch disjoint files, satisfying the file-disjoint constraint.

---

## JSON Extraction Strategy

The handler tries three strategies to extract JSON from the model response, in order:

1. **Direct parse** — `json.Unmarshal([]byte(response), &output)`. Works when the model follows the "respond with ONLY JSON" instruction.
2. **Strip markdown fences** — `pipeutil.StripMarkdownFences(response)` then unmarshal. Handles models that wrap JSON in ` ```json ... ``` ` despite being told not to.
3. **Find first brace** — scan for the first `{` character, take the substring from there to end, unmarshal. Handles models that prepend explanatory text before the JSON.

If all three fail, return a fatal error with the raw response truncated to 200 characters for diagnostics: `"failed to extract task graph from provider response: <truncated response>"`.

---

## Test Cases

### `decompose_test.go`

All tests use a mock provider that returns canned JSON responses. No real AI calls.

#### Valid DAG — linear chain

```go
// Input: provider returns 3 tasks in a linear chain (t1 → t2 → t3)
// Each task touches disjoint files.
// Assert: output envelope has ContentStructured, 3 tasks, no error.
// Assert: task IDs, names, specs, files, depends_on all populated correctly.
```

#### Valid DAG — diamond dependency

```go
// Input: provider returns 4 tasks: t1 (root), t2 depends on t1, t3 depends on t1,
// t4 depends on t2 and t3. All file-disjoint per level.
// Assert: valid output, 4 tasks, correct dependency structure.
```

#### Valid DAG — single task

```go
// Input: provider returns 1 task with no dependencies.
// Assert: valid output. No over-decomposition.
```

#### Cyclic DAG rejection

```go
// Input: provider returns tasks where t1 → t2 → t3 → t1 (cycle).
// Assert: fatal error envelope with message containing "cycle".
```

#### File overlap detection — same level

```go
// Input: provider returns t1 and t2 with no dependencies (both at level 0).
// t1.files = ["foo.go", "bar.go"], t2.files = ["bar.go", "baz.go"]
// "bar.go" is shared at level 0.
// Assert: fatal error envelope with message containing "bar.go" and "level 0".
```

#### File overlap allowed — different levels

```go
// Input: provider returns t1 (files: ["foo.go"]) and t2 (depends_on: ["t1"],
// files: ["foo.go", "bar.go"]). Same file at different levels is fine — t2
// runs after t1 completes.
// Assert: valid output, no error.
```

#### Max tasks exceeded

```go
// Input: provider returns 10 tasks, max_tasks flag is "5".
// Assert: fatal error envelope with message containing "exceeds max_tasks".
```

#### Missing required field — empty spec

```go
// Input: provider returns a task with spec: "".
// Assert: fatal error with message containing "missing required field".
```

#### Missing required field — empty files

```go
// Input: provider returns a task with files: [].
// Assert: fatal error with message containing "missing required field".
```

#### Duplicate task IDs

```go
// Input: provider returns two tasks both with id: "t1".
// Assert: fatal error with message containing "duplicate task ID".
```

#### Unknown dependency reference

```go
// Input: provider returns task t2 with depends_on: ["t99"] where t99 does not exist.
// Assert: fatal error with message containing "unknown task".
```

#### Rework mode — findings trigger rework template

```go
// Input: flags contain findings JSON. Provider returns targeted fix tasks.
// Assert: output is valid. Verify that the handler used the "rework" template
// by checking the prompt passed to the mock provider contains "Reviewer Findings".
```

#### Rework mode — no findings uses initial template

```go
// Input: flags have no findings. Provider returns initial decomposition.
// Assert: output is valid. Verify prompt contains "Decompose the following feature".
```

#### Empty spec — fatal error

```go
// Input: no spec in flags (spec flag is empty string).
// Assert: fatal error with message "no spec provided for decompose".
```

#### JSON extraction — markdown fences

```go
// Input: provider response wraps JSON in ```json ... ```
// Assert: JSON extracted successfully, valid output.
```

#### JSON extraction — prefixed text

```go
// Input: provider response has "Here is the task graph:\n{...}"
// Assert: JSON extracted from first brace, valid output.
```

#### JSON extraction — total failure

```go
// Input: provider response is "I don't understand the request."
// Assert: fatal error with message containing "failed to extract task graph".
```

#### Provider error propagation

```go
// Input: mock provider returns an error.
// Assert: error envelope with classified error (retryable for timeout, fatal otherwise).
```

### Validation function tests (table-driven)

The `validateDAG` function is exported for testing (or tested via unexported test in the same package). Table-driven tests cover each validation step independently:

```go
func TestValidateDAG(t *testing.T) {
    tests := []struct {
        name     string
        input    DecomposeOutput
        maxTasks int
        wantErr  string // substring match; "" means no error
    }{
        {
            name: "valid linear chain",
            input: DecomposeOutput{Tasks: []Task{
                {ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
                {ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t1"}},
            }},
            maxTasks: 8,
            wantErr:  "",
        },
        {
            name: "cycle detected",
            input: DecomposeOutput{Tasks: []Task{
                {ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{"t2"}},
                {ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t1"}},
            }},
            maxTasks: 8,
            wantErr:  "cycle",
        },
        {
            name: "file overlap at same level",
            input: DecomposeOutput{Tasks: []Task{
                {ID: "t1", Name: "a", Spec: "do a", Files: []string{"shared.go"}, DependsOn: []string{}},
                {ID: "t2", Name: "b", Spec: "do b", Files: []string{"shared.go"}, DependsOn: []string{}},
            }},
            maxTasks: 8,
            wantErr:  "shared.go",
        },
        // ... additional cases per validation step
    }
    // ...
}
```

---

## File Locations

| File | Purpose |
|---|---|
| `internal/pipes/decompose/pipe.yaml` | Pipe definition: triggers, flags, prompts, vocabulary |
| `internal/pipes/decompose/decompose.go` | Handler: prompt construction, provider call, JSON extraction, DAG validation |
| `internal/pipes/decompose/decompose_test.go` | Unit tests with mock provider |
| `internal/pipes/decompose/cmd/main.go` | Subprocess entry point |

---

## Implementation Order

1. Create `internal/pipes/decompose/pipe.yaml` with the configuration above.
2. Create `internal/pipes/decompose/decompose.go`:
   - Define `Task`, `DecomposeOutput`, `ReviewFinding`, `templateData` types.
   - Implement `preparePrompt` (template selection, data assembly).
   - Implement `extractJSON` (three-strategy extraction).
   - Implement `validateDAG` (required fields, unique IDs, max tasks, valid refs, cycle detection, file-disjoint per level).
   - Implement `runDecompose` (orchestrates prompt → provider → extract → validate → envelope).
   - Implement `NewHandler` / `NewHandlerWith` constructors.
3. Create `internal/pipes/decompose/cmd/main.go` subprocess entry point.
4. Create `internal/pipes/decompose/decompose_test.go` with all test cases above.
5. Add decompose binary to the build system (justfile `build` target).

Steps 1-4 are a single atomic unit — the pipe is not usable without all four files. Step 5 is a one-line addition to the justfile.

---

## Design Decisions

- **Single-turn provider call, not agentic loop**: Decompose reasons about architecture — it does not write or read files. A single `provider.Complete()` call with a well-crafted prompt is sufficient. No tools needed. This keeps the pipe simple, fast, and cheap.
- **Validation in the handler, not the graph executor**: The decompose pipe owns the contract for its output. Catching invalid graphs here produces clear error messages ("task t3 depends on unknown task t99") rather than opaque failures downstream in the graph executor. The graph executor performs its own validation as a defense-in-depth measure, but the decompose pipe should never produce invalid output.
- **File-disjoint constraint enforced by both prompt and code**: The system prompt instructs the AI to respect the constraint. The `validateDAG` function enforces it programmatically. Belt and suspenders — model compliance is probabilistic; validation is deterministic.
- **Mid-tier model (Sonnet)**: Decomposition produces structured output (JSON), not code. A fast, capable model keeps latency low (target: under 10s for the provider call). The build pipe uses whatever the pipeline configures for implementation.
- **`max_tasks` as a flag, not hardcoded**: Different specs need different granularity. The pipeline passes `max_tasks: "8"` as a default, but a user invoking decompose directly could override it. More than 8 tasks for a single feature suggests the spec should be split.
- **Three-strategy JSON extraction**: Models sometimes wrap JSON in markdown fences or prepend text despite clear instructions. The handler handles this defensively rather than failing on minor formatting deviations.
- **Rework produces a fresh DAG, not a patch**: A rework graph is structurally independent of the initial decomposition. It references files that need fixing but carries no task IDs or dependency edges from the original graph. This avoids stale-reference bugs when the initial graph's structure no longer applies after code has been modified.
- **No streaming**: The output is a JSON blob, not a text stream. The pipe returns synchronously. The pipeline executor waits for the complete result before dispatching the graph.

---

## Validation Commands

```bash
go build ./internal/pipes/decompose/...
go vet ./internal/pipes/decompose/...
go test ./internal/pipes/decompose/... -v -count=1
just build  # must include decompose binary
just test   # all tests pass
```

---

## Review Notes

Changes made during review:

1. **`model: claude-sonnet-4-20250514` changed to `model: sonnet`** — Every existing pipe.yaml uses short model names (`opus`, `gemini-3.1-pro-preview`, `kimi-k2.5`). Full model IDs are not the convention. The provider+model resolution layer handles mapping.

2. **Triggers, vocabulary, and templates sections emptied** — The decompose pipe is an internal pipeline step invoked programmatically by the build pipeline. It is never routed to via user input. Adding keywords like `decompose`, `breakdown`, and `split` to triggers and vocabulary creates conflicts with the `analyze` pipe, which already claims those terms. Planner templates are also unnecessary since no user query should route directly to decompose. This follows the principle of proportional complexity — an internal pipe should not pollute the router's search space.

3. **Removed spec-from-envelope fallback in `preparePrompt`** — The original spec said to fall back to `envelope.ContentToText()` for the spec when the flag is empty, but step 4 uses the same `ContentToText` call for codebase context. Both cannot read from the same source. In the build pipeline, spec is always passed via flags and envelope content carries codebase context from the study step. The fallback was misleading and would mask bugs (silently using codebase context as the spec). Fatal error on empty spec flag is the correct behavior.

4. **Added `CompileTemplates` export declaration** — The `cmd/main.go` code calls `decompose.CompileTemplates(pc)` but the handler section did not declare it. Every other pipe (build, review, analyze) exports this as `var CompileTemplates = pipeutil.CompileTemplates`. Added for completeness.

**What was kept as-is (and why):**

- **Kahn's algorithm for cycle detection** — Hand-rolled is appropriate here. The algorithm is ~20 lines of Go and well-understood. A third-party DAG library would add a dependency for trivial functionality. The combined cycle-detection + level-assignment in a single pass is clean.
- **Three-strategy JSON extraction** — Proven pattern already used by other pipes in the codebase.
- **`ReviewFinding` type duplication** — Each pipe is a separate subprocess binary. Sharing types across pipe packages would create import dependencies between independent binaries. Duplication is intentional.
- **Validation in the handler** — Correct placement per "deterministic first" principle. The prompt asks the AI to respect constraints; the code enforces them. Belt and suspenders.
- **Single-turn provider call** — Decompose reasons about architecture, it does not read/write files. No tools needed. This is proportional complexity.
- **No streaming** — Output is a JSON blob consumed by the graph executor, not rendered to the user. Correct.
- **Prompt length** — The system prompt is detailed but every rule serves a purpose (file-disjoint constraint, JSON shape, task granularity guidance). Not bloated for the complexity of the task.
