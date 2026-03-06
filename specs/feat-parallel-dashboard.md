# Feature: Parallel Pipeline Panel

## Metadata

type: `feat`
task_id: `parallel-dashboard`
prompt: `Extend the TUI panel to display parallel task activity during pipeline execution. Show parallel branches with tree structure, per-task status symbols, and expandable task output using the existing panel infrastructure.`

## Feature Description

When Virgil executes a pipeline with parallel steps (e.g., the `build` pipeline's parallel study branches or its task graph), the TUI panel should show the parallel branch structure, not just sequential steps. This feature extends the existing panel pipeline view to render parallel tasks with tree notation, status symbols, and the ability to expand a task's streaming output within the panel.

The stream is never replaced. The input line is never hidden. The panel is the right place for pipeline internals -- that is what tui.md specifies. This feature makes the panel aware of parallelism.

## User Story

As a Virgil user running a multi-task pipeline
I want to see what every parallel branch is doing in the panel
So that I can understand progress and identify failures without leaving the conversation.

## Design Principles

1. **Proportional presence** -- parallel tasks appear in the panel only when they exist. Sequential plans render exactly as they do today. A two-branch parallel step shows two lines, not a dashboard.
2. **Panel is the viewport** -- tui.md defines the panel as the place for pipeline detail. This feature extends the panel, not replaces the stream.
3. **No new chrome** -- no summary bars, no key hint lines, no column headers. The tree-structured step list with status symbols is sufficient.
4. **Server-driven** -- all execution happens on the server. The TUI renders events from the existing streaming protocol. No execution logic in the client.

---

## Panel Layout

### Parallel steps in the pipeline tree

The panel already renders sequential steps as a flat list with status symbols. Parallel branches extend this with tree notation, matching the pattern already documented in tui.md:

```
dev-feature
OAuth login → Keep

✓ spec             1.2s
✓ prepare          3.4s
  ├ worktree.create  0.3s
  ├ codebase.study   3.1s  (builder)
  └ codebase.study   3.4s  (reviewer)
◉ build-verify
  ◉ build
    ◉ pricing-table    edit pricing.go
    ◉ api-endpoint     read router.go
    ✓ test-fixtures    0.3s
    ○ migration        → #1
    ○ docs             → #1, #2
  ○ verify
○ publish
○ review
```

This is the existing panel format with parallel branches rendered as indented children. The tree characters (`├`, `└`) and status symbols (`✓`, `✗`, `◉`, `○`) are already defined in the TUI symbol vocabulary. Activity descriptions (e.g., "edit pricing.go") appear after the task name in `theme.Dim`, matching how tool calls already render in the panel.

### Expanded task output

When the user scrolls to a parallel task in the panel and presses `Enter`, the panel expands to show that task's streaming output below the tree. This uses the panel's existing viewport and scroll behavior (Ctrl+J/K). Pressing `Enter` again or `Esc` collapses back to the tree view.

```
dev-feature: build

  ◉ pricing-table    edit pricing.go      ← selected
  ◉ api-endpoint     read router.go
  ✓ test-fixtures    0.3s
  ○ migration        → #1
  ○ docs             → #1, #2

── pricing-table ──────────────────────
Reading the existing pricing table to
understand the current structure...

The pricing module uses a config-driven
approach. I'll add the new tier here:

  func PricingTiers() []Tier {
```

The panel title updates to show which task is expanded. The task list compresses to single lines (no sub-tree) to maximize output space. The existing `Ctrl+J/K` panel scroll bindings navigate the output viewport.

### Stream behavior during parallel execution

The stream continues to show interleaved pipeline notifications, exactly as tui.md specifies:

```
❯ add OAuth login to Keep

  ▸ Starting dev-feature pipeline.

❯ what's on my calendar today

  You have three meetings. ...

  ▸ dev-feature: building (5 tasks, 2 running)
  ▸ dev-feature: test-fixtures done
  ▸ dev-feature: build complete, verifying...

  dev-feature complete. PR #47 ready for review.
```

Pipeline notifications remain single dim lines with `▸` prefix. The panel provides depth. The stream provides awareness. The user keeps talking.

---

## Model Changes

### Parallel task state on the existing model

No new bubbletea sub-model. The existing `model` struct gains fields to track parallel task state, extending the `pipelineSteps`/`pipelineTools` pattern already in use:

```go
// Parallel task tracking for panel display
type parallelTask struct {
    ID        string
    Name      string
    Pipe      string
    Status    string // "waiting", "running", "done", "failed"
    Activity  string // last tool/activity description
    Duration  string // set on completion, e.g. "0.3s"
    Error     string // non-empty on failure
    Output    strings.Builder // captured streaming output
    DependsOn []string
}
```

Added to the existing `model`:

```go
type model struct {
    // ... existing fields ...

    // Parallel task tracking (nil when no parallel execution)
    parallelTasks []*parallelTask
    panelSelected int  // cursor within parallel task list (-1 = none)
    panelExpanded int  // index of expanded task (-1 = none)
}
```

When `parallelTasks` is non-nil, `formatPipelineSteps()` renders the tree with parallel branches instead of the flat list. When `parallelTasks` is nil, rendering is unchanged.

### Panel interaction

The panel already supports `Ctrl+J/K` for scrolling. Two new behaviors when parallel tasks are active:

| Key | Context | Action |
|-----|---------|--------|
| `Ctrl+J` | Panel open, parallel active | Move selection down through task list |
| `Ctrl+K` | Panel open, parallel active | Move selection up through task list |
| `Enter` | Panel open, task selected | Expand/collapse task output |
| `Esc` | Panel open, task expanded | Collapse back to tree view |

These reuse existing keybindings in the existing panel context. No new bindings are introduced. When no parallel tasks are active, `Ctrl+J/K` scroll the panel as before.

---

## Stream Event Types

New event type constants added to `internal/envelope/envelope.go`:

```go
const (
    SSEEventTaskStatus = "task_status"
    SSEEventTaskChunk  = "task_chunk"
    SSEEventTaskDone   = "task_done"
)
```

These are emitted within the existing `POST /signal` SSE stream, interleaved with the existing `step`, `chunk`, `tool`, and `done` events. No new endpoints. No new protocol.

The `step` event already signals the start of a pipeline phase. When a step contains parallel tasks, the server emits `task_status` events for each task as they start. This replaces the need for a separate `pipeline_start` event -- the existing `step` event already carries the pipe name, and the first `task_status` events define the task manifest implicitly.

### Event Payloads

**`task_status`** -- sent when a task starts, changes activity, or transitions state.

```json
{
    "task_id": "t1",
    "name": "pricing-table",
    "pipe": "build",
    "status": "running",
    "activity": "edit pricing.go",
    "depends_on": ["t3"]
}
```

Status values: `"waiting"`, `"running"`, `"done"`, `"failed"`.

The first `task_status` event for a task ID implicitly registers it. No separate manifest event needed.

**`task_chunk`** -- streaming output from a running task. Same shape as the existing `chunk` event with a `task_id` field added.

```json
{
    "task_id": "t1",
    "text": "Reading the existing pricing table..."
}
```

**`task_done`** -- sent when a task completes or fails.

```json
{
    "task_id": "t1",
    "status": "done",
    "duration": "31.2s",
    "error": ""
}
```

The pipeline's final result is still delivered via the existing `done` event with a full envelope. No `pipeline_end` event needed -- `done` already serves this purpose.

### Event flow

```
← route         {"pipe": "build-pipeline"}
← step          {"pipe": "spec"}
← chunk         {"text": "..."}
← step          {"pipe": "build"}
← task_status   {"task_id": "t1", "name": "pricing-table", "status": "running", ...}
← task_status   {"task_id": "t2", "name": "api-endpoint", "status": "running", ...}
← task_status   {"task_id": "t3", "name": "test-fixtures", "status": "running", ...}
← task_status   {"task_id": "t4", "name": "migration", "status": "waiting", "depends_on": ["t1"]}
← task_status   {"task_id": "t5", "name": "docs", "status": "waiting", "depends_on": ["t1", "t2"]}
← task_chunk    {"task_id": "t1", "text": "..."}
← task_status   {"task_id": "t1", "activity": "edit pricing.go", ...}
← task_done     {"task_id": "t3", "status": "done", "duration": "31.2s"}
← task_status   {"task_id": "t4", "status": "running", ...}
← task_done     {"task_id": "t1", "status": "done", "duration": "42.1s"}
← ...
← step          {"pipe": "verify"}
← step          {"pipe": "publish"}
← done          {full envelope}
```

---

## Server-side Changes

**`internal/runtime/runtime.go`** -- `ExecuteStream` gains a code path for parallel steps. When the plan contains parallel tasks, it:

1. Emits a `step` event for the parallel phase (same as today).
2. Emits `task_status` events as tasks are dispatched to goroutines.
3. Wraps each task's chunk sink to emit `task_chunk` events instead of bare `chunk` events.
4. Emits `task_status` events when tasks report activity (tool calls).
5. Emits `task_done` events when tasks complete.
6. After all parallel work completes, resumes sequential `step` events.

**`internal/runtime/status.go`** (new) -- `StatusSink` type and `StatusEvent` struct. The sink adapter translates `StatusEvent` values to `StreamEvent` values:

```go
type StatusSink func(event StatusEvent)

type StatusEvent struct {
    TaskID   string
    Type     string // "status", "chunk", "done"
    Status   string
    Name     string
    Pipe     string
    Activity string
    Text     string
    Duration time.Duration
    Error    string
    DependsOn []string
}
```

The `StatusSink` is called from task goroutines. The runtime wraps it with a mutex, the same pattern used by the existing `sink` in `handleSSE`.

**`internal/server/api.go`** -- The `sink` closure in `handleSSE` gains cases for the three new event types. No structural change.

### Bridge-level status reporting

The bridge's `CompleteStream` method already reports tool calls via the `ToolChunkPrefix` convention. The graph executor wraps each task's chunk sink to intercept tool-prefixed chunks and emit `task_status` events with the tool name as activity. No changes to the bridge interface.

---

## TUI Client: Event Handling

### In `readNextEventSync`

The existing event reader gains three new cases:

```go
case envelope.SSEEventTaskStatus:
    var ts struct {
        TaskID    string   `json:"task_id"`
        Name      string   `json:"name"`
        Pipe      string   `json:"pipe"`
        Status    string   `json:"status"`
        Activity  string   `json:"activity"`
        DependsOn []string `json:"depends_on"`
    }
    if err := json.Unmarshal([]byte(event.Data), &ts); err == nil {
        return taskStatusMsg{ts: ts, streamID: streamID, reader: reader}
    }
    continue

case envelope.SSEEventTaskChunk:
    var tc struct {
        TaskID string `json:"task_id"`
        Text   string `json:"text"`
    }
    if err := json.Unmarshal([]byte(event.Data), &tc); err == nil {
        return taskChunkMsg{taskID: tc.TaskID, text: tc.Text, streamID: streamID, reader: reader}
    }
    continue

case envelope.SSEEventTaskDone:
    var td struct {
        TaskID   string `json:"task_id"`
        Status   string `json:"status"`
        Duration string `json:"duration"`
        Error    string `json:"error"`
    }
    if err := json.Unmarshal([]byte(event.Data), &td); err == nil {
        return taskDoneMsg{td: td, streamID: streamID, reader: reader}
    }
    continue
```

### In `model.Update`

```go
case taskStatusMsg:
    if msg.streamID == m.activeStreamID {
        m.upsertParallelTask(msg.ts)
        m.showPipelinePanel()
    }
    return m, readNextEvent(msg.reader, msg.streamID)

case taskChunkMsg:
    if msg.streamID == m.activeStreamID {
        m.appendTaskOutput(msg.taskID, msg.text)
    }
    return m, readNextEvent(msg.reader, msg.streamID)

case taskDoneMsg:
    if msg.streamID == m.activeStreamID {
        m.completeParallelTask(msg.td)
        m.showPipelinePanel()
        // Stream notification for completed tasks
        label := m.taskLabel(msg.td.TaskID)
        if msg.td.Status == "failed" {
            m.stream.Append(KindNotification, SymArrow+" "+label+": failed")
        }
    }
    return m, readNextEvent(msg.reader, msg.streamID)
```

### Panel rendering

`formatPipelineSteps()` is extended: when `m.parallelTasks` is non-nil, the current step's children are rendered as indented task lines with status symbols. The logic follows the same pattern as the existing tool-call rendering (indented under the active step) but adds the parallel task tree.

When `m.panelExpanded >= 0`, the task's captured output is appended below the tree, separated by a dim rule. The panel viewport handles scrolling.

### Cleanup on pipeline end

When `streamDoneMsg` arrives (the existing `done` event), parallel state is cleared:

```go
m.parallelTasks = nil
m.panelSelected = -1
m.panelExpanded = -1
```

This happens in the existing `handleStreamDone` method. The panel continues to show the completed pipeline tree (all checkmarks) until the user moves on, same as today.

---

## Relevant Files

### Existing Files (Modified)

- `internal/envelope/envelope.go` -- add three SSE event type constants (`task_status`, `task_chunk`, `task_done`)
- `internal/tui/tui.go` -- add `parallelTask` type and tracking fields to `model`; add message type cases in `Update`; extend `readNextEventSync`; extend `formatPipelineSteps` for parallel tree rendering; add panel selection/expansion logic to `handleKey`
- `internal/tui/theme.go` -- no changes needed; existing styles cover all rendering
- `internal/runtime/runtime.go` -- extend `ExecuteStream` to emit task events during parallel execution
- `internal/server/api.go` -- extend the `sink` closure to write the three new event types

### New Files

- `internal/runtime/status.go` -- `StatusSink` type, `StatusEvent` struct, sink adapter

### Depends On (not part of this spec)

- `internal/runtime/graph.go` -- the graph executor from the pipeline-build spec. This spec assumes it exists and accepts a `StatusSink` callback.

---

## Test Strategy

### Unit Tests

**`internal/tui/tui_test.go`** (extend existing)

1. **Parallel tree rendering** -- set `parallelTasks` with various states, call `formatPipelineSteps()`, verify output matches expected tree with correct symbols and indentation.
2. **Task upsert** -- verify `upsertParallelTask` creates new tasks and updates existing ones.
3. **Task completion** -- verify `completeParallelTask` sets duration and status.
4. **Cleanup on done** -- verify parallel state is cleared when `streamDoneMsg` arrives.
5. **Panel selection** -- verify Ctrl+J/K move `panelSelected` through the task list when parallel tasks are active.
6. **Task expansion** -- verify Enter toggles `panelExpanded` and output appears in the formatted panel content.

**`internal/runtime/status_test.go`**

1. **Sink adapter** -- verify `StatusEvent` values produce correct `StreamEvent` JSON payloads.
2. **Concurrent safety** -- fire status events from multiple goroutines with `-race`.

### Integration Tests

**`internal/server/sse_test.go`** (extend existing)

1. **Parallel task flow** -- trigger a pipeline with parallel tasks. Verify the SSE stream contains `task_status`, `task_chunk`, `task_done` events interleaved with existing event types.
2. **Sequential plan unchanged** -- trigger a simple plan. Verify no task events appear.

### Manual Testing

1. Run a pipeline with parallel tasks. Verify the panel shows the tree with status symbols.
2. Press Ctrl+P to open the panel. Verify parallel branches appear under the active step.
3. Use Ctrl+J/K to navigate tasks. Press Enter to expand a task's output.
4. Cancel with double-Esc. Verify all tasks are terminated.
5. Run a simple query. Verify the TUI is completely unchanged.
6. Resize the terminal during parallel execution. Verify the panel adapts.

---

## Implementation Order

1. **Event constants** -- add three new constants to `envelope.go`. No functional change.
2. **StatusSink types** -- create `internal/runtime/status.go` with type definitions and sink adapter.
3. **TUI parallel rendering** -- extend `formatPipelineSteps` in `tui.go` to render parallel task trees. Test with synthetic data.
4. **TUI event handling** -- extend `readNextEventSync` and `model.Update` in `tui.go` for the three new event types.
5. **Panel interaction** -- add selection and expansion logic to `handleKey` for parallel tasks.
6. **Wire into runtime** -- extend `ExecuteStream` to emit task events during parallel execution.
7. **Wire into server** -- extend the `sink` closure in `handleSSE`.

Steps 1-5 can be developed and tested with synthetic data before the graph executor exists. Steps 6-7 require the graph executor from the pipeline-build spec.

---

## Review Notes

This spec was substantially rewritten to align with `docs/tui.md` and `docs/virgil.md`. Key changes:

1. **Eliminated the separate dashboard view.** The original spec created a new bubbletea model that replaced the stream with a full-screen dashboard. This directly violated tui.md's core design: "The stream is the soul of the interface" and "The detail panel shows the internals of a running pipeline." Parallel task detail now renders inside the existing panel, extending `formatPipelineSteps()`.

2. **Restored the input line.** The original spec hid the input area during parallel execution ("The input area is hidden during dashboard mode since the pipeline is autonomous"). tui.md explicitly states: "Nothing blocks the input line" and "Even when Virgil is processing, you can type." The input line is never touched.

3. **Removed the summary bar and key hints.** The original spec had a persistent summary bar ("5 tasks | 2 running | 1 done | $0.12") and a key hint line at the bottom. These violate "earned attention" -- permanent chrome for transient information. The stream already shows brief notifications ("building (5 tasks, 2 running)") and the separator already shows session cost.

4. **Removed the elapsed-time ticker.** The original spec had a `dashboardTickMsg` that updated an elapsed time display every tick. tui.md says "No animation dependency -- no spinners, progress bars, or animated transitions carry meaning." Duration is shown on completed tasks (static text), not as a live counter.

5. **Removed token/cost columns from the task list.** Per-task token counts and cost are implementation metrics, not user-facing information. They violate "proportional presence" -- the user wants to know what tasks are doing, not how many tokens they've consumed. Aggregate cost flows through the existing `envelope.Usage` on the final `done` event and appears in the separator line.

6. **Fixed keybinding conflicts.** The original spec reassigned `Ctrl+U` and `Ctrl+D` (which are stream scroll bindings) and introduced `j`/`k`/`q` as rune-level bindings that would conflict with text input. The revised spec reuses the existing `Ctrl+J/K` panel scroll bindings and `Enter`/`Esc` for expansion, all within the panel context where they already apply.

7. **Reduced from five new event types to three.** `pipeline_start` was unnecessary -- the existing `step` event already signals phase transitions, and task manifests are implied by the first `task_status` events. `pipeline_end` was unnecessary -- the existing `done` event already carries the final envelope. This avoids protocol bloat.

8. **Eliminated the separate `dashboard.go` file.** The parallel task rendering is an extension of `formatPipelineSteps()` in `tui.go`, not a separate sub-model. The `parallelTask` struct and handful of helper methods do not warrant a new file or a new bubbletea model. If the code grows large enough to extract, that is a refactoring decision, not an architecture decision.

9. **Matched the tree structure from tui.md.** The original spec used a tabular layout with column headers (#, task, pipe, status, time, tokens, activity). tui.md already shows the panel format: indented tree with `├`/`└` connectors and status symbols. The revised spec uses this exact format.

10. **Removed `sse.Reader` references from message types.** The original spec threaded `*sse.Reader` through new message types. This correctly follows the existing pattern in the codebase (every msg carries the reader for continuation), so the revised event handling preserves this pattern for the three remaining message types.
