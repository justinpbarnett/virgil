# Feature: Speculative Acknowledgement

## Metadata

type: `feat`
task_id: `speculative-ack`
prompt: `Add an acknowledgement response to non-deterministic workflows. On submit, the server generates a fast ack via Gemini 3.1 Flash Preview concurrently with the real pipeline. The ack streams to the TUI immediately while the real work runs behind it. Deterministic pipes skip the ack. The ack model is configurable in virgil.yaml.`

## Feature Description

Virgil's AI-backed pipes (chat, draft, code, review, etc.) take 2-8 seconds before the first token reaches the user. During this time, the user stares at "thinking..." dots.

This feature creates the illusion of instant response. When the user submits a signal that routes to an AI-backed pipe, the server starts two concurrent operations:

1. **Ack generation** — a lightweight Gemini 3.1 Flash Preview call that produces a 1-2 sentence acknowledgement personalized with pre-fetched memories. Tokens stream to the TUI within ~200ms.
2. **Real pipeline** — the normal planner → memory injection → pipe execution flow runs concurrently. When it completes, the response follows the ack in the stream.

The ack is context-aware. It includes the user's signal and any pre-fetched memories, so it produces personalized responses like "On it — drafting an email to get out of work since you're sick today" rather than generic placeholders.

Deterministic pipes (those with no configured provider — calendar, memory, fs, shell) skip the ack entirely. They're already fast enough that an ack would add latency, not reduce it.

The ack is entirely server-side. The TUI remains a thin renderer — it just handles a new `ack` SSE event type the same way it handles `chunk` events. No speculative logic, no caching, no idle detection in the client.

## User Story

As a Virgil user
I want responses to feel instant even when the real work takes seconds
So that the interaction feels conversational rather than transactional

## Relevant Files

### Existing Files (Modified)

- `config/virgil.yaml` — Add `ack` section with `provider`, `model`, and `max_tokens` fields.
- `internal/config/config.go` — Add `AckConfig` struct and `Ack` field to `Config`. Parse from virgil.yaml with defaults.
- `internal/server/api.go` — Modify `handleSSE()` to start concurrent ack generation for AI-backed pipes. Add ack prompt and Gemini Flash call. Stream `SSEEventAck` events before real pipeline chunks.
- `internal/server/server.go` — Add `ackProvider` field to `Server` struct. Initialize from `AckConfig` in `New()`.
- `internal/envelope/envelope.go` — Add `SSEEventAck = "ack"` constant.
- `internal/tui/tui.go` — Handle `SSEEventAck` in `readNextEventSync()` — treat ack chunks identically to regular chunks (append to pending, render in stream).
- `internal/bridge/usage.go` — Add `gemini-3.1-flash-preview` to the `CostTable`.

### New Files

None. The ack logic is small enough to live in `api.go` alongside `handleSSE()`.

## Implementation Plan

### Phase 1: Configuration

Add ack model configuration to `virgil.yaml` so the ack provider/model is user-controllable.

### Phase 2: Server-Side Ack Generation

Modify the SSE handler to detect AI-backed pipes (pipe has a configured provider) and start a concurrent ack stream using the configured ack model. The ack streams first, then the real pipeline output follows.

### Phase 3: TUI Event Handling

Add `SSEEventAck` handling to the TUI's SSE event loop. Ack chunks render identically to response chunks — no visual distinction.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add Ack Configuration

- In `internal/config/config.go`, add:
  ```go
  type AckConfig struct {
      Provider  string `yaml:"provider"`
      Model     string `yaml:"model"`
      MaxTokens int    `yaml:"max_tokens"`
  }
  ```
- Add `Ack AckConfig` field to the `Config` struct (after `Provider`).
- In `Load()`, set defaults after loading virgil.yaml: if `cfg.Ack.Provider` is empty, default to `"gemini"`. If `cfg.Ack.Model` is empty, default to `"gemini-3.1-flash-preview"`. If `cfg.Ack.MaxTokens` is 0, default to `256`.
- In `config/virgil.yaml`, add the ack section:
  ```yaml
  ack:
    provider: gemini
    model: gemini-3.1-flash-preview
    max_tokens: 256
  ```

### 2. Add SSE Event Constant and Cost Table Entry

- In `internal/envelope/envelope.go`, add `SSEEventAck = "ack"` alongside the existing SSE event constants.
- In `internal/bridge/usage.go`, add `"gemini-3.1-flash-preview"` to `CostTable`. Use Gemini Flash-tier pricing: `{InputPerMToken: 0.10, OutputPerMToken: 0.40}` (adjust when Google publishes official pricing).

### 3. Initialize Ack Provider on Server

- In `internal/server/server.go`, add `ackProvider bridge.StreamingProvider` field to the `Server` struct.
- In `New()` (or a new init helper), create the ack provider from `Deps.Config.Ack`:
  ```go
  ackCfg := bridge.ProviderConfig{
      Name:      cfg.Ack.Provider,
      Model:     cfg.Ack.Model,
      MaxTokens: cfg.Ack.MaxTokens,
  }
  provider, err := bridge.CreateProvider(ackCfg)
  ```
  If creation fails (e.g., missing API key), log a warning and leave `ackProvider` nil. The feature degrades gracefully.
- Pass the ack provider through `Deps` or set it directly on the server.

### 4. Add Ack Generation to SSE Handler

- In `internal/server/api.go`, modify `handleSSE()`. After routing and planning (line 111-112), before memory prefetch:
  1. Check if the routed pipe is AI-backed: look up the pipe's config and check if `EffectiveProvider(globalDefault)` returns a non-empty provider name. Since every pipe inherits the global provider, refine the check: a pipe is deterministic if its `PipeConfig.Provider` is explicitly empty AND it has no `prompts.system` configured. Simpler alternative: check if the pipe has a registered `StreamHandler` — deterministic pipes only register sync handlers.
  2. If AI-backed and `s.ackProvider != nil`:
     - Start a goroutine that generates the ack:
       ```go
       go func() {
           // Build ack prompt
           system := ackSystemPrompt  // constant string
           user := buildAckUserPrompt(req.Text, seed.Memory)

           s.ackProvider.CompleteStream(r.Context(), system, user, func(chunk string) {
               mu.Lock()
               w.Write([]byte(formatSSEEvent("ack", chunk)))
               flusher.Flush()
               mu.Unlock()
           })
           ackDone <- struct{}{}
       }()
       ```
     - The ack goroutine writes `ack` SSE events directly to the response writer.
     - Use a `sync.Mutex` to coordinate writes between the ack goroutine and the main pipeline goroutine, since both write to the same `http.ResponseWriter`.
     - The ack goroutine runs with `r.Context()` so it's cancelled if the client disconnects.
  3. The main flow continues: prefetch memory, execute pipeline, stream chunks.
  4. The real pipeline's chunk/step/done events interleave naturally after the ack finishes. Since the ack completes in ~500ms and the real pipeline takes 2-8s, the ack will almost always finish before the first real chunk arrives.

- Define the ack system prompt as a package-level constant:
  ```go
  const ackSystemPrompt = `You are acknowledging a request. Write 1-2 sentences max.
  Be specific — reference what the user asked for. If context is provided,
  use relevant details to personalize. Use action phrases like "Sure, getting
  started on..." or "On it —". Do not perform the task, just acknowledge it.`
  ```

- Define `buildAckUserPrompt(signal string, memories []envelope.MemoryEntry) string`:
  - Start with `"Request: " + signal`.
  - If memories are non-empty, append a `"\n\nContext:\n"` section with memory content summaries.

- For memory in the ack: pre-fetch memory concurrently (the existing `PrefetchMemory` call). The ack goroutine can wait on the same memory channel, or use its own lightweight fetch. Simplest approach: start the ack *after* the memory prefetch channel is created, and have the ack goroutine read from a copy of the prefetched memories once they're available. Since the ack prompt is tiny, even a 50ms memory fetch delay still yields sub-300ms TTFT.

- Handle the write coordination carefully:
  ```go
  var mu sync.Mutex
  ackDone := make(chan struct{}, 1)

  // Start ack goroutine (writes ack events)
  // ...

  // Main pipeline (writes chunk/step/done events)
  sink := func(event runtime.StreamEvent) {
      mu.Lock()
      defer mu.Unlock()
      // write event as before
  }

  result := s.runtime.ExecuteStream(r.Context(), plan, execSeed, sink)
  <-ackDone // ensure ack goroutine finished before writing done
  mu.Lock()
  writeSSEEvent(w, flusher, envelope.SSEEventDone, result)
  mu.Unlock()
  ```

### 5. Handle Ack Events in TUI

- In `internal/tui/tui.go`, in `readNextEventSync()` (line 767), add a case for `envelope.SSEEventAck`:
  ```go
  case envelope.SSEEventAck:
      var chunk struct {
          Text string `json:"text"`
      }
      if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
          continue // skip malformed ack chunks
      }
      return streamChunkMsg{text: chunk.Text, streamID: streamID, reader: reader}
  ```
  Ack chunks produce the same `streamChunkMsg` as regular chunks. The TUI renders them identically — they appear as the beginning of the response. No visual distinction, no special state. The ack text flows naturally into the stream, and when real chunks arrive, they continue after it.

### 6. Add Tests

- In `internal/server/api_test.go` (or a new `internal/server/ack_test.go` if the file gets large):
  - Test that SSE handler sends ack events for AI-backed pipes when ackProvider is configured.
  - Test that SSE handler skips ack for pipes with no provider configured (deterministic).
  - Test that SSE handler works normally when ackProvider is nil (graceful degradation).
  - Test that ack events arrive before chunk events in the SSE stream.

- In `internal/config/config_test.go`:
  - Test AckConfig defaults (provider=gemini, model=gemini-3.1-flash-preview, max_tokens=256).
  - Test AckConfig parsing from YAML.

## Testing Strategy

### Unit Tests

- Config: AckConfig defaults, YAML parsing, empty/partial configs.
- Server: ack events for AI-backed pipes, no ack for deterministic pipes, nil ackProvider graceful degradation, ack-before-chunk ordering.
- Cost table: `gemini-3.1-flash-preview` entry returns correct cost.

### Edge Cases

- Ack provider creation fails (missing API key) → server starts without ack, normal flow unchanged.
- Ack call errors mid-stream → partial ack text rendered, real response follows normally.
- Client disconnects during ack → `r.Context()` cancellation stops both ack and pipeline goroutines.
- Ack finishes after first real chunk arrives → chunks interleave. This is acceptable since both are valid response text. In practice, ack (~500ms) almost always finishes before real pipeline (~2-8s).
- Deterministic pipe (calendar) → no ack, chunks stream directly.
- Very fast AI pipe (cached response) → ack and real response may overlap. Stream interleaving is safe — TUI appends all chunks to `pending` in order.

## Risk Assessment

- **Wasted ack calls**: Every AI-backed signal triggers an ack call (~$0.00005 per call at Gemini Flash pricing). This is negligible. If cost is a concern, the user can remove the `ack` section from virgil.yaml to disable the feature entirely.
- **Write coordination**: The mutex on `http.ResponseWriter` adds minimal overhead. SSE writes are small and fast. The main risk is a deadlock — mitigated by keeping critical sections short (just the write+flush) and never holding the lock across blocking operations.
- **User confusion**: The ack says "getting started on X" and then the real response arrives seconds later. Mitigation: the ack is short (1-2 sentences), and the real response follows naturally in the same stream. The transition reads like a natural conversation: acknowledgement → pause → result.
- **Ack provider unavailability**: If Gemini is down or the API key isn't set, `ackProvider` is nil and the feature is silently disabled. No functionality is lost — the user sees the current behavior (thinking dots → response).

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test    # all unit tests pass
just build   # compiles cleanly
just lint    # no lint errors
```

## Open Questions (Resolved)

1. **Ack visual distinction**: No. The ack renders as the start of a natural response. Virgil.md shows natural conversation flow in the stream — the ack should be indistinguishable from the beginning of a response. The real response continues after it seamlessly.

2. **Ack model configuration**: Configurable via `virgil.yaml` under the `ack` section. Defaults to `gemini` provider with `gemini-3.1-flash-preview` model. Users can change the provider/model or remove the section entirely to disable.

3. **Determinism detection**: Inferred from existing config — a pipe is AI-backed if it has a provider configured (via `EffectiveProvider()`). No new `Deterministic` field on `Definition` or `pipe.yaml` changes needed. This respects virgil.md's principle that "the distinction between deterministic and non-deterministic pipes is an implementation detail."

4. **Ack for multi-step pipelines**: Yes. The ack fires based on whether the terminal pipe is AI-backed. Multi-step plans are slower, making the ack even more valuable.

5. **Client vs server responsibility**: Server-side only. Virgil.md: "The server handles all logic. Clients are thin renderers." The TUI just renders a new SSE event type — no speculative logic, no caching, no idle detection.

## Sub-Tasks

Single task — no decomposition needed.
