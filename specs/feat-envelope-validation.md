# Feature: Envelope Validation at Pipeline Transitions

## Metadata

type: `feat`
task_id: `envelope-validation`
prompt: `Add a thin, automatic validation layer at every envelope transition in the pipeline — schema checks on the envelope contract and content-type assertions — to catch malformed outputs before they propagate downstream.`

## Feature Description

Today the pipeline passes envelopes between steps with zero validation. A pipe can return an envelope with `ContentType: "list"` but `Content` set to a plain string, or omit required fields like `Pipe` and `Action`, and nothing catches it. The malformed envelope propagates through the remaining pipeline steps, wasting context and compute, and eventually producing confusing output or silent data loss.

This feature adds a thin `Validate()` function to the `envelope` package and wires it into the runtime's step boundary — the same place logging and observer notifications already happen. Like logging, this is infrastructure: always on, zero configuration, no pipe-level opt-in required.

## User Story

As a pipe author
I want malformed envelopes to be caught immediately after my pipe returns
So that I get clear error messages pointing at my pipe instead of mysterious failures three steps downstream

## Relevant Files

- `internal/envelope/envelope.go` — Envelope struct, ContentType constants, constructor helpers. Validation rules live here because they codify the envelope contract.
- `internal/runtime/runtime.go` — `runStep()` is the insertion point. Both `Execute()` and `ExecuteStream()` funnel through it for non-terminal steps.
- `internal/runtime/logging.go` — Observer interface. Validation errors should flow through the same observability path.
- `internal/pipe/subprocess.go` — `SubprocessHandler` and `SubprocessStreamHandler` deserialize envelopes from subprocess stdout. A second natural validation point.
- `internal/pipehost/host.go` — pipe-side harness. Validation here would catch errors before the envelope even leaves the subprocess, but this is a secondary concern (parent-side validation is the priority).

### New Files

- `internal/envelope/validate.go` — `Validate(env Envelope) error` function and supporting helpers.
- `internal/envelope/validate_test.go` — Tests for every validation rule.
- `internal/runtime/validate_test.go` — Integration tests proving validation halts the pipeline on bad envelopes.

## Implementation Plan

### Phase 1: Foundation — Envelope Validation Rules

Define `Validate()` in the envelope package. The function checks structural invariants that every well-formed envelope must satisfy:

1. **Required fields** — `Pipe` must be non-empty. `Action` must be non-empty. `Timestamp` must be non-zero.
2. **ContentType consistency** — If `Content` is non-nil and `Error` is nil, `ContentType` must be one of the four known constants (`text`, `list`, `structured`, `binary`). If `ContentType` is `text`, `Content` must be a `string`. If `ContentType` is `list`, `Content` must be a slice (via reflect). If `ContentType` is `structured`, `Content` must be a map or struct (via reflect).
3. **Error field consistency** — If `Error` is non-nil, `Error.Severity` must be one of the three known constants (`fatal`, `error`, `warn`). `Error.Message` must be non-empty.
4. **No content + no error** — If both `Content` is nil and `Error` is nil, that's valid (some pipes legitimately produce side-effect-only results where the action carries the meaning).

`Validate()` returns `nil` on success or an error describing the first violation found. Errors are descriptive: `"envelope from pipe \"calendar\": ContentType is \"list\" but Content is string"`.

### Phase 2: Core Implementation — Runtime Integration

Wire validation into `Runtime.runStep()` immediately after the handler returns, before the observer notification. On validation failure:

1. Log the violation at error level (always, regardless of configured log level).
2. Set a fatal error on the envelope: `envelope.FatalError("validation: " + err.Error())`.
3. The existing `isFatal()` check in the `Execute`/`ExecuteStream` loop will halt the pipeline.

This means validation failures behave identically to any other fatal error — the pipeline stops, the error propagates to the caller, and the observer sees the failure.

For the streaming path's terminal step (which bypasses `runStep`), add the same validation check after the stream handler returns in `ExecuteStream`.

### Phase 3: Integration — Subprocess Boundary

Add an optional validation call in `SubprocessHandler` after deserializing the envelope from stdout JSON (`subprocess.go:165`). This catches malformed envelopes at the process boundary before they even enter the runtime's step chain. Use the same `Validate()` function. On failure, return a fatal error envelope attributed to the subprocess pipe name.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create `envelope.Validate()`

- Create `internal/envelope/validate.go`
- Define `func Validate(env Envelope) error`
- Implement required-field checks: `Pipe` non-empty, `Action` non-empty, `Timestamp` non-zero
- Implement ContentType validation: if `Content != nil && Error == nil`, assert `ContentType` is one of the four constants
- Implement content-type/content shape consistency using `reflect`:
  - `ContentText` → `Content` must be `string`
  - `ContentList` → `Content` must be a slice
  - `ContentStructured` → `Content` must be a map or struct
  - `ContentBinary` → `Content` must be `string` or `[]byte` (skip shape check — binary is opaque)
- Implement error field validation: if `Error != nil`, assert `Severity` is one of the three constants and `Message` is non-empty
- Return a descriptive error on the first violation, including the pipe name for context

### 2. Test `envelope.Validate()`

- Create `internal/envelope/validate_test.go`
- Test valid envelopes: text content, list content, structured content, error envelope, side-effect-only (nil content, nil error)
- Test invalid cases: empty `Pipe`, empty `Action`, zero `Timestamp`, unknown `ContentType`, `ContentType: "list"` with string content, `ContentType: "text"` with slice content, `ContentType: "structured"` with string content, `Error` with empty `Message`, `Error` with unknown `Severity`
- Test edge cases: nil `Content` with `ContentType` set (valid — content may have been intentionally cleared), error envelope with content (valid — warn-level errors can carry partial results)

### 3. Wire validation into `Runtime.runStep()`

- In `internal/runtime/runtime.go`, in `runStep()`, after `result := handler(current, flags)` and before `r.observer.OnTransition(...)`:
  - Call `envelope.Validate(result)`
  - If error, log at error level: `r.logger.Error("envelope validation failed", "pipe", step.Pipe, "error", err)`
  - Set `result.Error = envelope.FatalError("validation: " + err.Error())`
- This automatically halts the pipeline via the existing `isFatal()` check in `Execute()` and `ExecuteStream()`

### 4. Wire validation into `ExecuteStream` terminal step

- In `ExecuteStream()`, after the stream handler returns `result` (line ~158), add the same validation check before `formatTerminal`
- On failure, log and set fatal error, then return (skip formatting)

### 5. Wire validation into `SubprocessHandler`

- In `internal/pipe/subprocess.go`, in `SubprocessHandler`, after `json.Unmarshal(stdout.Bytes(), &out)` succeeds (line ~165), call `envelope.Validate(out)`
- On failure, return `envelope.NewFatalError(cfg.Name, "validation: " + err.Error())`
- Do the same in `SubprocessStreamHandler` after `result` is set from the final chunk

### 6. Add runtime integration tests

- In `internal/runtime/validate_test.go` (new file), test that a pipe returning an envelope with mismatched content-type halts the pipeline
- Test that a pipe returning a valid envelope proceeds normally
- Test that validation errors appear in observer notifications
- Test the streaming path with a bad terminal envelope

## Testing Strategy

### Unit Tests

- `internal/envelope/validate_test.go` — exhaustive coverage of every validation rule, both positive and negative cases. Table-driven tests.
- `internal/runtime/validate_test.go` — integration tests using the existing `testObserver` pattern from `runtime_test.go`. Register fake handlers that return bad envelopes, execute a plan, assert pipeline halts with the correct error.

### Edge Cases

- Envelope with `Error` set (warn severity) AND valid `Content` — should pass validation (partial results are valid)
- Envelope with `Error` set (fatal) and no `Content`/`ContentType` — should pass (error envelopes don't need content)
- Envelope with `ContentType` set but `Content` is nil — should pass (content may be intentionally empty)
- Envelope after JSON round-trip through subprocess protocol — lists deserialize as `[]any`, maps as `map[string]any`, so content-type checks must handle these generic types rather than assuming concrete Go types
- Binary content type — skip shape assertion (opaque by definition)

## Risk Assessment

- **False positives on JSON-deserialized content**: After JSON round-trip, Go slices become `[]any` and maps become `map[string]any`. The reflect-based checks must handle these generic types. The subprocess handler deserializes via `json.Unmarshal` into `any`, so `ContentList` content will always be `[]any`, not `[]SomeStruct`. This is the highest-risk area — test it explicitly.
- **Performance**: `reflect.ValueOf()` is called once per step per pipeline. At the scale of subprocess invocations (10ms+ each), this is negligible. No optimization needed.
- **Existing pipes breaking**: If any current pipe produces malformed envelopes that happen to work today, validation will start failing. This is the point — but review each pipe's output during testing to ensure they comply. The four existing pipes (calendar, chat, draft, memory) should be checked.
- **No rollback concern**: This is additive infrastructure. If validation proves too strict, individual rules can be relaxed without removing the framework.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```
just test
just lint
```

## Open Questions (Unresolved)

- **Warn vs. halt on validation failure?** The spec assumes fatal halt (fail fast). An alternative is to log a warning and continue, which is more forgiving but lets bad data propagate. **Recommendation**: Start with fatal halt. If it proves too strict in practice, add a `ValidationMode` config option (strict/warn/off) later. Strictness is easier to relax than to tighten.
- **Validate seed envelopes?** The seed envelope entering the pipeline is constructed by the server/router, not by a pipe. Should it be validated too? **Recommendation**: Yes, validate in `Execute()`/`ExecuteStream()` before the loop starts. The same `Validate()` call works. Add this as a follow-up if seed construction proves reliable.

## Sub-Tasks

Single task — no decomposition needed.
