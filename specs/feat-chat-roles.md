# Feature: Chat Pipe — Role-Based System Prompts

## Metadata

type: `feat`
task_id: `chat-roles`
prompt: `Extend the chat pipe with a role flag that selects different system prompts, the same way draft uses type and review uses criteria. Add a spec-collaborator role with a system prompt tuned for presenting analysis, framing tradeoffs as decisions, contextualizing questions, and signaling spec completion. The default (empty/general) role preserves current behavior.`

## Feature Description

The chat pipe is the conversational terminal — the last step in pipelines that need to present results to the user in natural language. It already handles pipeline synthesis: when an upstream pipe's output arrives as context, chat combines it with the original signal and responds conversationally.

The problem is the system prompt is fixed: "You are Virgil, a personal assistant. Respond helpfully and concisely." This works for general chat but produces generic output when the pipeline needs a specific voice. The spec pipeline needs a collaborator who frames tradeoffs as decisions, explains why questions matter, and signals when a spec is complete. The dev pipeline might need a reporter who summarizes build results. These are different personas, not different pipes.

The solution mirrors what draft and review already do: a flag that selects a system prompt template. The `role` flag picks a system prompt from `prompts.templates` in `pipe.yaml`. The general/default role uses the existing system prompt. New roles add new system prompts without touching the handler code.

## User Story

As a Virgil pipeline
I want to select a chat persona via a flag
So that the same chat pipe can present results with the right voice and framing for different pipeline contexts

## Relevant Files

- `internal/pipes/chat/pipe.yaml` — add `role` and `phase` flags, add system prompt templates per role
- `internal/pipes/chat/chat.go` — update `prepareChat` to select system prompt by role, update handlers to accept compiled templates
- `internal/pipes/chat/chat_test.go` — tests for role-based prompt selection (create if needed)
- `internal/pipes/chat/cmd/main.go` — update to compile templates and pass to handler

### New Files

None.

## Implementation Plan

### Phase 1: Config

Add role flag and system prompt templates to `pipe.yaml`.

### Phase 2: Code

Add template compilation and role-based system prompt selection to the handler.

### Phase 3: Tests

Test that role flag selects the correct system prompt.

## Step by Step Tasks

### 1. Update `internal/pipes/chat/pipe.yaml`

Add flags:

```yaml
flags:
  role:
    description: Persona to adopt for the response.
    values: [general, spec-collaborator]
    default: general
  phase:
    description: Stage of the interaction, affects framing.
    values: [initial, progress, complete]
    default: progress
```

Add system prompt templates (these are role-specific system prompts, not user prompt templates):

```yaml
prompts:
  system: |
    You are Virgil, a personal assistant. Respond helpfully and concisely.

  templates:
    general: |
      You are Virgil, a personal assistant. Respond helpfully and concisely.

    spec-collaborator-initial: |
      You are presenting the initial analysis of a technical problem to the
      engineer you're collaborating with on a specification.

      You are a peer, not a servant. Present your analysis with conviction.
      When you have an opinion on a tradeoff, state it and say why. When
      you need input, ask specific questions and explain why the answer
      matters for implementation.

      Guidelines:
      - Present tradeoffs as real choices with concrete consequences,
        not abstract "pros and cons"
      - When you recommend an approach, say which and why
      - Frame each question with its impact: "I need to know X because
        it determines whether we need Y"
      - Be concise — don't repeat what the user told you
      - No headers or bullet-point lists for the sake of structure —
        talk like you're whiteboarding with a colleague
      - End with a clear summary of what you need from them

    spec-collaborator-progress: |
      You are continuing a specification collaboration. The engineer has
      provided answers or feedback on the current spec draft.

      Guidelines:
      - Briefly acknowledge what was resolved (one sentence, not a recap)
      - Explain any implications their decisions create
      - Present remaining questions with context
      - If you have a recommendation on remaining decisions, state it
      - Signal progress: "We've nailed down X and Y, still need Z"
      - Be concise — momentum matters in iterative work

    spec-collaborator-complete: |
      The specification is complete — all questions resolved, review passed.
      Present the final result to the engineer.

      Guidelines:
      - State clearly that the spec is ready
      - Give a 2-3 sentence summary of what was specified
      - Note the key decisions that shaped the spec
      - Mention what pipeline can consume it next (dev-feature)
      - Keep it brief — the spec document itself has the details
```

### 2. Update `internal/pipes/chat/chat.go`

**a) Add template compilation:**

```go
func CompileSystemPrompts(pipeConfig config.PipeConfig) map[string]string {
    prompts := make(map[string]string)
    for name, tmpl := range pipeConfig.Prompts.Templates {
        prompts[name] = tmpl
    }
    return prompts
}
```

These are plain strings (not Go templates needing execution) since system prompts don't have variable interpolation — the context comes from the user prompt side, which pipeline synthesis already handles.

**b) Add role resolution function:**

```go
func resolveSystemPrompt(prompts map[string]string, basePrompt string, flags map[string]string) string {
    role := flags["role"]
    if role == "" || role == "general" {
        return basePrompt
    }

    // Try compound key: role-phase
    if phase := flags["phase"]; phase != "" {
        if p, ok := prompts[role+"-"+phase]; ok {
            return p
        }
    }

    // Try role alone
    if p, ok := prompts[role]; ok {
        return p
    }

    // Fall back to base
    return basePrompt
}
```

**c) Update `NewStreamHandler` and `NewHandler`:**

Add `prompts map[string]string` parameter. In the handler function, replace the fixed `systemPrompt` with `resolveSystemPrompt(prompts, systemPrompt, flags)`.

Current signature:
```go
func NewStreamHandler(provider bridge.StreamingProvider, systemPrompt string, logger *slog.Logger) pipe.StreamHandler
```

New signature:
```go
func NewStreamHandler(provider bridge.StreamingProvider, systemPrompt string, prompts map[string]string, logger *slog.Logger) pipe.StreamHandler
```

Same for `NewHandler`. Inside each handler, the change is one line — replace `systemPrompt` in the `provider.Complete`/`CompleteStream` call with `resolveSystemPrompt(prompts, systemPrompt, flags)`.

### 3. Update `internal/pipes/chat/cmd/main.go`

After loading pipe config, compile system prompts and pass to handler:

```go
pc, err := pipehost.LoadPipeConfig()
// handle err
prompts := chat.CompileSystemPrompts(pc)
// pass prompts to NewStreamHandler/NewHandler
```

### 4. Write tests

In `internal/pipes/chat/chat_test.go` (create if needed):

- `TestResolveSystemPrompt_Default` — empty role, returns base prompt.
- `TestResolveSystemPrompt_General` — role=general, returns base prompt.
- `TestResolveSystemPrompt_SpecCollaboratorInitial` — role=spec-collaborator, phase=initial. Returns `spec-collaborator-initial` prompt.
- `TestResolveSystemPrompt_SpecCollaboratorProgress` — role=spec-collaborator, phase=progress. Returns `spec-collaborator-progress` prompt.
- `TestResolveSystemPrompt_SpecCollaboratorComplete` — role=spec-collaborator, phase=complete. Returns `spec-collaborator-complete` prompt.
- `TestResolveSystemPrompt_UnknownRole` — role=nonexistent, returns base prompt.
- `TestResolveSystemPrompt_RoleWithoutPhase` — role=spec-collaborator, no phase flag. Falls back to `spec-collaborator` key (doesn't exist), then to base. This is correct — the pipeline should always specify a phase.
- `TestCompileSystemPrompts` — verify all templates from pipe.yaml are compiled into the map.
- `TestPrepareChat_Unchanged` — verify prepareChat behavior is not affected by the role changes (it still handles content extraction and pipeline synthesis).

## Testing Strategy

### Unit Tests

- `resolveSystemPrompt` with all key resolution paths
- `CompileSystemPrompts` from pipe config
- Handler integration with mock provider, verifying the correct system prompt reaches the provider

### Edge Cases

- Role flag set but phase flag missing — falls back cleanly
- Role flag set to unknown value — falls back to base prompt
- Both role and phase set but no compound key exists — falls back to role-only, then base
- Pipeline synthesis still works correctly with role-based prompts (the user prompt content assembly is independent of system prompt selection)

## Risk Assessment

- **Backwards compatible.** The default role is `general`, which uses the existing system prompt. The handler signature changes, but the entry point is the only caller — no external API change.
- **No performance impact.** System prompt selection is a map lookup.
- **Adding new roles later** requires only a `pipe.yaml` change — add the role value to the flag and add the prompt template. No code changes.

## Validation Commands

```bash
go test ./internal/pipes/chat/... -v -count=1
go build ./internal/pipes/chat/cmd/
```
