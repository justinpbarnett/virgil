# Feature: Zen AI Provider

## Metadata

type: `feat`
task_id: `zen-provider`
prompt: `add opencode's zen as a provider for ai models`

## Feature Description

Add [OpenCode Zen](https://opencode.ai/docs/zen) as an AI provider in the bridge layer. Zen is a multi-model API proxy at `https://opencode.ai/zen/v1` with an OpenAI-compatible `/chat/completions` endpoint. This follows the exact same pattern as `xai` — a one-line addition to `CreateProvider` reusing `OpenAIProvider`.

## User Story

As a Virgil user
I want to use OpenCode Zen as my AI provider
So that I can access Zen's model catalogue through a single proxy

## Relevant Files

- `internal/bridge/bridge.go` — `CreateProvider` switch, add `"zen"` case
- `config/virgil.yaml` — update available providers comment
- `docs/setup.md` — add Zen row to the API keys table

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add Zen to provider factory

In `internal/bridge/bridge.go`, add a case to the `CreateProvider` switch:

```go
case "zen":
    return OpenAIProvider(config, "https://opencode.ai/zen/v1", "ZEN_API_KEY")
```

### 2. Update config comment

In `config/virgil.yaml`, change the providers comment to include `zen`:

```yaml
# Available providers: anthropic, openai, xai, zen, gemini, claude (CLI fallback)
```

### 3. Update setup docs

In `docs/setup.md`, add a row to the AI Provider API Keys table:

| Zen | available as alternative proxy | `ZEN_API_KEY` |

And add `ZEN_API_KEY` to both the env var and credentials file examples.

## Testing Strategy

No new test file needed. The existing `openai_test.go` already tests `OpenAIProvider` with arbitrary base URLs and env vars (`TestOpenAIProviderBaseURLOverride` tests xAI through the identical code path). Zen uses the same function — the only new code is a switch case routing to it.

Run existing tests to confirm nothing breaks:

```bash
go test ./internal/bridge/ -v -count=1
```

## Validation Commands

```bash
go test ./... -count=1
```

## Open Questions (Unresolved)

- **Endpoint compatibility across model families.** Zen routes different model families to different endpoints: `/chat/completions` for OpenAI-compatible models, `/messages` for Anthropic, `/models/{id}` for Google. This spec uses `/chat/completions`. If users need Anthropic models through Zen, a future enhancement could expose `base_url` in the YAML config so any existing provider can be pointed at Zen. **Recommendation:** ship with `/chat/completions`, add `base_url` config support only if requested.

## Sub-Tasks

Single task — no decomposition needed.
