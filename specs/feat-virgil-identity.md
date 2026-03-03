# Feature: Virgil Identity — Global Personality Layer

## Metadata

type: `feat`
task_id: `virgil-identity`
prompt: `Add a global identity/personality layer to Virgil. A base identity prompt in virgil.yaml gets prepended to every AI-invoking pipe's system prompt, giving a consistent voice across all non-deterministic outputs. The per-pipe system prompt handles what the pipe does; the identity prompt handles who is speaking.`

## Feature Description

Every AI-invoking pipe currently defines its own system prompt in `pipe.yaml`. These prompts describe the pipe's function ("You are a professional writer," "You are a senior engineer conducting a technical analysis") but there's no shared identity. The chat pipe says "You are Virgil, a personal assistant" — generic, and only in chat. The draft pipe says "You are a professional writer" — no Virgil at all.

The fix: a single `identity` field in `virgil.yaml` containing Virgil's character — economical speech, guide-not-performer posture, honest about limits, context-sensitive register. This text gets prepended to every AI pipe's system prompt automatically. The pipe's own prompt narrows behavior; the identity keeps the voice consistent.

The literary Virgil is the model: explains the terrain not himself, stern when stakes warrant it, knows his limits, speaks in the register of the moment, and crucially — stays silent when there's nothing to add.

## User Story

As a Virgil user
I want all AI-generated responses to share a consistent voice and personality
So that Virgil feels like a coherent guide rather than a collection of disconnected AI calls

## Relevant Files

- `config/virgil.yaml` — add `identity` field with the personality prompt
- `internal/config/config.go` — add `Identity string` to `Config` struct
- `cmd/virgil/main.go` — pass identity to subprocesses via `VIRGIL_IDENTITY` env var in `pipeEnv()`
- `internal/pipehost/host.go` — add `EnvIdentity` constant with the others, add unexported `injectIdentity` helper, call it from `LoadPipeConfigFrom()`
- `internal/pipes/chat/pipe.yaml` — narrow system prompts to role-only (remove "You are Virgil" phrasing)

### New Files

None.

## Implementation Plan

### Phase 1: Config Foundation

Add the `identity` field to `virgil.yaml` and the `Config` struct. New top-level YAML field — no migration needed, no existing fields affected.

### Phase 2: Environment Propagation

Pass the identity text from the server to pipe subprocesses via a new `VIRGIL_IDENTITY` environment variable. Add an unexported `injectIdentity` helper in `host.go` that reads the env var and prepends identity to all prompts in a `PipeConfig`. Wire it into `LoadPipeConfigFrom()` so every pipe gets identity automatically — no per-pipe changes needed.

### Phase 3: Identity Content

Write the actual identity prompt. Update the chat pipe's system prompts to remove identity-level language that now lives in the global identity.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add `Identity` field to config struct

In `internal/config/config.go`, add `Identity string` to the `Config` struct with YAML tag `identity`:

```go
type Config struct {
	Server       ServerConfig          `yaml:"server"`
	Provider     ProviderConfig        `yaml:"provider"`
	Identity     string                `yaml:"identity"`
	LogLevel     LogLevel              `yaml:"log_level"`
	DatabasePath string                `yaml:"database_path"`
	ConfigDir    string                `yaml:"-"`
	Pipes        map[string]PipeConfig `yaml:"-"`
	Vocabulary   VocabularyConfig      `yaml:"-"`
	Templates    TemplatesConfig       `yaml:"-"`
}
```

No changes to `Load()` needed — `loadYAML` already unmarshals all YAML fields into the struct.

### 2. Add identity to `virgil.yaml`

Add the `identity` field to `config/virgil.yaml`:

```yaml
identity: |
  You are Virgil — a guide, not a performer. You speak with the calm
  authority of someone who has walked this path before. You are economical
  with words: say what matters, nothing more. You do not narrate your own
  competence. You do not ask permission when the path is clear. When you
  don't know something, you say so plainly — you are a guide through the
  knowable, not an oracle.

  You are protective without being patronizing. You steer, you warn,
  you illuminate — but you never take the journey from the traveler.
  When the task is beyond your domain, you say so and point toward
  who can help.

  Core traits:
  - Economical. No padding, no pleasantries, no "is there anything else."
  - Direct. State what matters. Move on.
  - Pattern-aware. When you notice recurring issues, name them.
  - Honest about limits. When you reach the boundary of what you can do
    well, say so. Don't fabricate competence.
  - Context-sensitive. Terse for quick factual queries, more measured for
    complex creative work, direct for errors, reflective for summaries.
  - Oriented outward. Describe the user's situation, not your own process.
    Not "I'll retrieve your notes and draft a post" but "Here's the draft,
    drawn from your last three entries."
  - Protective when stakes warrant it. Push back on ambiguous or risky
    requests rather than blindly executing.
```

### 3. Pass identity via environment variable

In `cmd/virgil/main.go`, update `pipeEnv()` to include `VIRGIL_IDENTITY`:

```go
func pipeEnv(cfg *config.Config, cfgDir string) []string {
	env := os.Environ()
	env = append(env,
		pipehost.EnvDBPath+"="+cfg.DatabasePath,
		pipehost.EnvConfigDir+"="+cfgDir,
		pipehost.EnvUserDir+"="+config.UserDir(),
		pipehost.EnvProvider+"="+cfg.Provider.Name,
		pipehost.EnvProviderBinary+"="+cfg.Provider.Binary,
		pipehost.EnvIdentity+"="+cfg.Identity,
	)
	return env
}
```

### 4. Add identity injection to `internal/pipehost/host.go`

Add the `EnvIdentity` constant alongside the existing `Env*` constants:

```go
const (
	EnvDBPath         = "VIRGIL_DB_PATH"
	EnvConfigDir      = "VIRGIL_CONFIG_DIR"
	EnvUserDir        = "VIRGIL_USER_DIR"
	EnvProvider       = "VIRGIL_PROVIDER"
	EnvModel          = "VIRGIL_MODEL"
	EnvProviderBinary = "VIRGIL_PROVIDER_BINARY"
	EnvLogLevel       = "VIRGIL_LOG_LEVEL"
	EnvMaxTurns       = "VIRGIL_MAX_TURNS"
	EnvIdentity       = "VIRGIL_IDENTITY"
)
```

Add the unexported `injectIdentity` function:

```go
// injectIdentity prepends the global identity prompt (from VIRGIL_IDENTITY)
// to all system prompts in the pipe config. No-op if the env var is empty
// or the config has no prompts.
func injectIdentity(pc *config.PipeConfig) {
	identity := strings.TrimSpace(os.Getenv(EnvIdentity))
	if identity == "" {
		return
	}
	if pc.Prompts.System != "" {
		pc.Prompts.System = identity + "\n\n" + pc.Prompts.System
	}
	for k, v := range pc.Prompts.Templates {
		pc.Prompts.Templates[k] = identity + "\n\n" + v
	}
}
```

Wire it into `LoadPipeConfigFrom`:

```go
func LoadPipeConfigFrom(path string) (config.PipeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.PipeConfig{}, fmt.Errorf("reading pipe.yaml: %w", err)
	}
	var pc config.PipeConfig
	if err := config.UnmarshalPipeConfig(data, &pc); err != nil {
		return config.PipeConfig{}, fmt.Errorf("parsing pipe.yaml: %w", err)
	}
	injectIdentity(&pc)
	return pc, nil
}
```

Every pipe already calls `LoadPipeConfig()` → `LoadPipeConfigFrom()`. Deterministic pipes have empty prompts so `injectIdentity` is a no-op. AI-using pipes get identity prepended automatically. Zero per-pipe changes.

### 5. Update chat pipe system prompts

The chat pipe is the only pipe with "You are Virgil" in its system prompts. The identity layer now covers that, so narrow to role-only language.

In `internal/pipes/chat/pipe.yaml`, change the base system prompt and `general` template from:

```
You are Virgil, a personal assistant. Respond helpfully and concisely.
```

to:

```
Respond to the user's message. Be conversational and concise.
```

Leave `spec-collaborator-*` templates unchanged — they describe the role, not the identity.

All other AI pipes (draft, analyze, review, code, fix, etc.) already use role-specific language ("You are a professional writer," "You are a senior engineer") that complements rather than conflicts with the identity. No changes needed.

### 6. Write tests

In `internal/pipehost/host_test.go`, test identity injection through the public `LoadPipeConfigFrom` interface:

```go
func TestLoadPipeConfigFrom_InjectsIdentity(t *testing.T)
// Write a temp pipe.yaml with a system prompt and templates.
// Set VIRGIL_IDENTITY env var.
// Call LoadPipeConfigFrom. Verify identity is prepended to system prompt
// and all templates.

func TestLoadPipeConfigFrom_NoIdentity(t *testing.T)
// Unset VIRGIL_IDENTITY. Write a temp pipe.yaml.
// Call LoadPipeConfigFrom. Verify prompts are unchanged.

func TestLoadPipeConfigFrom_EmptyPrompts(t *testing.T)
// Set VIRGIL_IDENTITY. Write a temp pipe.yaml with no system prompt.
// Call LoadPipeConfigFrom. Verify system prompt stays empty
// (don't prepend identity to nothing).

func TestLoadPipeConfigFrom_TemplatesOnly(t *testing.T)
// Set VIRGIL_IDENTITY. Write a temp pipe.yaml with templates but no base
// system prompt. Verify templates get identity, base stays empty.
```

## Testing Strategy

### Unit Tests

- `LoadPipeConfigFrom` with identity env var set/unset, system prompt present/absent, templates present/absent
- Config parsing: verify `Identity` field is populated from YAML

### Edge Cases

- Empty identity in `virgil.yaml` — all pipes behave exactly as before
- Identity with trailing whitespace — trimmed before prepending
- Pipe with no system prompt (deterministic pipes) — no-op
- Pipe with templates but no base system prompt — templates get identity, base stays empty

### Integration Verification

- Build all pipes (`just build`) — no compilation errors
- Run full test suite (`just test`) — no regressions
- Manual test: send a chat signal, verify response voice matches the identity

## Risk Assessment

- **Fully backwards compatible.** If `identity` is absent from `virgil.yaml`, the env var is empty, `injectIdentity` is a no-op, and all behavior is identical to current.
- **No per-pipe code changes.** Identity injection happens inside `LoadPipeConfigFrom`, which every pipe already calls. No handler signatures change. No `cmd/main.go` files touched.
- **Environment variable size.** The identity prompt is ~800 bytes. Environment variables support up to 128KB+ on Linux. No concern.
- **Prompt length.** Prepending ~200 tokens of identity to each system prompt is negligible relative to context window limits.
- **Per-pipe prompt conflicts.** Only the chat pipe says "You are Virgil" — step 5 fixes it. All other pipes use role-specific language that complements the identity.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just build
just test
```

## Open Questions (Unresolved)

**1. Should deterministic pipe format templates be updated for voice?**
Format templates like the calendar's "Your calendar is clear." are functional but personality-neutral. They could be rewritten in Virgil's voice ("Calendar's clear."). This is a content-only change to `pipe.yaml` format templates, no code involved. **Recommendation:** follow-up task after identity is working.

**2. Should identity be overridable per-pipe?**
A pipe might want to suppress identity (e.g., analyze pipe producing structured JSON where personality could interfere with schema compliance). Currently `injectIdentity` applies unconditionally. A per-pipe `identity: false` flag in `pipe.yaml` could opt out. **Recommendation:** defer. The analyze pipe's "Respond ONLY with JSON" instruction should override identity's tone guidance. Add opt-out if a real problem surfaces.

**3. How should the identity prompt be iterated on?**
The identity text in step 2 is a starting point. Since it lives in `virgil.yaml` (not code), iteration requires no rebuilds. **Recommendation:** ship the initial version, refine through use.

## Sub-Tasks

Single task — no decomposition needed. Three files changed, no new files, zero per-pipe modifications.
