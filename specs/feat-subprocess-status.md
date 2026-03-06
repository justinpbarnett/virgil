# Feature: Subprocess Status Side-Channel

## Metadata

type: `feat`
task_id: `subprocess-status`
prompt: `Add a status reporting side-channel for pipe subprocesses using stderr JSON lines. This enables real-time observability for long-running and parallel pipe executions — knowing what each agent is doing at any moment.`

## Feature Description

Long-running pipes (build, review, analyze) can run for minutes. Today the only feedback during execution is text chunk streaming on stdout. There is no way for the executor to know what a pipe is *doing* — which tool it called, what file it is reading, whether it is waiting on an API, or how far through its work it has progressed.

This feature adds a **status event side-channel over stderr**. The executor reads stderr concurrently while the subprocess runs, parses JSON-line status events, and forwards them to a `StatusSink` callback. This is the same pattern as chunk streaming on stdout but carries structured metadata instead of content.

The side-channel coexists with the existing stderr usage: structured slog messages (JSON with `msg` field) and plain debug text. Status events use a distinct `"status"` key to disambiguate.

### Design Principles

**Infrastructure, not a feature.** The executor automatically reads and discriminates stderr lines. Pipes that emit status events get them forwarded to the TUI in real time with zero executor-side configuration. Pipes that want richer reporting can write explicit status events via `StatusReporter`, but no pipe is required to. A pipe that writes nothing to stderr works exactly as it does today.

**Leverage what exists.** Tool call activity already flows through stdout via `ToolChunkPrefix` chunks and is already parsed by the runtime into SSE `tool` events. This spec does not duplicate that mechanism. Stderr status events cover what stdout chunks cannot: progress milestones, external waits, and non-fatal errors.

**Proportional presence.** A simple pipe that runs for 200ms produces no status events and nobody notices. A build pipe that runs for three minutes produces a stream of activity updates. The system scales its reporting to match the operation's complexity.

For future parallel/graph execution, each status event carries an optional `task_id` so events from concurrent pipes can be attributed to the correct task.

## User Story

As a user watching a long-running build or pipeline
I want to see real-time status updates (progress milestones, blocking waits, non-fatal errors)
So that I know what the system is doing and can estimate when it will finish

## Protocol Specification

### Stderr Line Discrimination

Stderr already carries two kinds of lines. This feature adds a third:

| Line shape | Treatment |
|---|---|
| JSON with `"status"` key | **Status event** (new) — parsed and forwarded to StatusSink |
| JSON with `"msg"` key (no `"status"`) | Structured slog log — forwarded to parent logger (existing) |
| Everything else | Plain debug output — collected as stderr text (existing) |

Discrimination is a single check added to the existing `forwardLogs` path: if the JSON object has a `"status"` key, treat it as a status event; otherwise fall through to the existing slog/plain-text handling. This is a small delta to `forwardLogs`, not a rewrite.

### Status Event JSON Format

Each status event is a single JSON line on stderr:

```json
{"status":"progress","pipe":"build","message":"analyzing file parser.go","ts":1709740800}
```

Fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | string | yes | Event type — one of the defined types below |
| `pipe` | string | yes | Name of the pipe emitting the event |
| `task_id` | string | no | Graph task identifier — empty for non-graph execution |
| `message` | string | yes | Human-readable description |
| `ts` | int64 | yes | Unix timestamp (seconds) |
| `detail` | object | no | Structured metadata specific to the event type |

### Status Event Types

| Type | When emitted | TUI symbol | Typical `detail` |
|---|---|---|---|
| `progress` | Meaningful progress milestone | `▸` | `{"step": "writing tests"}` |
| `waiting` | Blocked on external resource (API, network) | `⟳` | `{"resource": "anthropic-api"}` |
| `error` | Non-fatal error during execution | `✗` | `{"error": "rate limited, retrying"}` |

Three types, not seven. The executor already knows when a pipe starts (`◉`) and finishes (`✓`/`✗`) — those are subprocess lifecycle events, not something pipes need to report. Tool call activity already flows through stdout via `ToolChunkPrefix` and is parsed by the runtime into SSE `tool` events. And `thinking` is internal model state with no user-visible representation.

Pipes that never emit any status events work exactly as before. The three types above cover what the TUI panel needs beyond lifecycle and tool calls: activity text (`progress`), blocking waits (`waiting`), and non-fatal warnings (`error`).

## Go Structs

### Status Event (shared — used by both pipe-side and executor-side)

```go
// Package: internal/pipe

// StatusEvent is a structured status report emitted by a pipe subprocess
// on stderr. The executor reads these concurrently to provide real-time
// observability.
type StatusEvent struct {
    Status  string         `json:"status"`            // event type: progress, waiting, error
    Pipe    string         `json:"pipe"`              // pipe name
    TaskID  string         `json:"task_id,omitempty"` // graph task ID (empty for sequential)
    Message string         `json:"message"`           // human-readable description
    TS      int64          `json:"ts"`                // unix timestamp (seconds)
    Detail  map[string]any `json:"detail,omitempty"`  // type-specific structured data
}

// Status event type constants.
const (
    StatusProgress = "progress"
    StatusWaiting  = "waiting"
    StatusError    = "error"
)

// StatusSink is a callback that receives parsed status events from a
// subprocess. Analogous to the chunk sink for streaming text.
type StatusSink func(event StatusEvent)
```

### StatusReporter (pipe-side API — optional)

```go
// Package: internal/pipehost

// StatusReporter provides a simple API for pipes to emit status events.
// It writes JSON lines to the given writer (typically os.Stderr).
//
// Most pipes do not need this. It exists for long-running pipes that
// want to report progress milestones or blocking waits beyond what
// slog messages and ToolChunkPrefix already provide.
type StatusReporter struct {
    pipe   string
    taskID string
    w      io.Writer
    mu     sync.Mutex
}

// NewStatusReporter creates a reporter for the named pipe. The writer
// should be os.Stderr in production. taskID can be empty.
func NewStatusReporter(w io.Writer, pipe string, taskID string) *StatusReporter

// Progress reports a meaningful milestone.
func (r *StatusReporter) Progress(message string)

// ProgressDetail reports a milestone with structured metadata.
func (r *StatusReporter) ProgressDetail(message string, detail map[string]any)

// Waiting reports a blocking external dependency.
func (r *StatusReporter) Waiting(resource string)

// Error reports a non-fatal error.
func (r *StatusReporter) Error(message string)
```

Four methods for three event types (Progress has a Detail variant). Each method constructs a `StatusEvent`, marshals it to JSON, and writes it as a single line to the writer. The mutex prevents interleaving from concurrent goroutines.

## Pipe-Side API (pipehost)

### StatusReporter Construction

`pipehost` provides a convenience constructor. Pipes that want explicit status reporting call it during initialization:

```go
// NewStatusReporter creates a StatusReporter that writes to os.Stderr.
// The pipe name is taken from the argument. taskID is read from the
// VIRGIL_TASK_ID environment variable (empty if not set).
func NewStatusReporter(pipeName string) *StatusReporter {
    taskID := os.Getenv(EnvTaskID)
    return NewStatusReporterWriter(os.Stderr, pipeName, taskID)
}
```

New environment variable:

```go
const EnvTaskID = "VIRGIL_TASK_ID"
```

This is set by the executor when running graph tasks. For sequential execution it is empty.

### Relationship to Existing Mechanisms

Tool call activity already flows through stdout via the `ToolChunkPrefix` convention in `bridge.RunAgenticLoop`. The runtime already parses these into SSE `tool` events (see `runtime.go` lines 263-268). This spec does not duplicate that path.

The `StatusReporter` is for information that *cannot* flow through the stdout chunk stream: progress milestones between tool calls, blocking waits on external APIs, and non-fatal error warnings. Most pipes will never use it. The build pipe is the first candidate because its agentic loop runs for minutes — it can call `reporter.Progress("writing tests")` at meaningful milestones without changing its function signatures or its `onChunk` callback.

## Executor-Side Integration

### Core Change: Concurrent Stderr Reading

Both `SubprocessHandler` and `SubprocessStreamHandler` currently capture stderr into a `limitedBuffer` and process it after exit via `forwardLogs`. For status events to arrive in real time, stderr must be read **concurrently** while the subprocess runs.

The change is the same for both handlers:

1. Replace `cmd.Stderr = &limitedBuffer{...}` with `cmd.StderrPipe()`
2. Spawn a goroutine running `readStderr` that scans lines and discriminates
3. Join the goroutine after `cmd.Wait()` via a `stderrDone` channel

The `readStderr` function is a refactored `forwardLogs` that processes lines as they arrive instead of after exit. For each line:

1. If JSON with `"status"` key: unmarshal as `StatusEvent`, call `cfg.StatusSink(event)` if sink is non-nil
2. If JSON with `"msg"` key: forward to logger (existing `forwardLogs` behavior)
3. Otherwise: collect as plain stderr text

The existing `forwardLogs` function is replaced by `readStderr` — same logic, now streaming instead of batch.

#### SubprocessConfig Addition

```go
type SubprocessConfig struct {
    Name       string
    Executable string
    WorkDir    string
    Timeout    time.Duration
    Env        []string
    Logger     *slog.Logger
    StatusSink StatusSink  // optional — receives status events from stderr
}
```

When `StatusSink` is nil, status events parsed from stderr are silently discarded. Existing code that constructs `SubprocessConfig` without a sink works exactly as before.

### PersistentProcess

Deferred. `PersistentProcess` does not currently capture stderr at all. Adding a long-lived stderr reader is a larger change. For the initial implementation, status events are supported only for spawned subprocesses.

### Runtime Integration

The `Runtime` does not directly handle status events. The `StatusSink` is injected into `SubprocessConfig` when the registry is built. The server layer provides the concrete sink:

- **Server (SSE)**: Converts `StatusEvent` to an SSE event with type `"status"` and JSON data
- **TUI**: Consumes SSE status events and updates the panel's activity text for the active step

New SSE event constant:

```go
// Package: internal/envelope
const SSEEventStatus = "status"
```

### Aggregation for Parallel Execution (future)

When the graph executor runs concurrent tasks:

1. Set `VIRGIL_TASK_ID` in each subprocess's environment
2. Provide a shared `StatusSink` that receives events from all subprocesses
3. Events arrive tagged with `task_id` so the consumer can attribute them

The shared sink must be safe for concurrent calls. Since `StatusSink` is a function type, the caller wraps it:

```go
func aggregatedSink(underlying StatusSink) StatusSink {
    var mu sync.Mutex
    return func(event StatusEvent) {
        mu.Lock()
        defer mu.Unlock()
        underlying(event)
    }
}
```

## Relevant Files

### Existing Files (Modified)

- `internal/pipe/subprocess.go` — add `StatusSink` to `SubprocessConfig`; refactor both handlers to use `cmd.StderrPipe()` + goroutine instead of `limitedBuffer`; replace `forwardLogs` with streaming `readStderr`
- `internal/envelope/envelope.go` — add `SSEEventStatus` constant
- `internal/pipehost/host.go` — add `EnvTaskID` constant

### New Files

- `internal/pipe/status.go` — `StatusEvent` struct, constants, `StatusSink` type, `readStderr` function
- `internal/pipe/status_test.go` — tests for `readStderr` parsing and event discrimination
- `internal/pipehost/status.go` — `StatusReporter` implementation
- `internal/pipehost/status_test.go` — tests for `StatusReporter` JSON output

### Files NOT Modified

- `internal/pipe/pipe.go` — no changes; `StatusEvent` lives in the new `status.go` file
- `internal/pipes/build/build.go` — no signature changes; the build pipe can optionally create a `StatusReporter` internally without any API change (Phase 3)
- `internal/pipes/build/cmd/main.go` — no changes needed; status reporting is internal to the pipe, not injected from outside
- `internal/runtime/runtime.go` — no changes; status events flow through the pipe layer via SSE, not the runtime
- `internal/pipe/persistent.go` — deferred; persistent process status support is a follow-up

## Implementation Plan

### Phase 1: Status Event Types and Reporter

Define the shared types. No executor changes yet — pipes can start using the reporter immediately, and the events will be silently discarded until Phase 2 wires the executor.

1. Create `internal/pipe/status.go` with `StatusEvent`, constants, `StatusSink`
2. Create `internal/pipehost/status.go` with `StatusReporter`
3. Write tests for both

### Phase 2: Executor-Side Stderr Streaming

Refactor subprocess handlers to read stderr concurrently and discriminate status events.

1. Add `StatusSink` field to `SubprocessConfig`
2. Write `readStderr` in `internal/pipe/status.go` — streaming replacement for `forwardLogs` that handles all three line types
3. Refactor `SubprocessHandler` to use `cmd.StderrPipe()` + goroutine + `readStderr`
4. Refactor `SubprocessStreamHandler` similarly
5. Remove `forwardLogs` (subsumed by `readStderr`)
6. Write tests for stderr discrimination (status event, slog message, plain text, mixed, malformed JSON)

### Phase 3: Build Pipe Integration (optional)

The build pipe can optionally create a `StatusReporter` internally to emit progress milestones. No function signature changes — the reporter is constructed inside `runBuild` and writes to stderr like any other pipe output.

1. Create a `StatusReporter` at the top of `runBuild` (only when the build is agentic / long-running)
2. Call `reporter.Progress(...)` at meaningful milestones (after tool groups complete, before/after verify)
3. No changes to `NewHandler`, `NewStreamHandler`, or `cmd/main.go` signatures

### Phase 4: SSE Forwarding

Wire the status sink from the server layer through to SSE.

1. Add `SSEEventStatus` constant to `internal/envelope/envelope.go`
2. When building `SubprocessConfig` in the server's pipe registration, set `StatusSink` to a function that converts events to SSE
3. TUI: consume `SSEEventStatus` events and update the panel's activity text for the active step

## Test Cases

### Unit Tests — StatusReporter (`internal/pipehost/status_test.go`)

- **TestStatusReporterProgress** — `Progress("writing tests")` writes a JSON line with `status: "progress"`, correct pipe name, non-zero timestamp
- **TestStatusReporterProgressDetail** — `ProgressDetail("step 2", detail)` writes JSON with the detail map included
- **TestStatusReporterWaiting** — `Waiting("anthropic-api")` writes JSON with `detail.resource`
- **TestStatusReporterError** — `Error("rate limited")` writes JSON with `status: "error"`
- **TestStatusReporterConcurrent** — 100 goroutines call methods simultaneously; all lines are valid JSON (no interleaving)
- **TestStatusReporterTaskID** — reporter constructed with task ID includes it in every event

### Unit Tests — readStderr (`internal/pipe/status_test.go`)

- **TestReadStderrStatusEvent** — line with `"status"` key is parsed as `StatusEvent` and forwarded to sink
- **TestReadStderrSlogMessage** — line with `"msg"` key (no `"status"`) is forwarded to logger, not to status sink
- **TestReadStderrPlainText** — non-JSON line is collected as plain stderr
- **TestReadStderrMixed** — input with all three line types is correctly discriminated
- **TestReadStderrMalformedJSON** — `{` prefix but invalid JSON is treated as plain text
- **TestReadStderrNilSink** — status events are silently discarded when sink is nil
- **TestReadStderrEmpty** — empty stderr produces no events and empty plain text

### Integration Tests — Subprocess with Status (`internal/pipe/subprocess_test.go`)

- **TestSubprocessHandlerStatusEvents** — spawn a test binary that writes status events to stderr and an envelope to stdout; verify status sink receives events and handler returns correct envelope
- **TestSubprocessStreamHandlerStatusEvents** — same but with streaming; verify status events arrive *during* execution (not after)
- **TestSubprocessHandlerNoStatus** — existing pipe without status events works exactly as before (backward compatibility)
- **TestSubprocessHandlerStatusAndLogs** — pipe emits both status events and slog messages; verify both are handled correctly

## Backward Compatibility

- **Pipes that do not emit status events work exactly as before.** The `readStderr` function treats lines without `"status"` identically to the existing `forwardLogs` behavior. This is a refactor of `forwardLogs` into a streaming version, not new behavior.
- **SubprocessConfig.StatusSink defaults to nil.** When nil, status events parsed from stderr are silently discarded. Existing code that constructs `SubprocessConfig` without a sink is unchanged.
- **No changes to the stdout protocol** (envelope/chunk JSON lines). Tool calls continue to flow through `ToolChunkPrefix` on stdout.
- **No changes to the stdin protocol** (`SubprocessRequest`).
- **No changes to pipe function signatures.** The `StatusReporter` is created internally by pipes that want it, not injected from outside.
- **`VIRGIL_TASK_ID`** is only set for graph execution. Existing subprocesses see an empty env var, which produces `task_id: ""` (omitted from JSON via `omitempty`).

## Risk Assessment

- **Low risk — additive change.** The stdout protocol is untouched. Stderr parsing is extended with one additional check (the `"status"` key) before the existing `"msg"` check.
- **Low risk — backward compatible.** Pipes that don't use `StatusReporter` produce no status events. The executor sees the same stderr content as before. No pipe signatures change.
- **Medium risk — concurrent stderr reading.** Moving from `limitedBuffer` (post-exit) to `StderrPipe` (concurrent) changes the subprocess lifecycle. The stderr goroutine must complete before accessing results. The `stderrDone` channel pattern handles this, but edge cases around process crashes and partial writes need careful testing.
- **Low risk — PersistentProcess deferred.** Not touching the persistent process path avoids the complexity of long-lived stderr readers.

## Validation Commands

```bash
go test ./internal/pipe/... -v -count=1
go test ./internal/pipehost/... -v -count=1
go build ./...
```

---

## Review Notes

Changes made during review and the reasoning behind each:

### Event types reduced from 7 to 3

Removed `started`, `done`, `tool_call`, and `thinking`.

- **`started`/`done`**: The executor already knows when a subprocess starts and finishes — these are lifecycle events owned by the infrastructure, not something pipes should report. Having pipes call `reporter.Started()` and `reporter.Done()` is opt-in ceremony that contradicts "no pipe opts in" from virgil.md's observability section.
- **`tool_call`**: Tool call activity already flows through stdout via `ToolChunkPrefix` in `bridge.RunAgenticLoop` and is already parsed by the runtime into SSE `tool` events (runtime.go lines 263-268). The spec was creating a parallel stderr channel for information that already has a working stdout channel.
- **`thinking`**: Internal model state with no user-visible representation. Does not map to any TUI symbol from tui.md. The TUI panel needs: step name, status symbol, duration, activity text. "The model is thinking" is none of those.

The remaining three (`progress`, `waiting`, `error`) map cleanly to TUI symbols (`▸`, `⟳`, `✗`) and cover what stdout chunks cannot: milestone descriptions, blocking waits, and non-fatal warnings.

### StatusReporter API reduced from 10 methods to 4

`Progress`, `ProgressDetail`, `Waiting`, `Error`. Four methods for three event types (Progress has a detail variant). The original had a method per event type plus variants, which was wide API surface for a type most pipes will never use.

### Build pipe integration made non-invasive

The original spec changed `runBuild`'s function signature to accept a `*StatusReporter` parameter, which cascaded into `NewHandler`, `NewStreamHandler`, `NewHandlerWith`, `NewStreamHandlerWith`, and `cmd/main.go`. This is invasive and couples the pipe's public API to an optional observability concern.

Changed to: the build pipe creates a `StatusReporter` internally if it wants one. No signature changes. No changes to `cmd/main.go`. This respects "observability is infrastructure, not a feature" — the pipe doesn't need to be told to report; it can choose to, but the interface stays clean.

### Executor-side change described as forwardLogs refactor

The original described `readStderr` as a new function alongside `forwardLogs`. Clarified that `readStderr` replaces `forwardLogs` — same discrimination logic, now streaming instead of batch, with one additional check for the `"status"` key. This makes the change smaller and more obviously backward-compatible.

### Design principles section added

The original spec lacked explicit connection to virgil.md's philosophy. Added a "Design Principles" subsection calling out infrastructure-not-feature, leveraging existing mechanisms, and proportional presence. These constrain future scope creep.

### TUI symbol mapping added to event type table

Added a "TUI symbol" column to the event type table so the mapping between status events and tui.md's symbol vocabulary is explicit and verifiable.

### Removed build pipe integration tests

The original had `TestBuildEmitsStartedAndDone` and `TestBuildEmitsToolCall` which tested removed event types and the removed pattern of injecting a reporter into `runBuild`. Since the build pipe integration is now optional and internal, these tests are not part of this spec's scope.

### Removed Phase 5 (Graph Executor Preparation)

Moved to future work since the graph executor does not exist yet. The aggregation pattern is documented in the Executor-Side Integration section. There is nothing to implement in this spec — `VIRGIL_TASK_ID` and `aggregatedSink` are described for future reference, not as current deliverables.
