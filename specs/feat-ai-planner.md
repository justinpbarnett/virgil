# Feature: Layer 4 AI Planner

## Metadata

type: `feat`
task_id: `ai-planner`
prompt: `Replace the Layer 4 stub fallback with an AI planner that uses Grok 4 Fast to deduce intent and produce complete execution plans. When deterministic Layers 1-3 fail, the planner sees the pipe catalogue, composes the right pipe or pipeline on the fly, and returns a plan the runtime can execute directly. This supersedes the original feat-layer4-ai-routing spec.`

## Feature Description

Layer 4 is currently a stub: if Layers 1-3 produce no confident match, the router returns `{pipe: "chat", confidence: 0.0}` unconditionally. Every ambiguous signal — including signals that clearly need a specific pipe or a multi-step pipeline — gets dumped into bare chat with minimal context.

This feature replaces the stub with a Grok 4 Fast-powered AI planner. When the deterministic router exhausts Layers 1-3 without a confident match, the AI planner reads the raw signal, inspects the full pipe catalogue (names, descriptions, and flag schemas), and produces a structured plan — either a single pipe with appropriate flags or an ephemeral multi-step pipeline composed on the fly.

### What changes

**The server orchestrates AI planning with immediate ack.** The router stays fast — `Route()` only tries Layers 1-3 (deterministic, <1ms). When they miss, the router returns a fallback result immediately. The server detects this and fires two things concurrently: (1) an ack stream so the user sees feedback within milliseconds, and (2) the AI planner call. When the AI planner returns, its plan is used for execution. The ack is already streaming while the planner thinks.

**The AI planner can compose ephemeral pipelines.** Unlike the deterministic planner (which only selects from pre-defined templates), the AI planner can chain arbitrary pipes based on intent. For example:

- `"what pipe should we build next?"` → `[study(source=codebase, role=planner, budget=4000), chat]` — the AI recognizes this needs codebase context before chat can answer intelligently.
- `"summarize my meetings today"` → `[calendar(action=list, range=today), draft(type=summary)]` — compose data retrieval with content generation.
- `"when is my next meeting?"` → `[calendar(action=list, range=today)]` — single pipe, correct flags.
- `"hey"` → `[chat]` — trivially conversational, just route to chat.

**The deterministic layers remain primary.** Layers 1-3 are unchanged. The AI only fires when they fail. Every Layer 4 hit is still logged as a miss with the AI's plan attached, so recurring patterns can be promoted into Layer 2 keywords or deterministic templates over time. The goal is for Layer 4 to shrink, not grow.

**Model defaults to Grok 4 Fast via xAI but is configurable.** This is a planning task: it needs speed (< 3s), structured output, and reasoning about composition — not creative generation. The planner provider and model are configured via a `planner` block in `virgil.yaml` (same structure as `ack`), defaulting to `xai` / `grok-4-fast`. The planner does not inherit the user's default model — it has its own config so the user can swap providers without affecting the main pipeline.

### Why not just a classifier

The original `feat-layer4-ai-routing` spec proposed a classifier that picks a single pipe name. That solves routing but not planning: "what pipe should we build next?" would still route to `chat` (correct) but without the `study` step that provides codebase context. The planner approach solves both routing AND context assembly in one step.

## User Story

As a Virgil user
I want ambiguous or complex signals to be intelligently routed and planned instead of falling back to bare chat
So that my requests get the right context and pipe composition regardless of whether I hit exact keywords

## Relevant Files

### Existing Files (Modified)

- `internal/router/router.go` — `RouteResult` struct: add miss metadata fields (`KeywordsFound`, `KeywordsNotFound`). `Route()` method: populate miss metadata at Layer 4. Remove `missLog` field from `Router` struct and `NewRouter` parameter (miss logging moves entirely to the server).
- `internal/router/misslog.go` — `MissEntry` struct: add `AIPlan` and `AIConfidence` fields to capture the AI planner's output.
- `internal/config/config.go` — Rename `AckConfig` to `ModelEndpoint` (reused for both ack and planner config). Add `Planner` field to `Config`.
- `internal/server/server.go` — `Server` struct: add `aiPlanner`, `missLog`, and `store` fields. Extract `startAck` helper method.
- `internal/server/api.go` — `handleSignal` and `handleSSE`: when Layer 4 fires, start ack immediately and run AI planner concurrently; use AI plan or fall back to deterministic planner. Server owns all miss logging.
- `cmd/virgil/main.go` — `runServer()`: construct the planner provider and AI planner, pass to `server.New()` via `Deps`. Remove `missLog` from `NewRouter` call. Pass `missLog` and `store` to server instead.

### Existing Files (Reference)

- `internal/bridge/bridge.go` — `Provider` interface and `CreateProvider` factory. xAI uses `OpenAIProvider` with `https://api.x.ai/v1` base URL and `XAI_API_KEY`.
- `internal/bridge/openai.go` — `OpenAIProvider` constructor used by xAI.
- `internal/pipe/pipe.go` — `Definition` struct with `Name`, `Description`, `Category`, `Flags` fields.
- `internal/runtime/runtime.go` — `Plan` and `Step` types that the AI planner must produce.
- `internal/planner/planner.go` — Deterministic planner (unchanged, still handles Layers 1-3).
- `internal/parser/parser.go` — `ParsedSignal` struct (flows through router, not used by AI planner).

### New Files

- `internal/planner/aiplanner.go` — AI planner: prompt construction, structured response parsing, plan validation, timeout handling.
- `internal/planner/aiplanner_test.go` — Tests for catalogue building, response parsing, plan validation, error fallback, ephemeral pipeline construction.

## Implementation Plan

### Phase 1: AI Planner Core

Create `internal/planner/aiplanner.go` with an `AIPlanner` struct.

The AI planner lives in `internal/planner/` alongside the deterministic planner. Both packages produce `runtime.Plan` from different inputs — the deterministic planner from `RouteResult` + `ParsedSignal`, the AI planner from a raw signal string + memory context. Grouping them keeps all plan-construction logic in one package.

**AIPlanner struct:**

```go
type AIPlanner struct {
    provider   bridge.Provider
    catalogue  string          // pre-built pipe catalogue for the system prompt
    pipeNames  map[string]bool // valid pipe names for response validation
    logger     *slog.Logger
}
```

Three fields: provider, catalogue string, pipe name set. No `pipeFlags` map — resolved question 1 says pass all flags through without stripping, so flag validation infrastructure is unnecessary.

**Constructor** takes `bridge.Provider`, `[]pipe.Definition`, and `*slog.Logger`. Memory context is passed per-call (see `Plan` method below), not stored on the struct — the planner is stateless after init. At init time:

1. Build the catalogue string from definitions — one entry per pipe with name, description, and flag schema. Exclude `chat` from the catalogue (it's the implicit fallback the AI can always select). Format:
   ```
   - calendar: Reads and manages events on calendar services.
     flags: action (list|create|update|delete, default: list), range (today|tomorrow|this_week|next_week)
   - study: Gathers and compresses relevant context from a source within a token budget.
     flags: source (codebase|memory|files), role (general|planner|builder|reviewer), budget (token count), compression (ai|structural)
   - draft: Produces written content from input context and instructions.
     flags: type (summary|email|blog|response)
   ...
   ```
2. Build `pipeNames` set for O(1) pipe name validation.

**`Plan(signal string, memory PlannerMemory) (*runtime.Plan, float64)`** method:

`PlannerMemory` is a lightweight snapshot passed by the server:

```go
// PlannerMemory provides recent context so the planner can handle follow-ups.
type PlannerMemory struct {
    RecentSignals []RecentSignal // last 3 signal→pipe pairs (most recent first)
    WorkingState  []string       // working state summaries (e.g. "calendar/last_query: tomorrow's events")
}

type RecentSignal struct {
    Signal string
    Pipe   string
}
```

The server builds this from `store.RecentInvocations(3)` and `store.ListState("planner")` before calling `Plan()`. This is a fast local SQLite read (~1ms), not an AI call.

1. System prompt (const, built once with catalogue interpolated):
   ```
   You are an intent planner for a personal assistant called Virgil. Given a user's
   signal, determine which pipe(s) to invoke and with what flags.

   Available pipes:
   {catalogue}

   You may select a single pipe or compose a pipeline of multiple pipes. In a pipeline,
   each pipe's output feeds as input to the next pipe. Common patterns:
   - study → chat: gather context from codebase/memory first, then chat answers using that context
   - calendar → draft: retrieve calendar data, then draft a summary
   - study → code: gather context, then generate code

   Respond with ONLY valid JSON. No markdown, no explanation.

   For a single pipe:
   {"pipe": "name", "flags": {"key": "value"}}

   For a multi-step pipeline:
   {"steps": [{"pipe": "name", "flags": {"key": "value"}}, {"pipe": "name2", "flags": {}}]}

   Rules:
   - Use "chat" only when the signal is genuinely conversational and no other pipe fits.
   - When the user asks about the codebase, project, or code: use study(source=codebase) before chat.
   - When the user asks about their data from a specific source: use that source pipe first.
   - Only include flags that are relevant. Omit flags you're unsure about.
   - The last pipe in a pipeline produces the user-facing response.
   - Use the recent history to understand follow-up signals. If the user's signal is ambiguous
     but the recent history shows a specific pipe, prefer that pipe for continuity.
   ```

2. Build the user message from signal + memory context:
   ```
   {signal}

   Recent history:
   - "check my calendar" → calendar
   - "what meetings do I have?" → calendar
   - "hey" → chat

   Working state:
   - calendar/last_query: tomorrow's events
   ```
   If `PlannerMemory` is empty, the user message is just the signal text. The memory section is only appended when non-empty.

3. Call `provider.Complete(ctx, system, userMessage)` with a 5-second timeout (internal `context.WithTimeout`).

4. Parse the JSON response:
   - If `{"pipe": "name", "flags": {...}}` → single-step plan.
   - If `{"steps": [...]}` → multi-step plan.
   - Validate every pipe name against `pipeNames`. If any pipe is unknown, discard and return `nil, 0.0`.
   - If `pipe == "chat"` in a single-step plan with no other steps, return `nil, 0.0` (let the existing fallback handle it — no point in an AI plan that just says "chat").
   - On valid plan: return `*runtime.Plan` with confidence 0.7.

5. On any failure (JSON parse error, provider error, timeout, unknown pipe), log the error and return `nil, 0.0`. Layer 4 must never block or panic.

### Phase 2: Router Changes

Modify `internal/router/router.go`:

The router stays fast — `Route()` still only does Layers 1-3. The AI planner is called by the server, not the router. Three changes:

1. **Remove `missLog` from the router entirely.** The router currently only calls `missLog.Log()` at Layer 4 (`router.go:208-216`). Since Layer 4 miss logging moves to the server, the router's `missLog` field is now dead code. Remove it from the `Router` struct and simplify `NewRouter`:

```go
func NewRouter(defs []pipe.Definition, logger *slog.Logger) *Router {
```

This also removes the `MissLog` import from any code that only constructs a router (tests, etc).

2. **Expose miss metadata in `RouteResult`.** The server needs keyword data to write the miss log entry:

```go
type RouteResult struct {
    Pipe             string
    Confidence       float64
    Layer            int
    KeywordsFound    []string // populated at Layer 4 for miss logging
    KeywordsNotFound []string // populated at Layer 4 for miss logging
}
```

3. **At Layer 4, populate miss metadata on the result instead of logging.** The router returns the keywords on the result and lets the server handle logging:

```go
// Layer 4: deterministic fallback — return chat and let the server handle AI planning.
result := RouteResult{
    Pipe:             "chat",
    Confidence:       0.0,
    Layer:            LayerFallback,
    KeywordsFound:    keywordsFound,
    KeywordsNotFound: keywordsNotFound,
}
r.logger.Warn("miss", "signal", signal)
r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
return result
```

4. **`IsQuestion` guard stays unchanged.** Wh-questions that miss Layers 1-2 still fall through to the Layer 4 return, and the server handles AI planning regardless.

### Phase 3: Server Integration

This is the core architectural change. The server orchestrates AI planning, ack, and miss logging.

**Modify `internal/server/server.go`:**

Add fields to `Server` struct and `Deps`:

```go
type Deps struct {
    // ...existing fields...
    AIPlanner *planner.AIPlanner // nil if provider unavailable
    MissLog   *router.MissLog
    Store     *store.Store       // for planner memory context (may be nil in tests)
}

type Server struct {
    // ...existing fields...
    aiPlanner *planner.AIPlanner
    missLog   *router.MissLog
    store     *store.Store
}
```

**Extract `startAck` helper** to eliminate the duplicated ack goroutine pattern. The current code already has this pattern once (`api.go:133-145`); without extraction, this feature would create a third copy:

```go
// startAck launches the ack stream in a goroutine and returns a channel that
// closes when the ack completes. Returns a closed channel if no ack provider
// is configured, so callers can always <-ackDone without checking.
func (s *Server) startAck(ctx context.Context, signal string, mu *sync.Mutex, w http.ResponseWriter, flusher http.Flusher) <-chan struct{} {
    done := make(chan struct{}, 1)
    if s.ackProvider == nil {
        done <- struct{}{}
        return done
    }
    go func() {
        user := buildAckUserPrompt(signal, nil)
        _, err := s.ackProvider.CompleteStream(ctx, ackSystemPrompt, user, func(chunk string) {
            mu.Lock()
            w.Write([]byte(sse.FormatText(envelope.SSEEventAck, chunk)))
            flusher.Flush()
            mu.Unlock()
        })
        if err != nil {
            s.logger.Warn("ack failed", "error", err)
        }
        done <- struct{}{}
    }()
    return done
}
```

**Modify `internal/server/api.go` — `handleSSE`:**

The restructured flow is linear — no duplicated branches or execution tails. The only difference between Layer 4 and Layers 1-3 is *when* ack starts and *how* the plan is built:

```go
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, req signalRequest, parsed parser.ParsedSignal) {
    flusher, ok := sse.InitResponse(w)
    if !ok { ... }

    seed := buildSeed(req)
    route := s.router.Route(r.Context(), req.Text, parsed)  // Layers 1-3 only, <1ms

    var mu sync.Mutex
    layer4 := route.Layer == router.LayerFallback && s.aiPlanner != nil

    // Layer 4: start ack IMMEDIATELY — before AI planning.
    // The user sees feedback within milliseconds even though planning takes seconds.
    var ackDone <-chan struct{}
    if layer4 {
        ackDone = s.startAck(r.Context(), req.Text, &mu, w, flusher)
    }

    // Build the plan — AI planning for Layer 4, deterministic for Layers 1-3.
    var plan runtime.Plan
    if layer4 {
        // Fetch lightweight memory context for the planner (~1ms SQLite read).
        plannerMem := s.buildPlannerMemory()

        // AI planner runs concurrently with ack (ack is already streaming above).
        aiPlan, aiConf := s.aiPlanner.Plan(req.Text, plannerMem)
        if aiPlan != nil {
            plan = *aiPlan
            route.Pipe = plan.Steps[0].Pipe
            route.Confidence = aiConf
        } else {
            plan = s.planner.Plan(route, parsed)
        }

        // Log the miss with AI plan data.
        s.logMiss(route, req.Text, aiPlan, aiConf)
    } else {
        plan = s.planner.Plan(route, parsed)
    }

    // Send route event (now we know the actual pipe).
    if len(plan.Steps) > 0 {
        sse.WriteJSON(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": plan.Steps[0].Pipe, "layer": route.Layer})
    }

    // Layers 1-3: start ack after planning (existing behavior, planning was instant).
    if ackDone == nil {
        pipeIsAIBacked := len(plan.Steps) > 0 && s.config.Pipes[plan.Steps[0].Pipe].Prompts.System != ""
        if pipeIsAIBacked {
            ackDone = s.startAck(r.Context(), req.Text, &mu, w, flusher)
        }
    }

    // Prefetch memory, execute plan, wait for ack (unchanged from here)...
    // ...
    if ackDone != nil { <-ackDone }
    // ...
}
```

**`logMiss` helper** (on `Server`):

```go
func (s *Server) logMiss(route router.RouteResult, signal string, aiPlan *runtime.Plan, aiConf float64) {
    if s.missLog == nil {
        return
    }
    var planJSON string
    if aiPlan != nil {
        if data, err := json.Marshal(aiPlan.Steps); err == nil {
            planJSON = string(data)
        }
    }
    s.missLog.Log(router.MissEntry{
        Signal:           signal,
        KeywordsFound:    route.KeywordsFound,
        KeywordsNotFound: route.KeywordsNotFound,
        FallbackPipe:     route.Pipe,
        AIPlan:           planJSON,
        AIConfidence:     aiConf,
    })
}
```

This inlines the plan-to-JSON marshaling directly — no need for an exported `MarshalPlanForLog` helper for a one-liner `json.Marshal`.

**Modify `internal/server/api.go` — `handleSignal` (synchronous path):**

Same logic but simpler — no ack to worry about:

```go
route := s.router.Route(r.Context(), req.Text, parsed)
var plan runtime.Plan
if route.Layer == router.LayerFallback && s.aiPlanner != nil {
    plannerMem := s.buildPlannerMemory()
    aiPlan, aiConf := s.aiPlanner.Plan(req.Text, plannerMem)
    if aiPlan != nil {
        plan = *aiPlan
    } else {
        plan = s.planner.Plan(route, parsed)
    }
    s.logMiss(route, req.Text, aiPlan, aiConf)
} else {
    plan = s.planner.Plan(route, parsed)
}
```

**`buildPlannerMemory` helper** (on `Server`):

```go
func (s *Server) buildPlannerMemory() planner.PlannerMemory {
    var mem planner.PlannerMemory
    if s.store == nil {
        return mem
    }

    // Recent invocations: last 3 signal→pipe pairs for continuity.
    invocations, err := s.store.RecentInvocations(3)
    if err == nil {
        for _, inv := range invocations {
            mem.RecentSignals = append(mem.RecentSignals, planner.RecentSignal{
                Signal: inv.Signal,
                Pipe:   inv.Pipe,
            })
        }
    }

    // Working state: lightweight key-value context.
    states, err := s.store.ListState("planner")
    if err == nil {
        for _, st := range states {
            mem.WorkingState = append(mem.WorkingState, st.Key+": "+st.Content)
        }
    }

    return mem
}
```

This is a fast local read (~1ms). It does NOT block ack streaming — the ack fires first, then `buildPlannerMemory` + `Plan()` run.

### Phase 4: Miss Log Extension

Modify `internal/router/misslog.go`:

Add `AIPlan` and `AIConfidence` fields to `MissEntry`:

```go
type MissEntry struct {
    Signal           string   `json:"signal"`
    KeywordsFound    []string `json:"keywords_found"`
    KeywordsNotFound []string `json:"keywords_not_found"`
    FallbackPipe     string   `json:"fallback_pipe"`
    AIPlan           string   `json:"ai_plan,omitempty"`
    AIConfidence     float64  `json:"ai_confidence,omitempty"`
    Timestamp        string   `json:"timestamp"`
}
```

No separate `marshalPlanForLog` helper — the server's `logMiss` method inlines `json.Marshal(aiPlan.Steps)` directly.

### Phase 5: Config

Modify `internal/config/config.go`:

Rename `AckConfig` to `ModelEndpoint` — the planner config has the same shape (provider, model, max_tokens). One type, two uses:

```go
type ModelEndpoint struct {
    Provider  string `yaml:"provider"`
    Model     string `yaml:"model"`
    MaxTokens int    `yaml:"max_tokens"`
}

type Config struct {
    // ...existing fields...
    Ack     ModelEndpoint `yaml:"ack"`
    Planner ModelEndpoint `yaml:"planner"`
}
```

Set defaults in `Load()` after loading YAML (same pattern as ack defaults):

```go
if cfg.Planner.Provider == "" {
    cfg.Planner.Provider = "xai"
}
if cfg.Planner.Model == "" {
    cfg.Planner.Model = "grok-4-fast"
}
if cfg.Planner.MaxTokens == 0 {
    cfg.Planner.MaxTokens = 1024
}
```

The `virgil.yaml` config block:

```yaml
planner:
  provider: xai
  model: grok-4-fast
  max_tokens: 1024
```

Users can override to any provider/model combination (e.g., `anthropic` / `claude-haiku-4-5-20251001`, `gemini` / `gemini-3.1-flash-preview`).

### Phase 6: Wiring

Modify `cmd/virgil/main.go` in `runServer()`:

After building the pipe registry, create the AI planner and pass it to the server (not the router):

```go
// Build AI planner for Layer 4 routing
var aiPlanner *planner.AIPlanner
plannerProvider, err := bridge.CreateProvider(bridge.ProviderConfig{
    Name:      cfg.Planner.Provider,
    Model:     cfg.Planner.Model,
    MaxTokens: cfg.Planner.MaxTokens,
    Logger:    logger,
})
if err != nil {
    logger.Warn("AI planner unavailable", "provider", cfg.Planner.Provider, "error", err)
} else {
    aiPlanner = planner.NewAIPlanner(plannerProvider, reg.Definitions(), logger)
}

// Router no longer takes missLog — server owns it
rt := router.NewRouter(reg.Definitions(), logger)

// AI planner, miss log, and store go to the server
srv := server.New(server.Deps{
    Config:    cfg,
    Router:    rt,
    Parser:    p,
    Planner:   pl,
    Runtime:   run,
    Registry:  reg,
    AIPlanner: aiPlanner,
    MissLog:   missLog,
    Store:     st,
    Logger:    logger,
})
```

If the configured provider's API key is not set, `aiPlanner` is nil, and the server skips AI planning — the router's chat fallback is used directly. No hard dependency.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Rename AckConfig to ModelEndpoint

- In `internal/config/config.go`, rename `AckConfig` to `ModelEndpoint`
- Update `Config.Ack` field type to `ModelEndpoint`
- Add `Planner ModelEndpoint` field to `Config` struct
- In `Load()`, add planner defaults after YAML load: provider `"xai"`, model `"grok-4-fast"`, max_tokens `1024`
- Update any references to `AckConfig` in the codebase (server.go ack provider construction uses `cfg.Ack.*` — field access unchanged)

### 2. Create AI Planner

- Create `internal/planner/aiplanner.go`
- Implement `AIPlanner` struct with `provider`, `catalogue`, `pipeNames`, `logger` fields (no `pipeFlags` — flags pass through without validation)
- Implement `NewAIPlanner(provider bridge.Provider, defs []pipe.Definition, logger *slog.Logger) *AIPlanner` constructor that builds the catalogue string and pipe name set from definitions (excluding chat)
- Define `PlannerMemory` and `RecentSignal` types for lightweight memory context
- Implement `Plan(signal string, memory PlannerMemory) (*runtime.Plan, float64)` method — builds user message from signal + memory context, 5-second timeout, JSON parsing, pipe name validation, fallback on any error
- Build flag schema from `pipe.Definition.Flags` map — include name, values (if enumerated), and default

### 3. Update RouteResult and Router

- In `internal/router/router.go`, remove `missLog` field from `Router` struct
- Simplify `NewRouter` signature: `NewRouter(defs []pipe.Definition, logger *slog.Logger)` — no miss log parameter
- Add `KeywordsFound []string` and `KeywordsNotFound []string` fields to `RouteResult` struct
- At Layer 4 in `Route()`, populate these fields on the result instead of calling `missLog.Log()` — remove the `missLog.Log()` call entirely
- Update all `NewRouter` call sites (main.go, tests) to drop the miss log argument

### 4. Update Miss Log

- In `internal/router/misslog.go`, add `AIPlan string` and `AIConfidence float64` fields to `MissEntry`

### 5. Update Server

- In `internal/server/server.go`, add `aiPlanner *planner.AIPlanner`, `missLog *router.MissLog`, and `store *store.Store` fields to `Server` struct and `Deps`
- Extract `startAck` helper method on `Server` — replaces the inline ack goroutine in the existing `handleSSE` code and is reused for both Layer 4 and Layers 1-3 paths
- Add `buildPlannerMemory()` helper on `Server` that reads recent invocations (last 3) and working state from the store
- Add `logMiss()` helper on `Server` that writes a miss entry with AI plan data (inlines `json.Marshal` for the plan)
- In `internal/server/api.go`, restructure `handleSSE` as a linear flow: route → (Layer 4: early ack + AI plan) → (Layers 1-3: deterministic plan) → route event → (Layers 1-3: late ack) → prefetch → execute → done
- In `internal/server/api.go`, update `handleSignal` (sync path): when Layer 4 fires, build planner memory, call AI planner, use AI plan or fall back to deterministic planner, log miss

### 6. Wire in main.go

- In `cmd/virgil/main.go`, create provider from `cfg.Planner` config via `bridge.CreateProvider`
- Create `planner.NewAIPlanner(plannerProvider, reg.Definitions(), logger)` — nil if provider creation fails
- Update `router.NewRouter` call to drop the `missLog` argument
- Pass AI planner, miss log, and store to `server.New()` via `Deps`

### 7. Update Existing Tests

- In `internal/router/router_test.go`, update all `NewRouter` calls to drop the miss log argument
- Verify Layer 4 tests still return chat fallback with miss metadata (`KeywordsFound`/`KeywordsNotFound`) populated on the result
- Verify all existing Layer 1-3 tests pass unchanged
- Update any config tests that reference `AckConfig` to use `ModelEndpoint`

### 8. Write AI Planner Tests

- Create `internal/planner/aiplanner_test.go`
- `TestCatalogueBuild` — Verify catalogue string is built correctly from definitions, excluding chat, including flag schemas
- `TestPlanSinglePipe` — Mock provider returns `{"pipe": "calendar", "flags": {"action": "list"}}` → planner returns single-step plan with correct pipe and flags
- `TestPlanMultiStep` — Mock provider returns `{"steps": [{"pipe": "study", "flags": {"source": "codebase"}}, {"pipe": "chat", "flags": {}}]}` → planner returns two-step plan
- `TestPlanChatOnlyReturnsNil` — Mock provider returns `{"pipe": "chat"}` → planner returns nil (let fallback handle it)
- `TestPlanUnknownPipeReturnsNil` — Mock provider returns `{"pipe": "nonexistent"}` → planner returns nil
- `TestPlanProviderError` — Mock provider returns error → planner returns nil, no panic
- `TestPlanInvalidJSON` — Mock provider returns garbage → planner returns nil
- `TestPlanTimeout` — Mock provider blocks > 5s → planner returns nil (context cancelled)
- `TestPlanWithMemoryContext` — Pass `PlannerMemory` with recent signals → verify user message includes history section
- `TestPlanEmptyMemory` — Pass empty `PlannerMemory` → verify user message is just the signal text

### 9. Write Server Integration Tests

- `TestLayer4AIPlanner` — Set up server with a mock AI planner. Send an unmatched signal. Verify the AI planner was called and its plan was executed.
- `TestLayer4FallbackAfterAIFailure` — Set up server with a mock AI planner that returns nil. Verify the deterministic planner's chat fallback was used.
- `TestLayer4AckStartsBeforePlanning` — Set up server with a slow mock AI planner (e.g., 500ms delay) and a mock ack provider. Send an SSE request with an unmatched signal. Verify ack chunks arrive before the AI planner returns.
- `TestLayer4MissLogIncludesAIPlan` — Verify miss log entry contains the AI plan JSON and confidence.
- `TestLayer4MissMetadataPopulated` — Verify `RouteResult.KeywordsFound` and `KeywordsNotFound` are populated for Layer 4 results.
- `TestLayer4PlannerReceivesMemory` — Set up server with a store containing recent invocations. Send an unmatched signal. Verify the AI planner received memory context (recent signals and working state).

## Testing Strategy

### Unit Tests

- **AI Planner** (`aiplanner_test.go`): Catalogue construction, JSON parsing for single/multi-step, validation (unknown pipes, chat-only, invalid JSON, provider errors, timeouts). Use a mock provider that returns canned responses.
- **Router** (`router_test.go`): Verify Layer 4 populates `KeywordsFound`/`KeywordsNotFound` on the result. Verify `NewRouter` works without miss log. Verify existing Layer 1-3 behavior unchanged.
- **Server** (`api_test.go` or similar): Layer 4 flow with mock AI planner — ack starts before planning via `startAck`, AI plan used when available, deterministic fallback when AI fails, miss log populated with AI plan data.

### Edge Cases

- Configured provider's API key not set → AI planner is nil → stub fallback behavior (chat) is preserved
- AI returns a plan where the first pipe is chat and there are subsequent steps (valid — chat might synthesize upstream context)
- AI returns flags for a pipe that doesn't accept those flags → pass through, pipes ignore unknown flags
- AI returns empty flags object → valid, treated as no flags
- Signal is empty string → should not reach Layer 4 (server validates)
- Provider returns valid JSON that isn't a plan object (e.g., `{"error": "rate limited"}`) → treated as invalid, returns nil
- Concurrent Layer 4 calls (AI planner must be safe for concurrent use — no mutable state after init)
- Store is nil (e.g., in tests) → `buildPlannerMemory()` returns empty `PlannerMemory` → planner works without memory context
- Store has no invocations yet (fresh install) → empty memory, planner operates on signal alone
- Follow-up signal like "what about tomorrow?" with recent calendar invocation → planner sees history, routes to calendar

## Risk Assessment

**Latency.** Layer 4 adds up to 5 seconds of AI planning time. The user doesn't feel this as dead time because the ack starts streaming immediately when Layer 4 fires — the user sees "On it — looking into what pipes are available..." while the planner thinks. The 5s timeout is a ceiling; Grok 4 Fast typically responds in 1-2s.

**No circular imports.** The AI planner lives in `internal/planner/` and imports `internal/runtime` for `Plan`/`Step` types and `internal/bridge` for `Provider`. The `planner` package already imports `router` for `RouteResult` (deterministic planner). `runtime` does not import `planner` or `router`. No circular dependency.

**No regression for existing routes.** The router's `Route()` method and Layers 1-3 are completely unchanged. `NewRouter` loses the `missLog` parameter — a minor signature change that simplifies call sites. Existing tests need the argument dropped but behavior is identical.

**Server complexity.** `handleSSE` is restructured as a linear flow with a `layer4` boolean controlling two specific decisions: (1) when to start ack (before or after planning), and (2) how to build the plan (AI or deterministic). The extracted `startAck` helper eliminates the duplicated ack goroutine pattern. The `logMiss` helper keeps miss logging out of the main flow.

**Wh-questions.** Wh-questions that miss Layers 1-2 still fall through to Layer 4 as they do today. The server's AI planner handles them — the AI can correctly interpret "when is my next meeting?" → calendar. The `IsQuestion` guard on Layer 3 is unchanged.

**Memory context overhead.** The planner's memory snapshot adds ~1ms (local SQLite read of 3 recent invocations + working state). The memory section adds ~100-200 tokens to the planner's user message. This is negligible compared to the 1-5s AI call. The planner can still compose `study` steps when it needs deeper context (e.g., full codebase scan) — the lightweight memory just handles the common case of follow-up disambiguation.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test    # all tests pass including new aiplanner_test.go and updated router_test.go
just build   # compiles cleanly (no circular imports)
just lint    # no lint violations
```

Manual verification:
```bash
# Layer 4 should compose study → chat for codebase-reflective question
virgil "what pipe should we build next for virgil?"
# Expected: study(source=codebase, role=planner) → chat. Response should reference actual pipes.

# Layer 4 should route calendar question correctly
virgil "when is my next meeting?"
# Expected: calendar(action=list, range=today) — single pipe, correct flags.

# Conversational signals should still go to chat
virgil "tell me a joke"
# Expected: chat (AI returns chat-only → nil → fallback).

# Layers 1-3 should be unaffected
virgil "check my calendar"
# Expected: Layer 1 exact match → calendar. AI planner never fires.
```

## Resolved Questions

1. **Flag validation strictness.** Pass all flags through without stripping. Pipes already ignore unknown flags — stripping would require keeping schemas perfectly in sync for zero safety gain. No `pipeFlags` validation map on the AIPlanner struct.

2. **Max response tokens.** Keep `MaxTokens: 1024`. You only pay for tokens actually generated, not the ceiling. A multi-step pipeline with verbose flag values could hit 300-400 tokens; lowering risks truncation for no savings.

3. **Ack content for Layer 4.** Generic ack is sufficient. The ack's purpose is latency masking, not information delivery. If more specificity is desired later, a lightweight "plan" SSE event could be sent after the planner returns.

4. **Memory context for the planner.** The AI planner receives a lightweight memory snapshot — recent invocations (last 3 signal→pipe pairs) and working state entries — so it can reason about conversation continuity. For example, if the user just asked about calendar and now says "what about tomorrow?", the planner sees the recent calendar invocation and routes correctly. The server fetches this context (fast, local SQLite) before calling the planner. The planner can still compose a `study` step when it decides deeper context is needed (e.g., codebase questions). This keeps the planner's own context window small (~500 tokens of memory) while giving it enough to handle follow-ups.

5. **JSON mode via provider SDK.** The OpenAI SDK supports `response_format: json_object` and xAI's API is compatible. This would guarantee valid JSON at the API level instead of relying on prompt instructions. However, it requires extending the `bridge.Provider` interface (e.g., `CompleteJSON` method or options parameter), which touches every provider. Deferred as a follow-up — the prompt instruction works reliably with modern models, and the error path handles failures gracefully. If more structured-output use cases emerge, add JSON mode to the bridge abstraction then.

## Sub-Tasks

Single task — no decomposition needed. The phases are sequential but tightly coupled (planner → router → server → wiring) and share types/interfaces across a small set of files.
