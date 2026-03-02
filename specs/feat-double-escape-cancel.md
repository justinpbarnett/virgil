# Feature: Double-Escape Cancellation

## Metadata

type: `feat`
task_id: `double-escape-cancel`
prompt: `add a cancel feature like claude code where pressing escape twice cancels the most recent prompt's process`

## Feature Description

When the user submits a prompt and the system is processing (waiting for the server or streaming a response), pressing Escape twice in quick succession cancels the in-flight request. This kills the server-side subprocess (e.g., an AI provider call), discards partial output, and returns the TUI to an idle input state. This mirrors Claude Code's double-escape cancellation UX.

## User Story

As a Virgil user
I want to press Escape twice to cancel a running prompt
So that I can abort slow or unwanted responses without restarting the application

## Relevant Files

- `internal/tui/tui.go` — bubbletea model, Update loop, key handling, streaming message types. This is where double-escape detection and cancel logic live.
- `internal/tui/client.go` — `openSSEStream` creates the HTTP request. Needs to accept a `context.Context` so requests can be cancelled.
- `internal/server/api.go` — `handleSSE` already passes `r.Context()` to `ExecuteStream`. Client disconnect triggers server-side context cancellation automatically.
- `internal/runtime/runtime.go` — `ExecuteStream` already accepts and propagates `context.Context` to stream handlers.
- `internal/pipe/subprocess.go` — `SubprocessStreamHandler` already uses `exec.CommandContext(ctx, ...)`. Context cancellation kills the subprocess.

### New Files

None. All changes are modifications to existing files.

## Implementation Plan

### Phase 1: Context-Based Request Cancellation (client.go)

Thread a `context.Context` through the HTTP request path so that cancelling the context aborts the SSE connection, which propagates through the server to kill the subprocess.

### Phase 2: Double-Escape Detection and Cancel Action (tui.go)

Add escape key tracking to the bubbletea model. On two Escape presses within a short time window while a request is active, cancel the context, discard the stream, and show a cancellation indicator.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add context parameter to `openSSEStream`

- In `internal/tui/client.go`, change `openSSEStream(serverAddr, text string)` to `openSSEStream(ctx context.Context, serverAddr, text string)`.
- Replace `http.NewRequest(...)` with `http.NewRequestWithContext(ctx, ...)`.
- No other changes to this function. The context flows into `streamClient.Do(req)`, so cancelling the context aborts the HTTP request and closes the response body.

### 2. Add cancel state to the model

- In `internal/tui/tui.go`, add these fields to the `model` struct:
  - `cancelFn context.CancelFunc` — cancels the in-flight HTTP request context. Nil when idle.
  - `lastEscTime time.Time` — timestamp of the most recent Escape keypress. Zero value when no recent escape.
- Add a constant `escapeWindow = 400 * time.Millisecond` for the double-escape detection threshold.

### 3. Create context on prompt submission

- In the `tea.KeyEnter` handler in `Update`, after incrementing `activeStreamID`, create a cancellable context:
  ```go
  ctx, cancel := context.WithCancel(context.Background())
  m.cancelFn = cancel
  ```
- Pass `ctx` through to `startStream`.

### 4. Thread context through `startStream`

- Change `startStream(addr, text string, streamID int)` to `startStream(ctx context.Context, addr, text string, streamID int)`.
- Inside the returned `tea.Cmd`, call `openSSEStream(ctx, addr, text)` instead of `openSSEStream(addr, text)`.

### 5. Handle Escape key in Update

- Add a `tea.KeyEsc` case in the `tea.KeyMsg` switch, before the default fallthrough to `textInput.Update`.
- Only act when there is an active request: `m.cancelFn != nil`.
- On first escape (when `m.lastEscTime` is zero or expired): record `m.lastEscTime = time.Now()` and return. Optionally show a subtle hint in the view (e.g., "press Esc again to cancel").
- On second escape within `escapeWindow`: trigger cancellation (next step).

### 6. Implement the cancel action

When double-escape is confirmed:

1. Call `m.cancelFn()` — cancels the HTTP request context, which:
   - Aborts the `streamClient.Do(req)` call if still connecting.
   - Closes the response body if streaming, causing the server's `r.Context()` to be cancelled.
   - The server's `ExecuteStream` propagates cancellation to `exec.CommandContext`, killing the subprocess.
2. Increment `m.activeStreamID` — so any in-flight `streamChunkMsg` or `streamDoneMsg` from the old stream is discarded in Update.
3. Reset UI state: `m.waiting = false`, `m.pending.Reset()`, `m.cancelFn = nil`, `m.lastEscTime = time.Time{}`.
4. Append a cancellation message: `m.appendMessage("virgil > [cancelled]")` and `m.appendMessage("")`.

### 7. Clear cancel state on normal completion

- In the `streamDoneMsg` handler (both success and error paths), after processing the message, set `m.cancelFn = nil` and `m.lastEscTime = time.Time{}`.
- This prevents stale cancel functions from lingering after a request completes normally.

### 8. Clear escape timer on non-escape keys

- At the top of the `tea.KeyMsg` handler, if the key is not Escape, reset `m.lastEscTime = time.Time{}`. This ensures that pressing Escape, then typing a character, then pressing Escape again does not trigger cancellation.

### 9. Update View for escape hint

- In the `View()` method, when `m.waiting` is true or `m.pending.Len() > 0`, and `!m.lastEscTime.IsZero()` (first escape was pressed), show a hint below the streaming output: `"press Esc again to cancel"`. This provides discoverability without cluttering the UI during normal use.

## Testing Strategy

### Unit Tests

- `internal/tui/tui_test.go`: Test the double-escape detection logic:
  - Two escapes within the window → cancel triggered (verify `cancelFn` called, `activeStreamID` incremented, state reset).
  - Two escapes outside the window → no cancel.
  - Escape during idle (no active request) → no effect.
  - Escape then non-escape key then escape → no cancel (timer reset).
  - Normal completion clears `cancelFn`.

### Edge Cases

- Cancel while waiting (before first chunk arrives) — context cancellation aborts the HTTP request; the `startStream` command returns `streamDoneMsg{err: context.Canceled}`, which is discarded due to `activeStreamID` mismatch.
- Cancel while streaming (mid-response) — context cancellation closes the connection; the pending `readNextEvent` command gets an error from `scanner.Scan()`, returns `streamDoneMsg{err: ...}`, which is discarded.
- Rapid triple-escape — first two trigger cancel, third is a no-op (no active request).
- Cancel after response completes but before `streamDoneMsg` is processed — `cancelFn` is already nil or the streamDoneMsg arrives and is processed normally.

## Risk Assessment

- **Low risk**: The cancellation chain (`context.Cancel` → HTTP abort → server context cancel → subprocess kill) already exists in the codebase. This feature wires TUI input into that existing chain.
- **Escape key conflicts**: The `tea.KeyEsc` event is distinct from escape sequences for arrow keys/etc. in bubbletea. The text input component may use Escape for blur/unfocus — we handle Escape before falling through to `textInput.Update`, but only act when a request is active (`cancelFn != nil`), so idle-state behavior is unchanged.
- **No server changes**: The server already handles client disconnection gracefully via `r.Context()`.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```sh
just build
just test
just lint
```

## Open Questions (Unresolved)

- **Escape window duration**: 400ms is proposed as the double-escape detection threshold. This balances responsiveness with avoiding accidental triggers. Claude Code uses a similar window. Recommendation: start with 400ms and adjust based on feel.
- **Hint text styling**: Should the "press Esc again to cancel" hint use dimmed/muted styling? Recommendation: yes, use lipgloss dim style if available, otherwise plain text is fine for v1.

## Sub-Tasks

Single task — no decomposition needed.
