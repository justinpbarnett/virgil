# Feature: Verify Pipe

## Metadata

type: `feat`
task_id: `verify-pipe`
prompt: `Add a mostly-deterministic verify pipe that runs the project's test suite, linters, and optionally checks plan conformance. Produces structured error output with file paths, line numbers, and failure details — formatted for the fix pipe to act on mechanically. Returns pass (null error) or fail (structured error with retryable=true).`

## Feature Description

The `verify` pipe is the quality gate in the `dev-feature` pipeline. After the build pipe writes code and tests, verify runs the test suite, checks for lint errors, and optionally validates that the implementation matches the declared plan.

The pipe is mostly deterministic — running tests and linters is pure command execution. The optional plan-conformance check uses AI to compare the implementation against the build plan, making it a hybrid pipe when that flag is enabled.

The critical design constraint is the error output format. The fix pipe downstream needs structured, machine-actionable failure data: file paths, line numbers, expected vs actual, rule names. Prose like "some tests failed" is useless. The verify pipe parses test and lint output into a structured error schema.

## User Story

As a pipeline step
I want to run tests and linters and produce structured pass/fail results
So that downstream pipes can act on failures mechanically

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/shell/shell.go` — shell executor pattern (reuse `Executor` interface or similar)
- `internal/pipes/shell/pipe.yaml` — shell pipe definition

### New Files

- `internal/pipes/verify/pipe.yaml` — pipe definition
- `internal/pipes/verify/verify.go` — handler implementation
- `internal/pipes/verify/parse.go` — test/lint output parsers
- `internal/pipes/verify/verify_test.go` — handler tests
- `internal/pipes/verify/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml`. Verify is a dev-category pipe with flags for suite selection, lint toggle, and plan-check toggle.

### Phase 2: Output Parsers

Build parsers that convert raw test/lint output into structured failure records. Start with Go (`go test -json`) and golangci-lint (`--out-format json`). The parser interface should be extensible for other languages later.

### Phase 3: Handler

Implement the handler: run test suite, run linters, optionally run plan check, aggregate results into structured output.

### Phase 4: Tests

Test parsing of various test/lint output formats, handler logic for pass/fail determination, and error structuring.

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/verify/pipe.yaml`:

```yaml
name: verify
description: Runs tests and validates implementation against the build plan.
category: dev
streaming: false
timeout: 120s

triggers:
  exact:
    - "run tests"
    - "verify build"
  keywords:
    - verify
    - test
    - validate
    - check
  patterns:
    - "verify {topic}"
    - "test {topic}"

flags:
  suite:
    description: Which test suite to run. Auto-detected from project config if omitted.
    default: auto
  lint:
    description: Whether to run linters.
    values: ["true", "false"]
    default: "true"
  plan-check:
    description: Whether to verify the implementation matches the declared plan.
    values: ["true", "false"]
    default: "false"
  cwd:
    description: Working directory (worktree path).
    default: ""

vocabulary:
  verbs:
    verify: verify
    validate: verify
    check: verify
  types:
    tests: tests
    lint: lint
    all: all
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb]
      plan:
        - pipe: verify
          flags: {}
```

### 2. Define output types

In `internal/pipes/verify/verify.go`, define:

```go
type VerifyOutput struct {
    Passed       bool            `json:"passed"`
    TestResult   *TestResult     `json:"test_result"`
    LintResult   *LintResult     `json:"lint_result"`
    Summary      string          `json:"summary"`
}

type TestResult struct {
    Passed   bool           `json:"passed"`
    Total    int            `json:"total"`
    Failed   int            `json:"failed"`
    Skipped  int            `json:"skipped"`
    Failures []TestFailure  `json:"failures"`
    Duration time.Duration  `json:"duration"`
}

type TestFailure struct {
    File     string `json:"file"`
    Test     string `json:"test"`
    Package  string `json:"package"`
    Error    string `json:"error"`
    Expected string `json:"expected,omitempty"`
    Actual   string `json:"actual,omitempty"`
}

type LintResult struct {
    Passed bool        `json:"passed"`
    Errors []LintError `json:"errors"`
}

type LintError struct {
    File    string `json:"file"`
    Line    int    `json:"line"`
    Column  int    `json:"column"`
    Rule    string `json:"rule"`
    Message string `json:"message"`
}
```

### 3. Implement test output parser

Create `internal/pipes/verify/parse.go`:

- `parseGoTestJSON(output string) (*TestResult, error)` — parse `go test -json` output. Each line is a JSON object with `Action`, `Package`, `Test`, `Output` fields. Track pass/fail per test, collect failure output.
- `parseLintJSON(output string) (*LintResult, error)` — parse golangci-lint JSON output. Extract file, line, column, rule, message from each issue.
- `detectTestCommand(cwd string) (string, error)` — auto-detect the test command. Check for: `justfile` (extract `test` recipe), `Makefile` (`test` target), `go.mod` (use `go test ./... -json`), `package.json` (`npm test`). Return the command string.
- `detectLintCommand(cwd string) (string, error)` — auto-detect the lint command. Check for: `justfile` (extract `lint` recipe), `.golangci.yml` (use `golangci-lint run --out-format json`).

### 4. Implement the handler

Create `internal/pipes/verify/verify.go`:

- Define an `Executor` interface (same as shell pipe):
  ```go
  type Executor interface {
      Execute(ctx context.Context, cmd string, cwd string) (stdout, stderr string, exitCode int, err error)
  }
  ```
- `NewHandler(executor Executor, provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler`
- The `provider` parameter is optional (nil if plan-check is disabled). Only used for the AI-based plan conformance check.
- Handler logic:
  1. Read `cwd` from flags. If empty, use the worktree path from the input envelope's structured content.
  2. Read `suite` flag. If `auto`, call `detectTestCommand(cwd)`.
  3. Run the test command. Parse output into `TestResult`.
  4. If `lint` flag is `true`: detect and run lint command. Parse output into `LintResult`.
  5. If `plan-check` flag is `true` and provider is non-nil: extract the build summary from the input envelope, prompt the provider to compare implementation vs plan. (This is the hybrid/AI part.)
  6. Aggregate into `VerifyOutput`. Set `Passed` to true only if all checks pass.
  7. Return envelope:
     - `content`: `VerifyOutput` struct
     - `content_type`: `structured`
     - `error`: null if passed. If failed:
       ```go
       &EnvelopeError{
           Message:   summary string (e.g., "3 test failures, 1 lint error"),
           Severity:  SeverityError,
           Retryable: true,
       }
       ```
     - The structured content in `VerifyOutput` contains the full failure details the fix pipe needs. The `error` field is the signal for the pipeline loop condition.

### 5. Create subprocess entry point

Create `internal/pipes/verify/cmd/main.go`:

```go
func main() {
    logger := pipehost.NewPipeLogger("verify")
    executor := &verify.OSExecutor{}

    // Provider is optional — only needed for plan-check
    var provider bridge.Provider
    if os.Getenv(pipehost.EnvProvider) != "" {
        p, err := pipehost.BuildProviderFromEnvWithLogger(logger)
        if err != nil {
            logger.Warn("provider unavailable, plan-check disabled", "error", err)
        } else {
            provider = p
        }
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("verify", err.Error())
    }

    pipehost.Run(verify.NewHandler(executor, provider, pc, logger), nil)
}
```

No streaming — verify runs commands and returns results.

### 6. Write tests

Create `internal/pipes/verify/verify_test.go`:

- **TestParseGoTestJSON_AllPass** — all tests pass, `TestResult.Passed` is true, no failures
- **TestParseGoTestJSON_WithFailures** — some tests fail, failures contain file, test name, error output
- **TestParseGoTestJSON_BuildError** — compilation failure (no test results), detected as failure
- **TestParseLintJSON_Clean** — no lint issues, `LintResult.Passed` is true
- **TestParseLintJSON_WithErrors** — lint errors with file, line, rule, message
- **TestDetectTestCommand_Justfile** — justfile present with `test` recipe, returns that command
- **TestDetectTestCommand_GoMod** — go.mod present, returns `go test ./... -json`
- **TestDetectTestCommand_None** — no recognizable project config, returns error
- **TestVerifyAllPass** — tests pass + lint clean, `Passed` is true, `error` is null
- **TestVerifyTestFail** — tests fail, `Passed` is false, error is retryable, content has failures
- **TestVerifyLintFail** — lint errors, `Passed` is false, content has lint errors
- **TestVerifyBothFail** — tests and lint both fail, summary includes both counts
- **TestVerifyLintDisabled** — `lint: false`, only tests run
- **TestVerifyExecutorError** — command execution itself fails (not a test failure — e.g., command not found), returns fatal error

### 7. Add to justfile build

Update the `build` recipe in `justfile`:
```
go build -o internal/pipes/verify/run ./internal/pipes/verify/cmd/
```

## Testing Strategy

### Unit Tests
- `internal/pipes/verify/verify_test.go` — parsers, handler logic, command detection, error structuring

### Edge Cases
- Test command produces no JSON output (binary failure, not test failure)
- Test command times out (context deadline exceeded → retryable error)
- Lint command not installed (should warn, not fatal — lint is optional)
- Empty test suite (no tests found — is this a pass or a warning?)
- Working directory doesn't exist (fatal error)

## Risk Assessment

- **Test output parsing is fragile.** `go test -json` output format is stable but verbose. The parser must handle interleaved package output, build errors that prevent tests from running, and subtests. Start with the common cases and iterate.
- **Auto-detection may guess wrong.** The `detectTestCommand` heuristic checks justfile → Makefile → go.mod → package.json in order. If a project has multiple, it picks the first. This is usually right but not always. The `suite` flag exists as an escape hatch.
- **Plan-check is the riskiest sub-feature.** Comparing implementation against a plan via AI is inherently fuzzy. Start with `plan-check: false` as the default and only enable it when the prompt quality is validated.

## Validation Commands

```bash
go test ./internal/pipes/verify/... -v -count=1
go build ./internal/pipes/verify/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
