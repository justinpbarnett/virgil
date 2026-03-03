# Feature: Layer 4 AI Intent Routing

## Metadata

type: `feat`
task_id: `layer4-ai-routing`
prompt: `Replace the current Layer 4 stub fallback (unconditional chat) with a lightweight AI interpretation step. When deterministic Layers 1-3 fail to produce a confident route, invoke Haiku to read the signal, consider the available pipes, and select the correct one. Log the AI decision alongside the miss so the deterministic layers can learn from it.`

## Feature Description

Layer 4 is currently a stub: if Layers 1-3 produce no confident match, the router returns `{pipe: "chat", confidence: 0.0}` unconditionally. This means every ambiguous signal — no matter how clearly it maps to a real pipe — gets dumped into chat. The miss log captures what happened, but nothing acts on it in the moment.

This feature replaces the stub with a Haiku-powered intent classifier. The router sends the raw signal and a catalogue of available pipes (name + description) to Haiku. Haiku returns a pipe name. If the AI picks a pipe that exists, the router returns it at Layer 4. If the AI picks nothing or fails, the router returns "chat" as before.

The key constraint: the AI is a fallback, not a crutch. Layers 1-3 remain the primary routing path. Layer 4 fires only when deterministic methods fail. Every Layer 4 hit is still logged as a miss — the AI decision is appended to the miss entry so that recurring patterns can be promoted into Layer 2 keywords or Layer 1 exact matches over time. The goal is for Layer 4 to fire less often the more the system is used, not more.

The model is hardcoded to Haiku. This is a classification task — small, fast, cheap. No configuration knob. The router should not inherit the user's default model (sonnet/opus would be wasteful here).

## User Story

As a Virgil user
I want ambiguous signals to be intelligently routed to the correct pipe instead of falling back to chat
So that I can speak naturally without worrying about hitting exact keywords

## Relevant Files

### Existing Files (Modified)

- `internal/router/router.go` — `Route()` method: replace Layer 4 stub with AI classification call. `Router` struct: add classifier field. Adjust `IsQuestion` guard to skip Layer 3 only (not Layer 4).
- `internal/router/misslog.go` — `MissEntry` struct: add `AIChoice` and `AIConfidence` fields to capture Layer 4 decisions.
- `cmd/virgil/main.go` — `runServer()`: construct the Haiku provider and classifier, pass to `NewRouter()`.

### Existing Files (Reference)

- `internal/bridge/bridge.go` — `Provider` interface for AI completion.
- `internal/bridge/claude.go` — `ClaudeProvider` used as the concrete provider for classification.
- `internal/pipe/pipe.go` — `Definition` struct with `Name`, `Description`, `Category` fields used to build the pipe catalogue prompt.
- `internal/config/config.go` — `ProviderConfig` for constructing the Haiku provider.
- `internal/parser/parser.go` — `ParsedSignal` struct (not used by classifier, but flows through the router).
- `internal/server/server.go` — No changes. Router is already injected via `Deps`.

### New Files

- `internal/router/classifier.go` — Encapsulates the AI classification logic: prompt construction, response parsing, timeout handling.
- `internal/router/classifier_test.go` — Tests for prompt construction, response parsing, error handling, and fallback behavior.

## Implementation Plan

### Phase 1: Classifier

Create `internal/router/classifier.go` with a `Classifier` struct that owns the AI classification concern.

**Classifier struct:**

```go
type Classifier struct {
    provider  bridge.Provider
    catalogue string           // pre-built pipe catalogue (name + description, one per line)
    pipeNames map[string]bool  // valid pipe names for response validation
    logger    *slog.Logger
}
```

**Constructor** takes `bridge.Provider`, `[]pipe.Definition`, and `*slog.Logger`. Builds the catalogue string once at init time from the definitions — one line per pipe: `"- {name}: {description}"`. Excludes the `chat` pipe from the catalogue (it's the implicit fallback). Builds `pipeNames` set for O(1) response validation.

**`Classify(signal string) (pipe string, confidence float64)`** method:

1. System prompt (static, defined as a const):
   ```
   You are a signal router. Given a user's signal and a list of available pipes,
   respond with ONLY the pipe name that best matches the user's intent.
   If no pipe is a good match, respond with "chat".

   Available pipes:
   {catalogue}
   ```

2. Call `provider.Complete(ctx, system, signal)` with a 5-second internal timeout.

3. Parse the response: trim whitespace, lowercase, check against `pipeNames`. If the response matches a known pipe name, return it with confidence 0.7. If the response is "chat" or unrecognized, return `"chat", 0.0`.

4. If the provider call fails (error, timeout), log the error and return `"chat", 0.0`. Layer 4 must never block the response.

The classifier owns its own timeout (5 seconds). The caller doesn't need to manage contexts — the classifier creates a child context internally from `context.Background()`.

The method takes only the raw signal string. The AI reads English — it doesn't need parsed components.

### Phase 2: Router + Miss Log Integration

Modify `internal/router/router.go`:

1. Add `classifier *Classifier` field to `Router` struct.

2. Change `NewRouter` signature to accept an optional `*Classifier` (nil means no AI — stub fallback behavior is preserved for tests and environments without a provider).

```go
func NewRouter(defs []pipe.Definition, missLog *MissLog, classifier *Classifier, logger *slog.Logger) *Router
```

3. **Adjust the `IsQuestion` guard.** The current implementation returns chat immediately for wh-questions, bypassing everything after Layer 2. This should change: `IsQuestion` should skip Layer 3 only (the dumb verb/source match that caused the original misroute), then fall through to Layer 4 where the AI can correctly interpret questions like "when is my next meeting?" → calendar.

```go
// After Layer 2...

// Wh-questions skip Layer 3: the verb/source match is too aggressive
// for questions. But they still reach Layer 4 where the AI can
// correctly interpret intent.
if !parsed.IsQuestion {
    // Layer 3: Category narrowing
    ...
    // Direct verb/source match
    ...
}

// Layer 4: AI classification
if r.classifier != nil {
    aiPipe, aiConf := r.classifier.Classify(signal)
    if aiPipe != "chat" {
        result := RouteResult{Pipe: aiPipe, Confidence: aiConf, Layer: LayerFallback}
        r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
        // Log as miss with AI decision so deterministic layers can learn
        if r.missLog != nil {
            r.missLog.Log(MissEntry{
                Signal:       signal,
                ...
                AIChoice:     aiPipe,
                AIConfidence: aiConf,
            })
        }
        return result
    }
}

// Fallback: chat
...
```

No new layer constants. `LayerFallback = 4` is used for both AI-routed and chat-fallback results. The `Confidence` field distinguishes them: 0.7 means the AI picked it, 0.0 means nothing worked.

4. Update `internal/router/misslog.go` — add fields to `MissEntry`:

```go
type MissEntry struct {
    Signal           string   `json:"signal"`
    KeywordsFound    []string `json:"keywords_found"`
    KeywordsNotFound []string `json:"keywords_not_found"`
    FallbackPipe     string   `json:"fallback_pipe"`
    AIChoice         string   `json:"ai_choice,omitempty"`
    AIConfidence     float64  `json:"ai_confidence,omitempty"`
    Timestamp        string   `json:"timestamp"`
}
```

Populate `AIChoice` and `AIConfidence` for every Layer 4 hit — whether the AI succeeded or fell through to chat. This creates a complete record for promoting recurring AI routes into Layer 2 keywords.

### Phase 3: Wiring + Tests

**Wiring in `cmd/virgil/main.go`:**

After building the pipe registry and before constructing the router, create the Haiku provider and classifier:

```go
classifierProvider, err := bridge.NewProvider(bridge.ProviderConfig{
    Name:     cfg.Provider.Name,
    Model:    "haiku",
    Binary:   cfg.Provider.Binary,
    MaxTurns: 1,
    Logger:   logger,
})
classifier := router.NewClassifier(classifierProvider, reg.Definitions(), logger)
rt := router.NewRouter(reg.Definitions(), missLog, classifier, logger)
```

**`internal/router/classifier_test.go`:**

- `TestClassifyBuildsCatalogue` — Verify the catalogue string is built correctly from definitions, excluding chat.
- `TestClassifyMatchesPipe` — Mock provider returns a valid pipe name → classifier returns that pipe with confidence 0.7.
- `TestClassifyReturnsChat` — Mock provider returns "chat" → classifier returns chat with confidence 0.0.
- `TestClassifyUnrecognizedResponse` — Mock provider returns garbage → classifier returns chat.
- `TestClassifyProviderError` — Mock provider returns error → classifier returns chat, no panic.

**Update `internal/router/router_test.go`:**

- Update `NewRouter` calls to pass `nil` for classifier (preserves existing behavior).
- `TestLayer4AIRouting` — Create router with a mock classifier that returns "draft". Send unmatched signal. Verify route returns "draft" at `LayerFallback` with confidence 0.7.
- `TestLayer4FallbackAfterAI` — Create router with a mock classifier that returns "chat". Verify route returns "chat" at `LayerFallback` with confidence 0.0.
- `TestWhQuestionReachesAI` — Create router with a mock classifier. Send a wh-question that would have been caught by Layer 3's verb match. Verify the classifier was called (question skipped Layer 3 but reached Layer 4).

## Testing Strategy

- Unit tests for classifier: prompt construction, response parsing, error fallback.
- Unit tests for router: Layer 4 integration with mock classifier, fallback behavior, IsQuestion flow.
- Existing router tests pass unchanged (classifier is nil, behavior defaults to current fallback).

## Validation

```bash
just test                    # all tests pass
just build                   # compiles cleanly
```

Manually verify:
```bash
virgil "what's a complicated workflow that would be cool to visualize?"
# AI should route to chat (it's a question about ideas, not a command)

virgil "when is my next meeting?"
# Keyword scoring misses (1/5 = 0.2). IsQuestion skips Layer 3. AI routes to calendar.

virgil "summarize my recent jira tickets"
# Layer 2 keywords miss. Layer 4 AI should route to jira.
```
