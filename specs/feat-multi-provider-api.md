# Feature: Multi-Provider API Infrastructure

## Metadata

type: `feat`
task_id: `multi-provider-api`
prompt: `Build out infrastructure for hitting Anthropic, Google Gemini, OpenAI, and xAI APIs. Each pipe specifies provider and model in its pipe.yaml. Clean abstraction that supports streaming and is easy to extend with new providers.`

## Feature Description

The bridge layer currently has a single provider implementation — `ClaudeProvider` — which shells out to the Claude CLI binary. This works but is slow (subprocess overhead per call), limited to one vendor, and depends on an external binary being installed.

This feature adds direct HTTP API clients for four providers: Anthropic, OpenAI, Google Gemini, and xAI. Each provider implements the existing `Provider` and `StreamingProvider` interfaces. The factory gains the ability to construct any of five providers (four new API providers plus the existing CLI provider as a fallback).

Pipes gain a `provider` field in their `pipe.yaml` alongside the existing `model` field. When a pipe declares `provider: openai` and `model: gpt-4o`, it gets an OpenAI client pointed at that model. When a pipe omits `provider`, it inherits the global default from `virgil.yaml`.

xAI's Grok API is OpenAI-compatible (same request/response format, different base URL), so both share a single implementation with a configurable endpoint. Adding future OpenAI-compatible providers (Groq, Together, etc.) is just a new base URL and key env var in the factory.

Each provider reads its own API key directly from the standard vendor environment variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `XAI_API_KEY`) inside its constructor. The server doesn't resolve or proxy keys — pipe subprocesses already inherit the full parent environment via `os.Environ()`. No keys in config files.

Provider structs are unexported (implementation details behind the interface). Constructors use the type name directly: `AnthropicProvider(cfg)`, not `NewAnthropicProvider(cfg)`.

## User Story

As a Virgil pipe author
I want to specify which provider and model a pipe uses in its pipe.yaml
So that different pipes can use different vendors based on cost, speed, and capability tradeoffs

## Relevant Files

### Existing Files (Modified)

- `internal/bridge/bridge.go` — `ProviderConfig` struct (add `MaxTokens` field), factory (rename to `CreateProvider`, add cases for anthropic, openai, gemini, xai). Rename existing `NewProvider` → `CreateProvider`.
- `internal/bridge/claude.go` — Rename `ClaudeProvider` → `claudeProvider` (unexported), `NewClaudeProvider` → `ClaudeProvider` (constructor).
- `internal/bridge/bridge_test.go` — Add factory tests for new provider names, update mock to implement `StreamingProvider`.
- `internal/config/config.go` — `PipeConfig`: add `Provider` and `MaxTokens` fields, add `EffectiveProvider()` and `EffectiveMaxTokens()` methods. `ProviderConfig`: add `MaxTokens` field.
- `internal/config/config_test.go` — Test `EffectiveProvider` and `EffectiveMaxTokens` resolution.
- `internal/pipehost/host.go` — Add `EnvMaxTokens` constant. Update `BuildProviderFromEnvWithLogger()` to read it.
- `cmd/virgil/main.go` — Update pipe env to pass resolved provider name and max_tokens per-pipe.

### New Files

- `internal/bridge/anthropic.go` — Anthropic Messages API client implementing `StreamingProvider`. Direct HTTP with SSE streaming.
- `internal/bridge/anthropic_test.go` — Request construction, streaming, error handling tests.
- `internal/bridge/openai.go` — OpenAI Chat Completions API client implementing `StreamingProvider`. Configurable base URL (shared with xAI).
- `internal/bridge/openai_test.go` — Request construction, streaming, base URL override, error handling tests.
- `internal/bridge/gemini.go` — Google Gemini API client implementing `StreamingProvider`. Direct HTTP with SSE streaming.
- `internal/bridge/gemini_test.go` — Request construction, streaming, error handling tests.

## Implementation Plan

### Phase 1: Config Extension

Extend `PipeConfig` and `ProviderConfig` to carry provider name and max_tokens per-pipe. Update environment variable propagation. Small, mechanical changes.

### Phase 2: Anthropic Provider

Implement the Anthropic Messages API client. POST to `https://api.anthropic.com/v1/messages` with `stream: true`. Parse SSE events of type `content_block_delta` to extract text chunks. Handle `message_stop` as completion.

### Phase 3: OpenAI-Compatible Provider

Implement the OpenAI Chat Completions API client with a configurable base URL. Default `https://api.openai.com/v1` for OpenAI, `https://api.x.ai/v1` for xAI. POST to `/chat/completions` with `stream: true`. Parse SSE `delta.content` chunks. Factory registers both "openai" and "xai" using the same struct with different base URLs and key env vars.

### Phase 4: Gemini Provider

Implement the Google Gemini API client. POST to `https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse&key={key}`. Parse SSE events containing `candidates[0].content.parts[0].text`.

### Phase 5: Factory Wiring and Validation

Wire all providers into the factory. Rename existing constructor pattern. Update virgil.yaml default. Run full test suite.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Config and Environment Changes

- In `internal/config/config.go` `PipeConfig`: add `Provider string \`yaml:"provider"\`` and `MaxTokens *int \`yaml:"max_tokens"\`` fields.
- Add `func (pc PipeConfig) EffectiveProvider(globalDefault string) string` — returns `pc.Provider` if set, else `globalDefault`.
- Add `func (pc PipeConfig) EffectiveMaxTokens(globalDefault int) int` — returns `*pc.MaxTokens` if set, else `globalDefault`.
- In `internal/config/config.go` `ProviderConfig`: add `MaxTokens int \`yaml:"max_tokens"\`` field.
- In `internal/bridge/bridge.go` `ProviderConfig`: add `MaxTokens int \`yaml:"-" json:"-"\`` field.
- In `internal/pipehost/host.go`: add `EnvMaxTokens = "VIRGIL_MAX_TOKENS"` constant. Update `BuildProviderFromEnvWithLogger()` to read `EnvMaxTokens` from env and set on `bridge.ProviderConfig`.
- In `cmd/virgil/main.go`: where pipe env is built (around line 188), resolve `pc.EffectiveProvider(cfg.Provider.Name)` and set `VIRGIL_PROVIDER` per-pipe. Resolve `pc.EffectiveMaxTokens(cfg.Provider.MaxTokens)` and set `VIRGIL_MAX_TOKENS`.
- In `internal/config/config_test.go`: add tests for `EffectiveProvider` and `EffectiveMaxTokens`.
- In `internal/config/config.go` `Load()`: set default `MaxTokens: 8192` on the `ProviderConfig` default.

### 2. Rename Existing Constructor Pattern

- In `internal/bridge/claude.go`: rename `ClaudeProvider` struct → `claudeProvider` (unexported). Rename `NewClaudeProvider` → `ClaudeProvider`.
- In `internal/bridge/bridge.go`: rename `NewProvider` → `CreateProvider`. Update the `"claude"` case to call `ClaudeProvider(config)`.
- Update all call sites: `cmd/virgil/main.go` (classifier provider creation), `internal/pipehost/host.go` (`BuildProviderFromEnvWithLogger`), `internal/bridge/bridge_test.go`.

### 3. Anthropic Provider

- Create `internal/bridge/anthropic.go`:
  - `type anthropicProvider struct { apiKey, model, baseURL string; maxTokens int; logger *slog.Logger }`
  - `func AnthropicProvider(cfg ProviderConfig) (*anthropicProvider, error)` — reads `ANTHROPIC_API_KEY` from `os.Getenv`. Returns error if missing. Default baseURL `https://api.anthropic.com`, default model `claude-sonnet-4-20250514`.
  - `Complete()` — POST `/v1/messages`, body: `{"model": model, "max_tokens": maxTokens, "system": system, "messages": [{"role": "user", "content": user}]}`. Headers: `x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`. Parse response JSON `.content[0].text`.
  - `CompleteStream()` — Same request with `"stream": true`. Read SSE with `bufio.Scanner`. Parse each `data:` line as JSON: look for `"type":"content_block_delta"` → extract `.delta.text`. Call `onChunk`. Accumulate full text. Return on `message_stop` event or `data: [DONE]`.
- Create `internal/bridge/anthropic_test.go`:
  - Test request body construction (system prompt placement, model, headers).
  - Test streaming response parsing for `content_block_delta` and `message_stop`.
  - Test error responses (401 auth, 429 rate limit, 400 bad request).
  - Use `httptest.NewServer` for integration-style tests.

### 4. OpenAI-Compatible Provider

- Create `internal/bridge/openai.go`:
  - `type openaiProvider struct { apiKey, model, baseURL string; maxTokens int; logger *slog.Logger }`
  - `func OpenAIProvider(cfg ProviderConfig, baseURL string, keyEnvVar string) (*openaiProvider, error)` — reads API key from `os.Getenv(keyEnvVar)`. Returns error if missing. Default model `gpt-4o`.
  - `Complete()` — POST `{baseURL}/chat/completions`, body: `{"model": model, "max_tokens": maxTokens, "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}]}`. Headers: `Authorization: Bearer {key}`, `content-type: application/json`. Parse `.choices[0].message.content`.
  - `CompleteStream()` — Same with `"stream": true`. Read SSE with `bufio.Scanner`. Parse each `data:` line (skip `[DONE]`) as JSON: extract `.choices[0].delta.content`. Call `onChunk` per delta.
- Create `internal/bridge/openai_test.go`:
  - Test request body (system as message, auth header format).
  - Test base URL override (xAI uses same client, different URL).
  - Test SSE delta parsing and accumulation.
  - Test error responses.

### 5. Gemini Provider

- Create `internal/bridge/gemini.go`:
  - `type geminiProvider struct { apiKey, model, baseURL string; maxTokens int; logger *slog.Logger }`
  - `func GeminiProvider(cfg ProviderConfig) (*geminiProvider, error)` — reads `GEMINI_API_KEY` from `os.Getenv`. Returns error if missing. Default baseURL `https://generativelanguage.googleapis.com/v1beta`, default model `gemini-2.0-flash`.
  - `Complete()` — POST `/models/{model}:generateContent?key={key}`, body: `{"system_instruction": {"parts": [{"text": system}]}, "contents": [{"parts": [{"text": user}]}], "generationConfig": {"maxOutputTokens": maxTokens}}`. Parse `.candidates[0].content.parts[0].text`.
  - `CompleteStream()` — POST `/models/{model}:streamGenerateContent?alt=sse&key={key}`. Same body. Read SSE with `bufio.Scanner`. Parse each `data:` line as JSON: extract `.candidates[0].content.parts[0].text`. Call `onChunk`. Accumulate.
- Create `internal/bridge/gemini_test.go`:
  - Test URL construction with model and key.
  - Test system instruction placement.
  - Test SSE streaming response parsing.
  - Test error responses.

### 6. Factory Wiring

- In `internal/bridge/bridge.go` `CreateProvider()`: add cases:
  - `"anthropic"` → `AnthropicProvider(config)`
  - `"openai"` → `OpenAIProvider(config, "https://api.openai.com/v1", "OPENAI_API_KEY")`
  - `"xai"` → `OpenAIProvider(config, "https://api.x.ai/v1", "XAI_API_KEY")`
  - `"gemini"` → `GeminiProvider(config)`
  - `"claude"` → `ClaudeProvider(config)` (existing, no API key needed)
- In `internal/bridge/bridge_test.go`: add factory tests for each provider name. Test that unknown providers still error.

### 7. Validate End-to-End

- Update `config/virgil.yaml`: change default provider to `anthropic`, add `max_tokens: 8192`, add comment listing available provider names.
- Run `just test` — all existing tests plus new tests must pass.
- Run `just build` — verify compilation with new files.

## Testing Strategy

### Unit Tests

- `internal/bridge/anthropic_test.go` — Request construction, SSE parsing, error mapping (use httptest.NewServer)
- `internal/bridge/openai_test.go` — Request construction, SSE parsing, base URL override, error mapping
- `internal/bridge/gemini_test.go` — URL construction with model/key, SSE parsing, error mapping
- `internal/bridge/bridge_test.go` — Factory tests for all provider names
- `internal/config/config_test.go` — `EffectiveProvider()` and `EffectiveMaxTokens()` resolution

### Edge Cases

- Missing API key: provider constructor returns clear error ("ANTHROPIC_API_KEY not set")
- Empty response body: providers return descriptive error, not panic
- Partial SSE stream (connection drops mid-stream): return accumulated text + error
- Rate limit (429): surface retry-after header value in error message
- Context cancellation: providers respect `ctx.Done()` and abort HTTP requests
- Model field empty: each provider uses its own sensible default
- Max tokens: providers use the resolved value from config (global default 8192, pipe override if set)

## Risk Assessment

- **Existing CLI provider untouched** — the `claude` provider continues to work exactly as before. No regression risk for current users.
- **No new dependencies** — all providers use `net/http` and `encoding/json` from stdlib. No SDK bloat.
- **API key exposure** — keys live in env vars only, never written to disk or logs. Provider log methods must never log API keys.
- **Subprocess protocol unchanged** — pipes still receive config via env vars, just with one new var (`VIRGIL_MAX_TOKENS`). Existing pipes that don't set `provider` in their yaml continue working unchanged.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```
just test
just build
just lint
```

## Decisions (Resolved)

1. **Default provider** — Switch to `anthropic` (API) as the global default. Faster than CLI subprocess, no binary dependency. `claude` (CLI) remains available as a fallback provider name.

2. **Max tokens** — Global default in `virgil.yaml` (`max_tokens` field on provider config, default 8192). Pipes can override via `max_tokens` in their `pipe.yaml`. Follows the same resolution pattern as `model` and `provider`: `PipeConfig.EffectiveMaxTokens(globalDefault)`.

3. **Retry logic** — No retries. Errors surface directly to the user. Runtime timeout handling is sufficient. Retry logic can be added later if needed.

## Sub-Tasks

Single task — no decomposition needed. The phases are sequential and each builds on the previous. Total scope is 6 new files plus modifications to 7 existing files.
