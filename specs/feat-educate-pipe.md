# Feature: Educate Pipe

## Metadata

type: `feat`
task_id: `educate-pipe`
prompt: `Create the educate pipe — a non-deterministic, streaming pipe that socratically teaches the user about a subject. It gauges current understanding, then teaches iteratively through probing questions and guided discovery rather than lecturing.`

## Feature Description

The educate pipe is a Socratic teaching engine. Given a subject ("teach me kubernetes", "explain recursion"), it doesn't dump information — it assesses what the user already knows, then asks targeted questions that guide them to discover the answers themselves. It challenges assumptions, surfaces misconceptions, and builds understanding layer by layer.

This is fundamentally different from the chat pipe's general-purpose responses or draft's document production. The educate pipe has a pedagogical model: assess → question → probe → build → consolidate. The system prompt enforces this discipline — the AI must resist the urge to lecture and instead ask the question that makes the student think.

The pipe operates in phases via the `phase` flag:

- **assess** — opening move. Gauge what the user knows about the subject before teaching anything.
- **teach** — main loop. Ask Socratic questions, respond to answers with deeper questions, correct misconceptions gently, build toward understanding.
- **consolidate** — wrap-up. Summarize what was learned, identify gaps remaining, suggest next steps.

## User Story

As a Virgil user
I want to say "teach me kubernetes" and have Virgil guide me through the subject using questions and dialogue
So that I deeply understand concepts through active discovery rather than passive reading

## Relevant Files

- `internal/bridge/bridge.go` — `Provider` and `StreamingProvider` interfaces for AI completion
- `internal/config/config.go` — `PipeConfig` struct, `PromptsConfig`, `VocabularyConfig`
- `internal/envelope/envelope.go` — `Envelope`, `ContentToText`, content type constants
- `internal/pipe/pipe.go` — `Handler`, `StreamHandler` type definitions
- `internal/pipehost/host.go` — `Run`, `RunWithStreaming`, `BuildProviderFromEnvWithLogger`, `LoadPipeConfig`, `NewPipeLogger`
- `internal/pipes/chat/chat.go` — reference implementation for streaming conversational pipe with phase-based prompt selection
- `internal/pipes/chat/cmd/main.go` — reference `cmd/main.go` for streaming pipe with `RunWithStreaming`
- `internal/pipes/draft/draft.go` — reference for `CompileTemplates` pattern and template data structs

### New Files

- `internal/pipes/educate/pipe.yaml` — pipe definition with triggers, flags, vocabulary, Socratic prompts
- `internal/pipes/educate/educate.go` — handler implementation (sync + streaming)
- `internal/pipes/educate/educate_test.go` — unit tests
- `internal/pipes/educate/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml` with the Socratic teaching identity: triggers for teaching signals, flags for phase/level/style, vocabulary contributions, and prompt templates that enforce the Socratic method across all phases.

### Phase 2: Handler

Build the handler following the chat pipe pattern — phase-based prompt selection, streaming support, content synthesis from input envelope. The key difference is prompt design: every template must instruct the model to ask questions rather than explain.

### Phase 3: Subprocess Entry Point

Wire up `cmd/main.go` using `pipehost.RunWithStreaming` following the chat pipe pattern exactly.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create `internal/pipes/educate/pipe.yaml`

```yaml
name: educate
description: Teaches a subject using the Socratic method — gauges understanding, then guides discovery through questions.
category: research
streaming: true
timeout: 60s

triggers:
  exact:
    - "teach me"
    - "tutor me"
  keywords:
    - teach
    - learn
    - educate
    - tutor
    - explain
    - understand
    - lesson
    - study
  patterns:
    - "teach me {topic}"
    - "explain {topic}"
    - "help me understand {topic}"
    - "tutor me on {topic}"
    - "learn about {topic}"
    - "I want to learn {topic}"

flags:
  phase:
    description: Stage of the teaching interaction.
    values: [assess, teach, consolidate]
    default: assess
  level:
    description: Target difficulty level.
    values: [beginner, intermediate, advanced]
    default: ""
  style:
    description: Teaching style preference.
    values: [socratic, guided, challenge]
    default: socratic

vocabulary:
  verbs:
    teach: educate
    learn: educate
    educate: educate
    tutor: educate
    explain: educate
    understand: educate
  types: {}
  sources:
    notes: memory
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb, source, topic]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, topic: "{topic}", limit: "5" }
        - pipe: "{verb}"
          flags: { topic: "{topic}" }

    - requires: [verb, topic]
      plan:
        - pipe: "{verb}"
          flags: { topic: "{topic}" }

    - requires: [verb]
      plan:
        - pipe: "{verb}"

prompts:
  system: |
    You are a Socratic tutor. Your purpose is to help the student discover
    understanding through their own thinking — not to lecture, not to dump
    information, not to give answers they haven't earned.

    Your method:
    1. Ask ONE focused question at a time. Never ask multiple questions.
    2. Listen to the answer carefully. Identify what it reveals about
       understanding and misconceptions.
    3. Build on correct understanding. Don't repeat what they already know.
    4. Surface misconceptions gently through questions that expose the
       contradiction, not by saying "that's wrong."
    5. Use concrete examples and analogies the student can reason about.
    6. When the student is stuck, give a hint through a simpler related
       question — never just tell them the answer.
    7. Affirm genuine insight. When they get it, say so briefly and move deeper.

    What you must NOT do:
    - Do not give long explanations or lectures.
    - Do not list facts, definitions, or bullet points.
    - Do not answer your own questions.
    - Do not ask "do you understand?" — instead ask a question that
      TESTS understanding.
    - Do not be condescending. Treat the student as intelligent but learning.

    Keep responses concise. A good Socratic exchange is short turns, not essays.

  templates:
    assess: |
      The student wants to learn about a subject. Your job right now is to
      gauge what they already know — NOT to start teaching yet.

      Ask 1-2 targeted questions that reveal their current understanding level.
      Don't ask "what do you know about X?" — that's too vague. Ask something
      specific that someone with real understanding would answer differently
      than a beginner.

      Subject: {{.Topic}}
      {{if .Content}}
      Context from previous conversation:
      {{.Content}}
      {{end}}
      {{if .Level}}The student self-reports as: {{.Level}}. Verify this — people often misjudge their own level.{{end}}

    teach: |
      Continue the Socratic teaching dialogue. The student has responded to
      your previous question.

      Subject: {{.Topic}}

      Student's response:
      {{.Content}}

      Based on their response:
      1. Identify what they understand correctly and what's missing or wrong.
      2. If they showed understanding, go deeper — ask a question that builds
         on what they just demonstrated.
      3. If they showed a misconception, ask a question that will help them
         see the contradiction themselves.
      4. If they're stuck, offer a concrete analogy or simpler related question
         as a stepping stone.

      {{if .Style}}Teaching style: {{.Style}}{{end}}

    consolidate: |
      The teaching session is wrapping up. Based on the conversation so far,
      provide a brief consolidation:

      Subject: {{.Topic}}

      Conversation so far:
      {{.Content}}

      1. Summarize the key insights the student discovered (in 2-3 sentences).
      2. Name one thing they now understand well.
      3. Name one area where understanding is still developing.
      4. Suggest one specific thing to explore next.

      Keep it brief — this is a checkpoint, not a lecture.
```

### 2. Create `internal/pipes/educate/educate.go`

Define the template data struct, template compilation, prompt preparation, and both sync and streaming handlers.

**Template data struct:**

```go
type templateData struct {
    Content string
    Topic   string
    Level   string
    Style   string
}
```

**Key functions to implement:**

- `CompileTemplates(pc config.PipeConfig) map[string]*template.Template` — pre-compile `assess`, `teach`, `consolidate` templates from `pc.Prompts.Templates`. Same pattern as `draft.CompileTemplates`.

- `resolvePrompt(compiled map[string]*template.Template, pc config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt string, userPrompt string, err *envelope.EnvelopeError)` — extract content from the input envelope via `envelope.ContentToText`. Read `topic` from `flags["topic"]`, `phase` from `flags["phase"]` (default `"assess"`), `level` from `flags["level"]`, `style` from `flags["style"]`. Select the template matching `phase`. Render template with `templateData`. If no matching template exists, fall back to the system prompt with raw content. Return the system prompt from `pc.Prompts.System` and the rendered user prompt.

- `NewHandler(provider bridge.Provider, pc config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler` — call `resolvePrompt`, then `provider.Complete(ctx, systemPrompt, userPrompt)`. Return envelope with `pipe: "educate"`, `action: "teach"`, `content_type: "text"`, content = model response. On provider error, use `envelope.ClassifyError` (timeouts are retryable, auth errors are fatal).

- `NewStreamHandler(provider bridge.StreamingProvider, pc config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler` — same as `NewHandler` but calls `provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)`. Chunks streamed to user via `sink` callback.

**Implementation notes:**

- Follow the chat pipe's pattern for prompt resolution — the `resolvePrompt` function mirrors `chat.go`'s approach of building a composite key from flags to select the template.
- The `topic` flag is the subject being taught ("kubernetes", "recursion"). If absent, extract from the input envelope's content.
- Content synthesis: when receiving upstream content (e.g., from memory pipe), prepend it as context. The student's actual message is the primary content. Follow chat's `synthesizeContent` pattern if the input envelope has upstream context.

### 3. Create `internal/pipes/educate/cmd/main.go`

Follow the chat pipe's entry point pattern exactly:

```go
package main

import (
    "github.com/justinpbarnett/virgil/internal/bridge"
    "github.com/justinpbarnett/virgil/internal/pipe"
    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/educate"
)

func main() {
    logger := pipehost.NewPipeLogger("educate")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("educate", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("educate", err.Error())
    }

    compiled := educate.CompileTemplates(pc)

    pipehost.RunWithStreaming(provider,
        educate.NewHandler(provider, pc, compiled, logger),
        func(sp bridge.StreamingProvider) pipe.StreamHandler {
            return educate.NewStreamHandler(sp, pc, compiled, logger)
        })
}
```

### 4. Write tests in `internal/pipes/educate/educate_test.go`

**Test cases:**

- `TestCompileTemplates` — verify all three templates (`assess`, `teach`, `consolidate`) compile without error from a valid `PipeConfig`.
- `TestResolvePrompt_Assess` — phase=assess, topic="kubernetes". Verify the `assess` template is selected, topic appears in rendered prompt, system prompt is returned.
- `TestResolvePrompt_Teach` — phase=teach, content="I think pods are like containers". Verify `teach` template selected, student response appears in rendered prompt.
- `TestResolvePrompt_Consolidate` — phase=consolidate, content from conversation. Verify `consolidate` template selected.
- `TestResolvePrompt_DefaultPhase` — no phase flag provided. Verify defaults to `assess`.
- `TestResolvePrompt_WithLevel` — level=beginner. Verify level instruction appears in the assess template output.
- `TestResolvePrompt_WithStyle` — style=challenge. Verify style instruction appears in the teach template output.
- `TestResolvePrompt_EmptyContent` — empty input envelope. Should still work (assess phase doesn't require prior content).
- `TestResolvePrompt_TopicFromFlags` — topic provided in flags. Verify it populates `templateData.Topic`.
- `TestNewHandler_Success` — mock provider returning Socratic response. Verify envelope has `pipe: "educate"`, `action: "teach"`, `content_type: "text"`, non-empty content.
- `TestNewHandler_ProviderTimeout` — mock provider returning timeout error. Verify retryable error in envelope.
- `TestNewHandler_ProviderFatal` — mock provider returning auth error. Verify fatal error in envelope.
- `TestNewStreamHandler_Success` — mock streaming provider. Verify chunks are sent to sink, final envelope is correct.

## Testing Strategy

### Unit Tests

- Template compilation from pipe config
- Prompt resolution for all three phases with various flag combinations
- Handler integration with mock provider (both sync and streaming)
- Error classification (retryable vs fatal)

### Edge Cases

- No topic provided — the pipe should still work, using the input content as the implicit subject
- Phase flag with unknown value — should fall back gracefully (use system prompt with raw content)
- Very long student responses — pipe doesn't truncate, passes through to provider
- Upstream context from memory pipe — should be included as additional context, not replace the student's message
- Empty flags map — should default to assess phase with socratic style

## Risk Assessment

- **Vocabulary conflict:** The word "explain" could conflict with other pipes. Currently no other pipe maps "explain". If a future pipe does, it will be caught at startup by the config loader's conflict detection.
- **Keyword overlap with study pipe:** "study" is a keyword for the study pipe. The educate pipe uses "study" only in its patterns, not keywords, so no conflict. But "learn" could be ambiguous — the router's keyword scoring will differentiate based on surrounding context.
- **Teaching quality:** The Socratic method is hard for AI models. The system prompt is heavily constrained to prevent lecturing, but prompt quality will need iteration based on real usage. This is a configuration concern, not a code concern.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go test ./internal/pipes/educate/... -v -count=1
go build ./internal/pipes/educate/cmd/
```

## Open Questions (Unresolved)

- **Conversation state tracking:** The current pipe architecture is stateless per-invocation. Multi-turn Socratic dialogue requires the runtime/planner to pass conversation history through the envelope's content field. This works today (the planner can chain signals), but the educate pipe's effectiveness depends heavily on how much conversation history reaches it. **Recommendation:** Rely on the existing envelope content mechanism for now — the planner passes prior context. If this proves insufficient, it becomes a runtime-level feature request, not an educate pipe concern.

- **"explain" verb ownership:** Should "explain" map to `educate` or remain unmapped? Mapping it means "explain kubernetes" routes to a Socratic dialogue rather than a direct explanation. Some users saying "explain X" want a quick answer, not a teaching session. **Recommendation:** Map it to `educate`. Users wanting a quick answer can use the chat pipe directly, and the Socratic approach to explanation is more valuable than a dump of facts.

## Sub-Tasks

Single task — no decomposition needed.
