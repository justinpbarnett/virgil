# Feature: Fix Pipe

## Metadata

type: `feat`
task_id: `fix-pipe`
prompt: `Add a non-deterministic fix pipe that reads structured verification failures and applies targeted corrections. The pipe is a scalpel — it makes minimum changes to resolve specific failures without re-planning or re-architecting. Each iteration should converge toward passing, not diverge.`

## Feature Description

The `fix` pipe sits inside the inner verify-fix loop of the `dev-feature` pipeline. When the verify pipe reports test failures or lint errors, the fix pipe reads those structured errors and applies targeted corrections.

The key design principle is **convergence**. Each fix iteration should bring the code closer to passing, not introduce new problems. The pipe makes the minimum change necessary to address each reported failure. If the same error persists after multiple attempts, the pipe should try a different approach rather than repeating the same change.

The fix pipe is distinct from the build pipe. Build plans and implements. Fix reads a precise error report and patches. Build is a sledgehammer. Fix is a scalpel.

## User Story

As a pipeline step
I want to read structured test/lint failures and apply minimal targeted fixes
So that the inner verify-fix loop converges toward all tests passing

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/verify/verify.go` — verify pipe whose output is this pipe's input (defines `VerifyOutput`, `TestFailure`, `LintError`)
- `internal/pipes/code/code.go` — code pipe pattern for AI-based code generation
- `internal/bridge/bridge.go` — AI provider interface

### New Files

- `internal/pipes/fix/pipe.yaml` — pipe definition
- `internal/pipes/fix/fix.go` — handler implementation
- `internal/pipes/fix/fix_test.go` — handler tests
- `internal/pipes/fix/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml`. Fix is a dev-category non-deterministic pipe.

### Phase 2: Prompt Engineering

Design the system prompt to enforce the scalpel constraint: fix only the reported errors, don't refactor, don't add features, don't re-architect. The user prompt template renders the structured failures into a format the model can act on precisely.

### Phase 3: Handler

Implement the handler. It extracts the structured `VerifyOutput` from the input envelope, renders the failures into a fix prompt, and calls the provider. The provider reads the relevant files and makes targeted edits.

### Phase 4: Tests

Test prompt construction from various failure combinations, error handling, and convergence behavior.

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/fix/pipe.yaml`:

```yaml
name: fix
description: Reads verification failures and applies targeted fixes to code and tests.
category: dev
streaming: true
timeout: 120s

triggers:
  exact:
    - "fix errors"
    - "fix failures"
  keywords:
    - fix
    - repair
    - correct
    - patch
  patterns:
    - "fix {topic}"
    - "fix errors in {topic}"

flags:
  scope:
    description: How broadly to fix.
    values: [targeted, broad]
    default: targeted
  attempt:
    description: Which fix attempt this is (for convergence tracking).
    default: "1"

vocabulary:
  verbs:
    fix: fix
    repair: fix
    correct: fix
    patch: fix
  types: {}
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb]
      plan:
        - pipe: fix
          flags: {}

prompts:
  system: |
    You are a precise debugger. You fix specific errors with minimal changes.

    Rules:
    - Fix ONLY the reported errors. Do not refactor surrounding code.
    - Do not add features, improve style, or reorganize unless directly
      required to fix the error.
    - Read the failing file before making changes. Understand the context.
    - For test failures: check if the test expectation is wrong or if the
      implementation is wrong. Fix the correct one.
    - For lint errors: apply the minimum change to satisfy the rule.
    - If this is attempt 2+ for the same error, try a DIFFERENT approach.
      Do not repeat the same fix that didn't work.
    - After making fixes, verify your changes are syntactically valid.
    - Never introduce new compilation errors, new test failures, or new
      lint violations.

  templates:
    targeted: |
      Fix the following verification failures.

      ## Test Failures
      {{range .TestFailures}}
      - **{{.Package}}/{{.Test}}** ({{.File}})
        Error: {{.Error}}
        {{if .Expected}}Expected: {{.Expected}}{{end}}
        {{if .Actual}}Actual: {{.Actual}}{{end}}
      {{end}}

      {{if .LintErrors}}
      ## Lint Errors
      {{range .LintErrors}}
      - **{{.File}}:{{.Line}}** [{{.Rule}}] {{.Message}}
      {{end}}
      {{end}}

      Fix each failure with the minimum change necessary.
      {{if gt .Attempt 1}}
      This is attempt {{.Attempt}}. Previous fix attempts did not resolve
      all failures. Try a different approach for persistent errors.
      {{end}}

    broad: |
      Fix the following verification failures, and scan for similar issues
      in related files.

      ## Test Failures
      {{range .TestFailures}}
      - **{{.Package}}/{{.Test}}** ({{.File}})
        Error: {{.Error}}
        {{if .Expected}}Expected: {{.Expected}}{{end}}
        {{if .Actual}}Actual: {{.Actual}}{{end}}
      {{end}}

      {{if .LintErrors}}
      ## Lint Errors
      {{range .LintErrors}}
      - **{{.File}}:{{.Line}}** [{{.Rule}}] {{.Message}}
      {{end}}
      {{end}}

      Fix each reported failure, then check nearby code for the same
      patterns and fix those too.
```

### 2. Define output types

In `internal/pipes/fix/fix.go`, define:

```go
type FixOutput struct {
    Summary       string     `json:"summary"`
    FilesModified []string   `json:"files_modified"`
    FixesApplied  int        `json:"fixes_applied"`
    Attempt       int        `json:"attempt"`
}

type fixPromptData struct {
    TestFailures []TestFailure
    LintErrors   []LintError
    Attempt      int
}

// TestFailure and LintError are imported from the verify package types,
// but redefined here to maintain pipe isolation (pipes don't import each other).
type TestFailure struct {
    File     string `json:"file"`
    Test     string `json:"test"`
    Package  string `json:"package"`
    Error    string `json:"error"`
    Expected string `json:"expected,omitempty"`
    Actual   string `json:"actual,omitempty"`
}

type LintError struct {
    File    string `json:"file"`
    Line    int    `json:"line"`
    Column  int    `json:"column"`
    Rule    string `json:"rule"`
    Message string `json:"message"`
}
```

Note: `TestFailure` and `LintError` are redefined in the fix package rather than imported from verify. Pipes don't import each other (isolation principle). The types flow via JSON through the envelope — the fix pipe deserializes them from the verify pipe's structured content.

### 3. Implement the handler

Create `internal/pipes/fix/fix.go`:

- `NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler`
- Handler logic:
  1. Extract the verify output from the input envelope's structured content. The content is a JSON object matching `VerifyOutput` shape — deserialize into local types.
  2. Extract test failures and lint errors from the verify output.
  3. If no failures (empty test failures and empty lint errors), return a pass-through envelope with a summary "Nothing to fix".
  4. Read `scope` flag (default: `targeted`). Select the corresponding prompt template.
  5. Read `attempt` flag (default: "1"). Parse to int.
  6. Render the prompt template with failures and attempt number.
  7. Call `provider.Complete()` with the system prompt and rendered user prompt.
  8. Parse the provider's response — the provider describes what files it modified and what fixes it applied.
  9. Return envelope:
     - `content`: `FixOutput` struct
     - `content_type`: `structured`
     - `error`: null (fix itself doesn't fail unless the provider errors — whether the fixes actually work is determined by the next verify pass)

### 4. Implement stream handler

Same pattern as code/build pipes — stream the provider's output for TUI display.

### 5. Create subprocess entry point

Create `internal/pipes/fix/cmd/main.go`:

```go
func main() {
    logger := pipehost.NewPipeLogger("fix")
    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("fix", err.Error())
    }
    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("fix", err.Error())
    }
    compiled := fix.CompileTemplates(pc)
    pipehost.RunWithStreaming(provider,
        fix.NewHandlerWith(provider, pc, compiled, logger),
        func(sp bridge.StreamingProvider) pipe.StreamHandler {
            return fix.NewStreamHandlerWith(sp, pc, compiled, logger)
        },
    )
}
```

### 6. Write tests

Create `internal/pipes/fix/fix_test.go`:

- **TestFixTargetedTestFailures** — test failures in input, verify prompt renders each failure with file/test/error
- **TestFixTargetedLintErrors** — lint errors in input, verify prompt renders each error with file/line/rule
- **TestFixBothFailureTypes** — test failures + lint errors, both sections rendered
- **TestFixNoFailures** — no failures in input, returns "Nothing to fix" summary
- **TestFixBroadScope** — `scope: broad`, verify prompt uses `broad` template
- **TestFixAttemptTracking** — `attempt: 3`, verify prompt includes attempt number and "try different approach" instruction
- **TestFixProviderError** — provider fails, returns classified error envelope
- **TestFixMalformedInput** — input envelope content isn't parseable as verify output, returns fatal error
- **TestFixStreamHandler** — streaming works, chunks flow to sink

### 7. Add to justfile build

Update the `build` recipe in `justfile`:
```
go build -o internal/pipes/fix/run ./internal/pipes/fix/cmd/
```

## Testing Strategy

### Unit Tests
- `internal/pipes/fix/fix_test.go` — prompt construction, input parsing, scope selection, attempt tracking, error handling

### Edge Cases
- Verify output with zero test failures but lint errors (and vice versa)
- Very large number of failures (should the pipe truncate to stay within provider context?)
- Attempt number exceeds expected range (should still work, just includes the "try different" instruction)
- Input content is string instead of structured (came from a non-verify pipe — should error clearly)

## Risk Assessment

- **Convergence is hard to guarantee.** The fix pipe might introduce new errors while fixing old ones. The inner loop's max retry limit is the safety net, but prompts should strongly instruct against introducing new problems.
- **Type isolation creates maintenance burden.** `TestFailure` and `LintError` are defined in both verify and fix packages. If the schema changes in verify, fix must be updated too. This is the cost of pipe isolation — worth it for now, but consider a shared types package later if it becomes painful.
- **Attempt tracking relies on the pipeline passing the attempt number.** The pipeline runtime must increment the `attempt` flag on each loop iteration. If it doesn't, the fix pipe won't know to try a different approach.

## Validation Commands

```bash
go test ./internal/pipes/fix/... -v -count=1
go build ./internal/pipes/fix/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
