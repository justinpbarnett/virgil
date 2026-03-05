# Feature: Session Cost Counter

## Metadata

type: `feat`
task_id: `session-cost-counter`
prompt: `Add a cost counter that tracks the cost of all API calls to all models for that session. Reset on :clear or application start/stop. Use docs/tui.md for design and placement guidelines.`

## Feature Description

Virgil makes API calls to multiple providers (Anthropic, OpenAI, Gemini, xAI, Claude CLI) across multiple pipes per session. There's currently no visibility into what these calls cost. Each provider returns token usage data in its API response, but the bridge layer discards it.

This feature adds a session-scoped cost counter that:

1. Extracts token usage (input + output tokens) from every provider API response
2. Multiplies by a per-model pricing table to compute cost
3. Accumulates cost across all API calls for the session
4. Displays the running total in the TUI
5. Resets on `:clear`, application start, and application stop

The counter follows the TUI's "earned attention" principle — it's visible but never competing with the conversation. It sits in the separator line between stream and input, rendered in the Dim style, showing only the dollar amount. It appears after the first API call and stays visible for the rest of the session.

## User Story

As a Virgil user
I want to see how much my session's API calls have cost
So that I can be aware of spending and adjust my usage patterns

## Relevant Files

### Existing Files (Modified)

- `internal/bridge/bridge.go` — Add `Usage` struct and extend provider interfaces to return usage data alongside responses. Add `CostTable` with per-model pricing.
- `internal/bridge/anthropic.go` — Parse `usage.input_tokens` and `usage.output_tokens` from API response body. Return `Usage` from `Complete` and `CompleteStream`.
- `internal/bridge/openai.go` — Parse `usage.prompt_tokens` and `usage.completion_tokens` from API response body. For streaming, accumulate from final chunk's usage field.
- `internal/bridge/gemini.go` — Parse `usageMetadata.promptTokenCount` and `usageMetadata.candidatesTokenCount` from API response body.
- `internal/bridge/claude.go` — Parse usage from CLI JSON output if available; estimate from response length if not.
- `internal/bridge/bridge_test.go` — Test `Usage` struct, `CostTable` lookups, `CostFor` computation.
- `internal/envelope/envelope.go` — Add `Usage` field to `Envelope` to carry token counts and cost through the pipeline.
- `internal/pipehost/host.go` — Pipe subprocesses already return envelopes; the `Usage` field flows through naturally via JSON serialization.
- `internal/server/api.go` — Include usage data in SSE `done` events. Add new SSE event type `cost` for per-call cost reporting.
- `internal/runtime/runtime.go` — After pipe execution, extract usage from the envelope and include in the returned result.
- `internal/tui/tui.go` — Add `sessionCost` field to model. Handle `cost` SSE events. Reset on `:clear` and init.
- `internal/tui/stream.go` — Render cost in the separator line between stream and input.
- `internal/tui/theme.go` — No new styles needed; use existing `Dim` style for cost display.
- `internal/tui/command.go` — Reset `sessionCost` when `:clear` is executed.

### New Files

- `internal/bridge/usage.go` — `Usage` struct, `CostTable` map, `CostFor()` function, and per-model pricing constants. Separated from `bridge.go` to keep pricing data isolated and easy to update.
- `internal/bridge/usage_test.go` — Tests for cost calculation, table lookups, unknown model fallback.

## Implementation Plan

### Phase 1: Usage Tracking Infrastructure

Define the `Usage` struct and cost table. This is the foundation — pure data types and math, no integration yet.

The `Usage` struct carries raw token counts and computed cost:

```go
type Usage struct {
    InputTokens  int
    OutputTokens int
    Model        string
    Cost         float64 // computed from tokens × price
}
```

The `CostTable` maps model identifiers to per-token prices:

```go
type ModelPricing struct {
    InputPerMToken  float64 // price per million input tokens
    OutputPerMToken float64 // price per million output tokens
}
```

Pricing is hardcoded and updated manually. Models not in the table get zero cost (no error, no panic — just no cost tracking for unknown models). This is deliberate: the counter should never break the system.

### Phase 2: Provider Instrumentation

Each provider parses token usage from its API response and returns it alongside the text response. The approach differs by provider:

**Anthropic**: The Messages API returns `usage` at the top level of the response JSON. For streaming, the `message_start` event contains initial usage and `message_delta` contains final output token count.

**OpenAI/xAI**: The Chat Completions API returns `usage` in the response body. For streaming, usage appears in the final chunk when `stream_options: {"include_usage": true}` is set in the request.

**Gemini**: The `generateContent` response includes `usageMetadata` with `promptTokenCount` and `candidatesTokenCount`.

**Claude CLI**: The JSON output format includes usage data. Parse if present; if not, set zero usage (CLI fallback is already the least-cost path).

The current `Provider` interface returns `(string, error)`. Rather than changing the interface signature (which would break all callers), providers store their last usage in a field and expose it via a new `LastUsage() Usage` method. This is added as an optional interface:

```go
type UsageReporter interface {
    LastUsage() Usage
}
```

The runtime checks for this interface after each call and extracts usage if available.

### Phase 3: Pipeline Propagation

After a pipe executes via the runtime, the runtime checks if the provider implements `UsageReporter`, extracts usage, computes cost via `CostFor()`, and attaches it to the envelope's new `Usage` field.

For SSE streaming, a new event type `cost` is emitted after `done`:

```
event: cost
data: {"input_tokens":1523,"output_tokens":842,"model":"claude-sonnet-4-20250514","cost":0.0127}
```

This keeps cost data separate from the response envelope — the cost event is metadata about the call, not part of the pipe's output.

### Phase 4: TUI Display

The TUI accumulates cost from `cost` SSE events into a `sessionCost float64` field on the model.

**Placement**: The cost appears in the separator line between the stream viewport and the input line. Currently this separator is a plain horizontal rule. The cost sits right-aligned on this line:

```
──────────────────────────────────────────────────── $0.03
❯
```

When `sessionCost` is zero (no API calls yet, or just reset), the separator shows no cost — just the plain rule. The cost appears only after the first API call returns usage data. This follows the "proportional presence" principle: nothing to show means nothing shown.

**Formatting**:
- Under $0.01: show `<$0.01` (not `$0.00` — that implies free)
- Under $1.00: show `$0.XX` (two decimal places)
- $1.00 and above: show `$X.XX` (two decimal places)
- The entire cost string uses the `Dim` theme style — visible but not competing for attention

**Reset triggers**:
- Application start: `sessionCost` initializes to `0`
- `:clear` command: `sessionCost` resets to `0`
- Application exit: counter is discarded (not persisted)

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create Usage Types and Cost Table

- Create `internal/bridge/usage.go` with `Usage` struct, `ModelPricing` struct, `CostTable` variable (map[string]ModelPricing), and `CostFor(model string, inputTokens, outputTokens int) float64` function
- Populate `CostTable` with current pricing for: `claude-sonnet-4-20250514`, `claude-haiku-4-5-20251001`, `claude-opus-4-20250514`, `gpt-4o`, `gpt-4o-mini`, `gpt-4.1`, `gpt-4.1-mini`, `gpt-4.1-nano`, `gemini-2.0-flash`, `gemini-2.5-pro`, `grok-3`, `grok-3-mini`. Use current published pricing.
- `CostFor` returns 0.0 for models not in the table (silent fallback)
- Create `internal/bridge/usage_test.go` with tests: known model cost calculation, unknown model returns zero, zero tokens returns zero cost

### 2. Add UsageReporter Interface

- In `internal/bridge/bridge.go`, add the `UsageReporter` interface with `LastUsage() Usage` method
- This is an optional interface — not all providers must implement it

### 3. Instrument Anthropic Provider

- In `internal/bridge/anthropic.go`, add `lastUsage Usage` field to the provider struct
- In the `Complete` method: parse `usage.input_tokens` and `usage.output_tokens` from the JSON response body; compute cost via `CostFor`; store in `lastUsage`
- In `CompleteStream`: parse usage from `message_start` and `message_delta` SSE events; store final counts in `lastUsage`
- Add `LastUsage() Usage` method
- In `internal/bridge/anthropic_test.go`, add tests verifying usage extraction from mock responses

### 4. Instrument OpenAI Provider

- In `internal/bridge/openai.go`, add `lastUsage Usage` field to the provider struct
- In the `Complete` method: parse `usage.prompt_tokens` and `usage.completion_tokens` from JSON response; compute cost; store in `lastUsage`
- In `CompleteStream`: add `stream_options: {"include_usage": true}` to the request body; parse usage from the final streaming chunk; store in `lastUsage`
- Add `LastUsage() Usage` method
- In `internal/bridge/openai_test.go`, add tests verifying usage extraction

### 5. Instrument Gemini Provider

- In `internal/bridge/gemini.go`, add `lastUsage Usage` field to the provider struct
- In `Complete`: parse `usageMetadata.promptTokenCount` and `usageMetadata.candidatesTokenCount` from JSON response; compute cost; store in `lastUsage`
- In `CompleteStream`: parse usage metadata from final SSE event; store in `lastUsage`
- Add `LastUsage() Usage` method
- In `internal/bridge/gemini_test.go`, add tests verifying usage extraction

### 6. Instrument Claude CLI Provider

- In `internal/bridge/claude.go`, add `lastUsage Usage` field to the provider struct
- In `Complete` (JSON output mode): check if the parsed JSON output contains usage fields; if so, extract and store. If not, set zero usage.
- In `CompleteStream`: same approach — check for usage in output, zero if absent
- Add `LastUsage() Usage` method

### 7. Add Usage Field to Envelope

- In `internal/envelope/envelope.go`, add `Usage *Usage` field to the `Envelope` struct (pointer, nil when no usage data). Import `bridge.Usage` type or define a local `EnvelopeUsage` to avoid circular import — if circular, define a mirror struct in envelope package with the same fields.
- The field serializes to JSON naturally; nil omits it (`json:",omitempty"`)

### 8. Extract Usage in Runtime

- In `internal/runtime/runtime.go`, after a pipe's handler returns (both `Execute` and `ExecuteStream`), check if the provider from the registry implements `UsageReporter`
- If it does, call `LastUsage()` and attach to the result envelope's `Usage` field
- For pipes that use subprocess execution (not direct handler), usage comes through the envelope JSON from the subprocess — the pipehost already serializes the full envelope

### 9. Emit Cost SSE Event

- In `internal/server/api.go`, in `handleSSE()`: after writing the `done` event, check if the final envelope has non-nil `Usage`. If so, write an additional SSE event with type `cost` and the usage data as JSON.
- In `internal/envelope/envelope.go`, add `SSEEventCost = "cost"` constant alongside existing event type constants

### 10. Handle Cost Events in TUI

- In `internal/tui/tui.go`, add `sessionCost float64` field to the model struct
- In `readNextEvent()` (or the SSE event dispatch), handle `cost` events: parse the usage JSON, add `Cost` to `sessionCost`
- Create a new `costMsg` bubbletea message type carrying the cost delta
- In `Update()`, handle `costMsg` by adding to `sessionCost`

### 11. Render Cost in Separator

- In `internal/tui/stream.go` (or wherever the separator between stream and input is rendered — check `tui.go`'s `View()` method): modify the separator line to include the cost display right-aligned
- Format: when `sessionCost > 0`, render `"── ... ── $X.XX"` with cost in `Dim` style, right-aligned. When zero, render plain separator.
- Use the formatting rules: `<$0.01` for sub-cent, `$X.XX` for everything else

### 12. Reset on :clear

- In `internal/tui/command.go`, in the `:clear` handler, set `m.sessionCost = 0`

## Testing Strategy

### Unit Tests

- `internal/bridge/usage_test.go` — `CostFor` with known models, unknown models, zero tokens, large token counts
- `internal/bridge/anthropic_test.go` — Usage parsing from mock API response JSON and mock SSE stream
- `internal/bridge/openai_test.go` — Usage parsing from mock response JSON and streaming chunks
- `internal/bridge/gemini_test.go` — Usage parsing from mock response JSON
- `internal/bridge/bridge_test.go` — `UsageReporter` interface assertion for each provider type

### Edge Cases

- Provider returns no usage data (e.g., API changes or network error mid-stream): `LastUsage()` returns zero-value `Usage`, cost is $0.00, counter unaffected
- Unknown model not in cost table: `CostFor` returns 0, counter doesn't increment, no error
- Multiple rapid API calls: each `cost` SSE event adds independently; order doesn't matter since it's simple addition
- `:clear` between API calls: counter resets; subsequent calls start from zero
- Extremely large token counts: `float64` handles this fine for dollar amounts
- Pipe subprocess mode: usage flows through envelope JSON serialization if the subprocess provider supports it; otherwise nil usage, zero cost

## Risk Assessment

**Low risk — additive feature with graceful degradation:**

- `UsageReporter` is an optional interface. Providers that don't implement it simply contribute zero cost. No existing behavior changes.
- `Usage` on `Envelope` is a pointer with `omitempty`. Existing serialization/deserialization is unaffected — nil usage is omitted from JSON.
- The `cost` SSE event is a new event type. Existing TUI code ignores unknown SSE events (the reader falls through unmatched event types). Even if the TUI isn't updated yet, the server can emit cost events safely.
- Cost display in the separator is purely visual. If cost is zero, the separator renders exactly as before.
- Pricing table is static. Stale prices produce slightly inaccurate costs but never errors. Updating prices is a one-line change per model.

**Circular import risk**: `envelope` package importing `bridge.Usage` — if this creates a cycle, define `EnvelopeUsage` as a separate struct in the envelope package with identical fields. The runtime copies fields when attaching usage.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test
just lint
just build
```

## Open Questions (Unresolved)

1. **Should cost persist across TUI reconnections?** If the TUI disconnects and reconnects to the same server session, should the server track cumulative cost and re-send it? **Recommendation**: No — keep it simple. The counter is TUI-local and session-scoped. Reconnection starts a fresh counter. This matches the "reset on start" semantics.

2. **Should the Claude CLI provider estimate tokens from response length?** The CLI may not expose usage in all output modes. **Recommendation**: No estimation. If usage data isn't in the CLI output, report zero. Inaccurate estimates are worse than no data. As the multi-provider API feature lands, the CLI provider becomes a fallback anyway.

3. **Should there be a `:cost` command for detailed breakdown?** A command showing per-model and per-pipe cost breakdown would be useful for power users. **Recommendation**: Out of scope for this spec. The accumulator tracks total cost only. A breakdown feature can be added later by storing per-call usage entries instead of just the running total.

## Sub-Tasks

Single task — no decomposition needed.
