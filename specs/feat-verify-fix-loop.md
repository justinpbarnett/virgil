# Feature: Verify-Fix Loop Execution

## Metadata

type: `feat`
task_id: `verify-fix-loop`
prompt: `Implement loop execution for the pipeline system. Loops repeat a sequence of steps until a condition is met or a max iteration cap is reached (verify -> fix -> verify -> fix -> ... until verify passes). This is the self-correction mechanism that makes agentic pipelines reliable.`

## Feature Description

The pipeline executor currently runs steps sequentially. A new **loop** primitive adds controlled repetition. This is the self-correction backbone of agentic pipelines.

A **loop** groups consecutive steps into a repeating unit. The steps execute in order, then a condition is evaluated against the most recent step outputs. If the condition is not yet satisfied, the loop repeats from its first step. An iteration cap prevents infinite loops. The primary use case is verify-fix: run tests, if they fail have an AI fix the errors, then re-run tests, repeating until tests pass or the attempt budget is exhausted.

Loops integrate with the pipeline executor described in `specs/pipeline-build.md` and build on the condition syntax and context map already specified there. This spec focuses on loop execution mechanics, Go types, and the verify-fix loop as the primary concrete walkthrough.

**Cycles are deferred.** The cycle primitive (conditional jump from a later step back to an earlier one, e.g. review-rework) is specified in `specs/pipeline-build.md` and will be implemented as a follow-on feature once loops are solid. Loops are the common case — every pipeline that builds code needs verify-fix. Cycles are used only in the outer review-rework path.

---

## Relevant Files

### Existing Files (Reference)

- `specs/pipeline-build.md` — pipeline spec defining YAML syntax for loops/cycles, `PipelineConfig` structs, and context map semantics
- `internal/runtime/runtime.go` — current sequential executor (`Runtime.Execute`, `Runtime.runStep`)
- `internal/envelope/envelope.go` — `Envelope`, `EnvelopeError`, severity constants, `ContentStructured`
- `internal/pipe/pipe.go` — `Handler`, `StreamHandler`, `Definition`
- `internal/pipes/verify/verify.go` — `VerifyOutput` struct, returns `Retryable: true` error on failure
- `internal/pipes/fix/fix.go` — `FixOutput` struct, reads verify output, `attempt` flag for convergence tracking
- `internal/runtime/logging.go` — `Observer` interface, `OnTransition`
- `internal/runtime/format.go` — `structToMap`, `normalizeToMap` helpers

### New Files

- `internal/runtime/pipeline.go` — `PipelineExecutor` with loop support
- `internal/runtime/condition.go` — condition parser and evaluator
- `internal/runtime/pipeline_test.go` — pipeline executor tests including loop cases
- `internal/runtime/condition_test.go` — condition evaluator tests

### Modified Files

None. Loops are new execution logic inside the new pipeline executor. The existing `Runtime` and its sequential `Execute` method are unchanged — they continue to handle simple multi-step plans. The pipeline executor is a separate type that composes `Runtime.runStep` internally.

---

## Pipeline YAML Syntax

Loops are declared as a top-level `loops` key in a `pipeline.yaml`, referencing step names from the `steps` list. The full syntax is defined in `specs/pipeline-build.md`; the relevant fragment for this spec:

```yaml
steps:
  - name: verify
    pipe: verify
    args:
      cwd: "{{worktree.path}}"
      lint: "true"

  - name: fix
    pipe: fix
    args:
      cwd: "{{worktree.path}}"
      attempt: "{{loop.iteration}}"
    condition: verify.error

loops:
  - name: verify-fix
    steps: [verify, fix]
    until: verify.error == null
    max: 5
```

### Loop semantics

- `steps` — ordered list of step names that form the loop body. These must be consecutive steps in the pipeline's `steps` list.
- `until` — condition expression evaluated after each full iteration. When true, the loop exits and the pipeline continues to the next step after the loop.
- `max` — maximum number of iterations. When reached without the `until` condition becoming true, the loop exits with a failure envelope.

---

## Go Structs

### Config types

`LoopConfig` and `CycleConfig` are defined in `specs/pipeline-build.md` and live in `internal/config/config.go`. This spec uses `LoopConfig` only:

```go
type LoopConfig struct {
    Name  string   `yaml:"name"`
    Steps []string `yaml:"steps"`  // step names forming the loop body
    Until string   `yaml:"until"`  // condition expression
    Max   int      `yaml:"max"`    // iteration cap
}
```

### Runtime state types

These live in `internal/runtime/pipeline.go`:

```go
// LoopState tracks the execution state of a loop during pipeline execution.
type LoopState struct {
    Config    config.LoopConfig
    Iteration int                    // 1-indexed current iteration
    History   []LoopIterationRecord  // results from each completed iteration
}

// LoopIterationRecord captures the outcome of one loop iteration.
type LoopIterationRecord struct {
    Iteration int
    Steps     []StepRecord
    Satisfied bool  // true if the until condition was met after this iteration
}

// StepRecord captures one step's execution within a loop.
type StepRecord struct {
    Step     string
    Duration time.Duration
    Error    *envelope.EnvelopeError
    Skipped  bool  // true if the step's condition evaluated to false
}
```

### Condition types

These live in `internal/runtime/condition.go`. The condition evaluator checks the envelope's error field or a content field — it does not implement a general expression language. Two patterns, no boolean combinators, no arithmetic. Complex logic belongs in pipe handlers, not pipeline YAML.

```go
// Condition is a parsed condition expression.
type Condition struct {
    Field    string // dot-path, e.g. "verify.error"
    Operator string // "truthy", "==", or "null"
    Value    string // literal value for equality, empty for truthy/null
}

// ParseCondition parses a condition string into a Condition.
// Supported forms:
//   - "field"                  -> truthy check (non-null, non-empty)
//   - "field == value"         -> equality check
//   - "field == null"          -> null check (field is absent or nil)
//   - "field == \"value\""     -> quoted equality check
func ParseCondition(expr string) (Condition, error)

// Evaluate evaluates a condition against the pipeline context map.
// Returns true if the condition is satisfied.
func (c Condition) Evaluate(ctx map[string]any) bool
```

---

## Condition Evaluator Design

The condition evaluator is deliberately minimal. The full condition syntax is defined in `specs/pipeline-build.md` (Condition Syntax section). This section covers the implementation.

### Parsing

`ParseCondition` handles three forms:

1. **Truthy** — `verify.error` — field is present, non-null, and (if string) non-empty.
2. **Equality** — `review.outcome == "fail"` — field equals the literal string value. Quotes around the value are optional but recommended for clarity.
3. **Null check** — `verify.error == null` — field is absent from the context map or its value is nil.

Parsing is string splitting, not a grammar. Split on ` == ` (with spaces). If no operator found, it's a truthy check. If the right side is `null`, it's a null check. Otherwise it's equality. Strip surrounding quotes from the value.

Invalid expressions (unsupported operators like `!=`, `>`, `&&`) are rejected at config validation time with a clear error message.

```go
func ParseCondition(expr string) (Condition, error) {
    expr = strings.TrimSpace(expr)
    if expr == "" {
        return Condition{}, fmt.Errorf("empty condition expression")
    }

    // Reject unsupported operators before splitting
    for _, op := range []string{" != ", " > ", " < ", " >= ", " <= ", " && ", " || "} {
        if strings.Contains(expr, op) {
            return Condition{}, fmt.Errorf("unsupported operator in condition: %q", expr)
        }
    }

    parts := strings.SplitN(expr, " == ", 2)
    field := strings.TrimSpace(parts[0])

    if len(parts) == 1 {
        return Condition{Field: field, Operator: "truthy"}, nil
    }

    value := strings.TrimSpace(parts[1])
    value = strings.Trim(value, "\"")

    if value == "null" {
        return Condition{Field: field, Operator: "null"}, nil
    }

    return Condition{Field: field, Operator: "==", Value: value}, nil
}
```

### Evaluation

The context map stores step outputs as `ctx["stepname.fieldname"]`. The evaluator looks up `c.Field` in the map:

```go
func (c Condition) Evaluate(ctx map[string]any) bool {
    val, exists := ctx[c.Field]

    switch c.Operator {
    case "truthy":
        return exists && val != nil && val != ""

    case "null":
        return !exists || val == nil

    case "==":
        if !exists || val == nil {
            return false
        }
        return fmt.Sprintf("%v", val) == c.Value

    default:
        return false
    }
}
```

### Context map field resolution

Context map semantics are defined in `specs/pipeline-build.md` (Context map section). The key points for loop execution:

- `ctx["stepname.error"]` = envelope's `Error` field (nil if no error)
- For structured content: each top-level field of `envelope.Content` is stored as `ctx["stepname.fieldname"]`
- `ctx["loop.iteration"]` = current 1-indexed iteration count (updated at the start of each loop iteration)

The `verify.error` condition resolves to `ctx["verify.error"]`, which is the `*EnvelopeError` from the verify step's output. When verify passes, `Error` is nil, so `verify.error == null` evaluates to true. When verify fails, `Error` is non-nil (with `Retryable: true`), so `verify.error` (truthy) evaluates to true — which is what the fix step's `condition` checks.

---

## Loop Execution

### Algorithm

The pipeline executor identifies loop boundaries during initialization by mapping step names to their indices in the steps list. When execution reaches the first step of a loop, the loop executor takes over:

```
func (pe *PipelineExecutor) executeLoop(loop LoopState, steps []StepConfig, ctx map[string]any, input envelope.Envelope) (envelope.Envelope, error)
```

1. Parse the `until` condition once (at pipeline load time, cached in `LoopState`).
2. Set `iteration = 1`.
3. **Iteration start**: set `ctx["loop.iteration"] = iteration`.
4. Execute each step in the loop body sequentially:
   a. Resolve template variables in step args against the context map.
   b. If the step has a `condition`, evaluate it. If false, skip the step (record as skipped).
   c. Execute the step via `Runtime.runStep`.
   d. Update the context map with step outputs.
   e. If the step returns a fatal error, abort the loop immediately. Propagate the fatal envelope.
5. After all steps in the iteration complete, evaluate the `until` condition.
6. If `until` is satisfied: exit the loop. The last step's envelope becomes the loop output. Continue pipeline execution.
7. If `until` is not satisfied and `iteration < max`: increment iteration, go to step 3.
8. If `until` is not satisfied and `iteration >= max`: **loop exhaustion**. Build a failure envelope.

### Step condition within a loop

The fix step has `condition: verify.error` — it only runs when verify has produced an error. On the final iteration when verify passes, fix is skipped. This is important: the loop still evaluates `until` after the iteration even if some steps were skipped.

Execution flow for a successful verify-fix loop:

```
iteration 1:
  verify  -> fails (ctx["verify.error"] = {message: "2 test failures", retryable: true})
  fix     -> condition "verify.error" is truthy -> runs, applies fixes
  until "verify.error == null" -> false (verify.error still set from this iteration)

iteration 2:
  verify  -> passes (ctx["verify.error"] = nil)
  fix     -> condition "verify.error" is truthy -> false -> SKIPPED
  until "verify.error == null" -> true -> EXIT LOOP
```

### Loop counter reset

The loop's iteration counter must support external reset (setting `Iteration` back to 0 and clearing `History`). This is needed by the future cycle primitive: when an outer cycle fires and jumps back to a step before the loop, the loop gets a fresh attempt budget. The `LoopState` struct exposes a `Reset()` method for this purpose, even though nothing calls it until cycles are implemented.

### Failure escalation on exhaustion

When a loop reaches `max` iterations without satisfying `until`:

```go
func loopExhaustedEnvelope(loop LoopState) envelope.Envelope {
    out := envelope.New("pipeline", "loop-exhausted")
    out.ContentType = envelope.ContentStructured
    out.Content = map[string]any{
        "loop":       loop.Config.Name,
        "iterations": loop.Iteration,
        "max":        loop.Config.Max,
        "history":    loop.History,
    }
    out.Error = envelope.FatalError(fmt.Sprintf(
        "loop %q exhausted after %d iterations without satisfying: %s",
        loop.Config.Name, loop.Config.Max, loop.Config.Until,
    ))
    return out
}
```

The envelope carries the full iteration history so callers can inspect what happened across all attempts. The error is fatal — the pipeline stops. No publish step runs after a failed verify-fix loop.

---

## Iteration Tracking

The synthetic variable `{{loop.iteration}}` is available within loop steps. It is a 1-indexed integer stored in the context map as `ctx["loop.iteration"]`.

The fix pipe already has an `attempt` flag that controls convergence behavior — on attempt 2+ the system prompt tells the model to try a different approach. The pipeline wires this with:

```yaml
args:
  attempt: "{{loop.iteration}}"
```

The template resolver converts the integer to a string when resolving args. The fix pipe's `parseAttempt` function parses it back.

Within a loop, pipes can read `loop.iteration` from the context to adjust behavior:
- Fix pipe: escalates from targeted to broad scope on later attempts.
- Verify pipe: could enable additional checks on later iterations (not currently implemented, but the mechanism is available).

---

## Integration with Pipeline Executor

The pipeline executor (`internal/runtime/pipeline.go`) walks the step list with awareness of loop boundaries. The overall structure:

```go
type PipelineExecutor struct {
    runtime    *Runtime
    config     config.PipelineConfig
    ctx        map[string]any         // context map
    loops      map[string]*LoopState  // loop name -> state
    loopRanges map[int]string         // step index -> loop name (for first step of each loop)
    stepIndex  map[string]int         // step name -> index in steps list
    observer   Observer               // for TUI notifications
    logger     *slog.Logger
}

func (pe *PipelineExecutor) Execute(seed envelope.Envelope) envelope.Envelope {
    current := seed
    i := 0

    for i < len(pe.config.Steps) {
        step := pe.config.Steps[i]

        // Check if this step starts a loop
        if loopName, isLoopStart := pe.loopRanges[i]; isLoopStart {
            loopState := pe.loops[loopName]
            result := pe.executeLoop(loopState, current)
            if isFatal(result) {
                return result
            }
            current = result
            // Advance past the loop's last step
            lastLoopStep := pe.stepIndex[loopState.Config.Steps[len(loopState.Config.Steps)-1]]
            i = lastLoopStep + 1
            continue
        }

        // Execute single step
        current = pe.executeStep(step, current)
        if isFatal(current) {
            return current
        }

        i++
    }

    return current
}
```

The executor uses index-based stepping rather than range iteration so that the future cycle primitive can jump backward without changing this loop structure. Loop ranges are precomputed at initialization from the loop config's step names.

### Step types within the executor

The pipeline executor handles four step types in a unified loop:

1. **Simple step** — `pipe` field set. Execute via `Runtime.runStep`.
2. **Parallel group** — `parallel` field set. Fan out branches, wait, merge.
3. **Graph step** — `graph` field set. Execute via graph executor (separate spec concern).
4. **Loop step** — detected by `loopRanges` map. Delegates to `executeLoop`.

Loops are not step types in the YAML — they are overlays on existing steps. The `loops` list references step names, and the executor detects boundaries by step index.

---

## Walkthrough: Verify-Fix Loop

Starting state: the `build-tasks` graph step has completed. All task code is written in the worktree. The pipeline reaches the `verify` step, which is the first step of the `verify-fix` loop.

### Iteration 1

1. `ctx["loop.iteration"] = 1`
2. **verify** runs: `go test ./... -json -count=1` in the worktree. Two tests fail.
   - Output: `VerifyOutput{Passed: false, TestResult: {Failed: 2, Failures: [...]}, Summary: "2 test failure(s)"}`
   - Envelope: `Error: {Message: "2 test failure(s)", Severity: "error", Retryable: true}`
   - Context update: `ctx["verify.error"] = &EnvelopeError{...}`, `ctx["verify.passed"] = false`
3. **fix** condition check: `verify.error` -> truthy (error is non-nil) -> step runs.
   - Input: the verify envelope with structured `VerifyOutput` content.
   - Flags: `attempt: "1"`, `scope: "targeted"`, `cwd: "/path/to/worktree"`
   - Fix pipe parses test failures from input, renders the `targeted` prompt template, calls the provider. Provider modifies files via tool use.
   - Output: `FixOutput{Summary: "Fixed 2 test failures", Attempt: 1}`
   - Context update: `ctx["fix.summary"] = "Fixed 2 test failures"`
4. **until** check: `verify.error == null` -> `ctx["verify.error"]` is non-nil -> false -> continue loop.

### Iteration 2

1. `ctx["loop.iteration"] = 2`
2. **verify** runs again. One test now passes, one still fails.
   - Output: `VerifyOutput{Passed: false, TestResult: {Failed: 1, Failures: [...]}, Summary: "1 test failure(s)"}`
   - Context update: `ctx["verify.error"] = &EnvelopeError{...}` (updated)
3. **fix** condition check: `verify.error` -> truthy -> step runs.
   - Flags: `attempt: "2"`. The system prompt now includes "This is attempt 2. Try a different approach."
   - Provider applies a different fix strategy.
4. **until** check: `verify.error == null` -> false -> continue.

### Iteration 3

1. `ctx["loop.iteration"] = 3`
2. **verify** runs. All tests pass. Lint is clean.
   - Output: `VerifyOutput{Passed: true, Summary: "all checks passed (47 tests passed, lint clean)"}`
   - Envelope: `Error: nil`
   - Context update: `ctx["verify.error"] = nil`, `ctx["verify.passed"] = true`
3. **fix** condition check: `verify.error` -> falsy (nil) -> step **SKIPPED**.
4. **until** check: `verify.error == null` -> `ctx["verify.error"]` is nil -> true -> **EXIT LOOP**.

Pipeline continues to `publish`.

---

## Observer Notifications and TUI Integration

The pipeline executor emits observer notifications at loop boundaries. These feed the TUI's stream and detail panel as defined in `docs/tui.md`.

### Observer events

The `executeLoop` method calls the observer at three points:

1. **Iteration start** — emitted when a new loop iteration begins. Payload: loop name, iteration number, max.
2. **Iteration end** — emitted when all steps in an iteration complete. Payload: loop name, iteration number, satisfied (bool), step records.
3. **Loop exit** — emitted when the loop exits (either satisfied or exhausted). Payload: loop name, total iterations, satisfied (bool).

These use the `SSEEventPipelineProgress` event type defined in `specs/pipeline-build.md`:

```json
{"type": "loop", "name": "verify-fix", "iteration": 2, "max": 5}
```

### Stream rendering

The TUI renders loop progress as pipeline notification lines using the `⟳` symbol from the TUI symbol vocabulary:

```
▸ build: verify failed, fixing (attempt 1)...
▸ build: ⟳ verify-fix iteration 2
▸ build: verify passed
```

On exhaustion:

```
▸ build: ✗ verify-fix exhausted after 5 attempts
```

### Detail panel rendering

The panel shows loop iterations with retry counts, as already specified in `docs/tui.md`:

```
◉ build-verify
  ⟳ iteration 2
    ▸ verify     fail → 1 test failure
    ▸ fix        12.1s
  ✓ iteration 3
    ✓ verify     pass
```

The `LoopIterationRecord` and `StepRecord` types carry all the data the panel needs: step name, duration, error, and skipped status.

---

## Test Cases

### `TestLoopSucceedsOnRetry`

**Setup**: Mock verify handler fails on call 1, passes on call 2. Mock fix handler always succeeds.
**Pipeline**: `steps: [verify, fix]`, `loops: [{steps: [verify, fix], until: verify.error == null, max: 5}]`
**Assert**:
- Verify called 2 times.
- Fix called 1 time (skipped on iteration 2 because verify passed).
- Final envelope has no error.
- `ctx["loop.iteration"]` ended at 2.
- Fix received `attempt: "1"` on its one invocation.

### `TestLoopExhausted`

**Setup**: Mock verify handler always fails (returns retryable error).
**Pipeline**: Same as above with `max: 3`.
**Assert**:
- Verify called 3 times.
- Fix called 3 times.
- Final envelope has fatal error containing "loop.*exhausted.*3 iterations".
- Envelope content includes history with 3 iteration records.
- Each fix invocation received incrementing `attempt` values: "1", "2", "3".

### `TestLoopStepConditionSkip`

**Setup**: Mock verify passes on first call.
**Assert**:
- Verify called 1 time.
- Fix called 0 times (condition `verify.error` is false).
- Loop exits after 1 iteration.

### `TestLoopFatalAbort`

**Setup**: Mock fix handler returns a fatal error (not retryable — e.g., provider unreachable).
**Assert**:
- Loop aborts immediately after the fatal fix step.
- Verify called 1 time, fix called 1 time.
- Final envelope has the fatal error from fix.
- No further iterations attempted.

### `TestLoopReset`

**Setup**: Create a `LoopState` with `Iteration: 3` and non-empty `History`.
**Assert**:
- After `Reset()`, `Iteration` is 0 and `History` is empty.
- This method exists for the future cycle primitive but is testable now.

### `TestConditionParseValid`

**Assert**: All valid condition forms parse correctly:
- `"verify.error"` -> Condition{Field: "verify.error", Operator: "truthy"}
- `"verify.error == null"` -> Condition{Field: "verify.error", Operator: "null"}
- `"review.outcome == \"fail\""` -> Condition{Field: "review.outcome", Operator: "==", Value: "fail"}
- `"review.outcome == fail"` -> Condition{Field: "review.outcome", Operator: "==", Value: "fail"}

### `TestConditionParseInvalid`

**Assert**: Invalid conditions produce errors at parse time:
- `""` -> error (empty)
- `"a != b"` -> error (unsupported operator)
- `"a > 5"` -> error (unsupported operator)
- `"a && b"` -> error (unsupported operator)

### `TestConditionEvaluate`

**Setup**: Context map with known values.
**Assert**:
- Truthy on present non-nil value -> true.
- Truthy on absent key -> false.
- Truthy on nil value -> false.
- Truthy on empty string -> false.
- Null check on nil value -> true.
- Null check on absent key -> true.
- Null check on present non-nil value -> false.
- Equality match -> true.
- Equality mismatch -> false.
- Equality on missing key -> false.

---

## File Locations

| File | Purpose |
|---|---|
| `internal/runtime/pipeline.go` | `PipelineExecutor` type, `executeLoop`, loop counter management, observer notifications |
| `internal/runtime/condition.go` | `Condition` type, `ParseCondition`, `Evaluate` |
| `internal/runtime/pipeline_test.go` | All loop and executor tests listed above |
| `internal/runtime/condition_test.go` | Condition parsing and evaluation unit tests |
| `internal/config/config.go` | `LoopConfig` struct (if not already added from pipeline-build spec) |

---

## Implementation Order

1. **Condition evaluator** — `internal/runtime/condition.go` + tests. No dependencies. Pure parsing and evaluation logic.
2. **Pipeline executor skeleton** — `internal/runtime/pipeline.go` with `PipelineExecutor` struct, context map management, sequential step execution (no loops yet). Verify it can run a simple multi-step pipeline.
3. **Loop execution** — add `executeLoop` to the pipeline executor. Wire loop detection from config. Add observer notifications. Add loop tests.
4. **Integration tests** — full pipeline tests with mock pipes exercising the verify-fix loop.

Steps 1 and 2 can proceed in parallel. Step 3 depends on both. Step 4 depends on 3.

---

## Validation Commands

```bash
go build ./...
go vet ./...
go test ./internal/runtime/... -v -count=1
go test ./internal/config/... -v -count=1
```

---

## Review Notes

Changes made during review, with rationale:

### Scope reduction: cycles deferred

The original spec covered loops, cycles, condition evaluation, and carry semantics — four concepts in one feature spec. Per the "proportional complexity" principle from `docs/virgil.md`, the verify-fix loop is the common case that every code-building pipeline needs. Cycles (review-rework) are used only in the outer path of the build pipeline and depend on loops being solid first.

**Changed**: Removed all cycle execution logic, cycle state types, cycle test cases, and the review-decompose walkthrough. Added a note that cycles are deferred to a follow-on spec. Kept `LoopState.Reset()` as a forward-compatible hook for when cycles need to reset loop counters.

### Condition evaluator simplified

The original `Condition` struct used a `Negated bool` field to represent `field == null` as a negated truthy check. This was clever but confusing — the `Operator` said "truthy" when the intent was "null check". The new struct uses `Operator: "null"` directly, which is what the code means and what YAML authors write. Three operators: `truthy`, `null`, `==`. No `Negated` field.

The evaluator was already minimal (no boolean combinators, no arithmetic). That is correct per the "deterministic first" principle — conditions are deterministic checks against the context map, not a mini expression language. Complex logic belongs in pipe handlers.

### Duplication with pipeline-build.md reduced

The original spec copied the full YAML syntax (including cycle definitions and steps unrelated to loops), the `CycleConfig` struct, and the condition syntax documentation from `specs/pipeline-build.md`. The revised spec references `pipeline-build.md` for shared definitions and includes only the loop-relevant fragments. The `LoopConfig` struct is still shown because it is the primary type this spec implements.

### TUI integration added

The original spec had no mention of observer notifications, the `⟳` symbol from `docs/tui.md`, or how loop iterations appear in the stream and detail panel. Added an "Observer Notifications and TUI Integration" section that:
- Defines the three observer events (`executeLoop` emits: iteration start, iteration end, loop exit)
- Shows the SSE event format (consistent with `pipeline-build.md`'s `SSEEventPipelineProgress`)
- Shows stream rendering with `⟳` for retries and `✗` for exhaustion
- Shows detail panel rendering with iteration history
- Confirms that `LoopIterationRecord` and `StepRecord` carry all data the TUI needs

### Observer field added to PipelineExecutor

The `PipelineExecutor` struct now includes an `observer Observer` field. The original had `cycles` and `cycleFroms` maps but no observer — meaning loop progress was invisible to the TUI. Observability is infrastructure per `docs/virgil.md`: the executor emits events automatically, pipes do not opt in.

### Test case cleanup

- Removed 4 cycle-specific tests (`TestCycleWithCarry`, `TestCycleExhausted`, `TestCycleResetsLoopCounter`, `TestNestedLoopInCycle`) — these belong in the future cycle spec.
- Added `TestLoopReset` to verify the `Reset()` method that cycles will eventually call.
- Fixed `TestConditionParseValid` expectations to match the new `Operator: "null"` (was `Operator: "truthy", Negated: true`).
- Fixed `TestConditionEvaluate` to test `null` operator directly instead of "negated truthy".
