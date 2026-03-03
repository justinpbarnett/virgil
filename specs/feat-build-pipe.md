# Feature: Build Pipe

## Metadata

type: `feat`
task_id: `build-pipe`
prompt: `Add a non-deterministic build pipe that takes a feature spec and codebase context, plans the implementation approach, then writes code and tests in TDD style. On subsequent cycles, it receives structured reviewer findings and addresses them specifically rather than reimplementing from scratch. The pipe operates within a worktree — all file writes are isolated.`

## Feature Description

The `build` pipe is the core implementation engine for the `dev-feature` pipeline. It receives a feature spec, codebase context (from the study pipe), and optionally reviewer findings (from a previous cycle), then produces working code and tests.

Unlike the existing `code` pipe (which generates a single code artifact from a prompt), `build` orchestrates a multi-file implementation: it plans which files to create or modify, determines test strategy, writes tests first (TDD), then implements until tests pass. It uses the AI provider with an agentic multi-turn loop — the provider gets tools for file I/O and shell commands within the worktree.

On cycle 2+, the pipe receives structured findings from the reviewer. These findings become the primary instruction — the builder addresses each finding specifically (category, file, line, issue, action) rather than starting over.

## User Story

As a pipeline step
I want to plan and implement a feature with tests from a spec and codebase context
So that the pipeline produces working, tested code ready for verification

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/code/code.go` — existing code generation pipe (simpler, single-artifact)
- `internal/pipes/study/study.go` — study pipe whose output is this pipe's input context
- `internal/bridge/bridge.go` — AI provider interface
- `internal/pipehost/host.go` — subprocess harness

### New Files

- `internal/pipes/build/pipe.yaml` — pipe definition
- `internal/pipes/build/build.go` — handler implementation
- `internal/pipes/build/build_test.go` — handler tests
- `internal/pipes/build/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml` with build pipe identity. This pipe is non-deterministic (uses AI) and long-running (up to 5 minutes for complex features).

### Phase 2: Prompt Engineering

Design the system prompt and user prompt templates. The system prompt establishes the builder persona — a meticulous developer who plans before coding, writes tests first, follows project conventions. The user prompt templates handle two cases: initial build (from spec + context) and rework (from spec + context + findings).

### Phase 3: Handler

Implement the handler. The build pipe uses a multi-turn provider interaction — it sets `VIRGIL_MAX_TURNS` high so the provider can iterate (plan → write tests → write code → verify tests pass). The handler collects all file changes and the provider's final summary.

### Phase 4: Tests

Test prompt construction, findings integration, and output structure.

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/build/pipe.yaml`:

```yaml
name: build
description: Plans and implements code changes with tests from a feature spec and codebase context.
category: dev
streaming: true
timeout: 300s

triggers:
  exact:
    - "build feature"
    - "implement feature"
  keywords:
    - build
    - implement
    - construct
  patterns:
    - "build {topic}"
    - "implement {topic}"
    - "build {topic} in {source}"

flags:
  spec:
    description: The feature description to implement.
    default: ""
  style:
    description: Implementation approach.
    values: [tdd, impl-first]
    default: tdd
  findings:
    description: Structured reviewer findings from a previous cycle (JSON string).
    default: ""

vocabulary:
  verbs:
    build: build
    implement: build
    construct: build
  types: {}
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb]
      plan:
        - pipe: build
          flags: {}

prompts:
  system: |
    You are a meticulous software developer. You plan before you code,
    write tests before implementation, and follow project conventions exactly.

    Rules:
    - Read the codebase context carefully. Match existing patterns — naming,
      error handling, import style, test structure.
    - Plan your approach first: list which files to create or modify, what
      tests to write, and the implementation order.
    - Write tests first (TDD). Make them fail. Then implement until they pass.
    - Handle errors explicitly. No silent failures.
    - Do not modify files outside the working directory.
    - When findings from a reviewer are present, address each finding
      specifically. Do not reimplement from scratch — make targeted changes
      that resolve each issue.
    - Keep changes minimal and focused. Do not refactor code that isn't
      related to the feature or findings.

  templates:
    initial: |
      Implement the following feature.

      ## Feature Spec
      {{.Spec}}

      ## Codebase Context
      {{.Context}}

      {{if .Style}}Implementation style: {{.Style}}{{end}}

      Plan your approach, then implement. Write tests first.

    rework: |
      Address the following reviewer findings for this feature.

      ## Feature Spec
      {{.Spec}}

      ## Codebase Context
      {{.Context}}

      ## Reviewer Findings
      {{range .Findings}}
      - [{{.Category}}/{{.Severity}}] {{.File}}:{{.Line}} — {{.Issue}}
        Action: {{.Action}}
      {{end}}

      Address each finding specifically. Do not reimplement from scratch.
      Make targeted changes that resolve each issue.
```

### 2. Define output types

In `internal/pipes/build/build.go`, define:

```go
type BuildOutput struct {
    Summary      string        `json:"summary"`
    Approach     string        `json:"approach"`
    FilesCreated []string      `json:"files_created"`
    FilesModified []string     `json:"files_modified"`
    TestsWritten int           `json:"tests_written"`
    Style        string        `json:"style"`
    CycleNumber  int           `json:"cycle_number"`
}

type ReviewFinding struct {
    Category string `json:"category"`
    Severity string `json:"severity"`
    File     string `json:"file"`
    Line     int    `json:"line"`
    Issue    string `json:"issue"`
    Action   string `json:"action"`
}
```

### 3. Implement the handler

Create `internal/pipes/build/build.go`:

- `NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler`
- Handler logic:
  1. Extract `spec` from flags. If empty, use input envelope's text content as the spec.
  2. Extract `style` from flags (default: `tdd`).
  3. Extract `findings` from flags. If non-empty, parse as `[]ReviewFinding` JSON.
  4. Extract codebase context from the input envelope's structured content (the study pipe's output).
  5. Select prompt template: `rework` if findings present, `initial` otherwise.
  6. Execute the prompt template with spec, context, style, and findings data.
  7. Call `provider.Complete()` with the system prompt and rendered user prompt.
  8. Parse the provider's response to extract a summary of what was built. The provider response is expected to describe its actions — files created/modified, tests written, approach taken.
  9. Build `BuildOutput` struct from the parsed response.
  10. Return envelope with `content_type: structured`.

- The provider invocation uses the high `VIRGIL_MAX_TURNS` env var so it can iterate through plan → test → implement → verify cycles within a single pipe execution. The provider binary (e.g., Claude) handles the actual file I/O and shell commands via its own tool use.

### 4. Implement stream handler

Create `NewStreamHandler` following the same pattern as the code pipe:

- `NewStreamHandler(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler`
- Streams the provider's output chunks to the sink for TUI display
- Final envelope contains the structured `BuildOutput`

### 5. Create subprocess entry point

Create `internal/pipes/build/cmd/main.go`:

```go
func main() {
    logger := pipehost.NewPipeLogger("build")
    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("build", err.Error())
    }
    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("build", err.Error())
    }
    compiled := build.CompileTemplates(pc)
    pipehost.RunWithStreaming(provider,
        build.NewHandlerWith(provider, pc, compiled, logger),
        func(sp bridge.StreamingProvider) pipe.StreamHandler {
            return build.NewStreamHandlerWith(sp, pc, compiled, logger)
        },
    )
}
```

### 6. Write tests

Create `internal/pipes/build/build_test.go`:

- **TestBuildInitial** — happy path: spec + context, no findings. Verify prompt uses `initial` template, output is `BuildOutput` with correct fields.
- **TestBuildWithFindings** — spec + context + findings. Verify prompt uses `rework` template, findings are rendered in the prompt.
- **TestBuildTDDStyle** — verify `style: tdd` appears in the prompt
- **TestBuildImplFirstStyle** — verify `style: impl-first` appears in the prompt
- **TestBuildEmptySpec** — no spec flag and no input content, returns fatal error
- **TestBuildProviderError** — provider returns error, returns classified error envelope
- **TestBuildProviderEmpty** — provider returns empty, returns warn error
- **TestBuildFindingsParsing** — malformed findings JSON, returns fatal error
- **TestBuildStreamHandler** — verify streaming works, chunks flow, final envelope correct
- **TestBuildTemplateCompilation** — verify all templates compile without error

### 7. Add to justfile build

Update the `build` recipe in `justfile`:
```
go build -o internal/pipes/build/run ./internal/pipes/build/cmd/
```

## Testing Strategy

### Unit Tests
- `internal/pipes/build/build_test.go` — prompt construction, findings integration, template selection, error handling, streaming

### Edge Cases
- Empty findings array (first cycle, no rework needed)
- Findings with missing fields (graceful handling)
- Very large spec (exceeds provider context — should the pipe truncate or error?)
- Provider timeout on long implementations

## Risk Assessment

- **Medium risk — prompt quality is critical.** The build pipe's effectiveness depends on the system prompt and template quality. Poor prompts produce poor code. This will require iteration.
- **Multi-turn provider interaction is the complexity center.** The provider needs enough turns to plan, write tests, implement, and self-verify. Too few turns and the implementation is incomplete. Too many and it wastes time.
- **Findings integration must be precise.** The builder must address findings specifically (file, line, action) not vaguely. The prompt template must present findings in a format the model can act on mechanically.

## Validation Commands

```bash
go test ./internal/pipes/build/... -v -count=1
go build ./internal/pipes/build/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
