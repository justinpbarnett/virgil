# Feature: Spec Pipe — Interactive Specification Collaboration

## Metadata

type: `feat`
task_id: `spec-pipe`
prompt: `Add a spec pipe that enables interactive specification collaboration. User says "spec out <feature>", Virgil studies the codebase, generates an initial spec draft, writes it to specs/, and streams the result. Follow-up signals update the spec file in place. Each turn is stateless — the spec file on disk is the only persistent artifact, read fresh as context each turn. A single working_state key (spec/active) enables follow-up resolution without session management.`

## Feature Description

Virgil needs a way to collaboratively develop implementation specifications through natural conversation. The user says "let's spec out feature X", Virgil studies the codebase, generates an initial spec draft, writes it to `specs/`, and streams the result. The user provides feedback ("I think we should use approach B for storage"), Virgil reads the current spec file, updates it with the new decision, and presents implications. This iterates until the spec is complete, at which point the user can hand it to the build pipeline.

The design preserves Virgil's stateless, ephemeral prompt architecture. Each signal is independent — the spec file on disk is the only persistent artifact, read fresh as context each turn (the same way codebase files are read). No session tracking, no phase management, no accumulated working_state beyond a single pointer (`spec/active`) for follow-up resolution. The AI infers phase from the spec file's content: missing file = create, file with open questions = iterate, all sections resolved = ready.

The spec pipe is a new pipe that handles spec file resolution, generation, and persistence. It composes with study via templates: `study → spec` for initial creation. Follow-ups route directly to `spec` via the AI planner. The spec pipe owns both generation and presentation — no separate chat step needed since the streamed spec output IS the response.

## User Story

As a Virgil user
I want to say "spec out <feature>" and iterate on the design through conversation
So that I can develop detailed implementation specs without leaving the terminal, and hand them to the build pipeline when ready

## Relevant Files

- `internal/pipes/draft/pipe.yaml` — has `spec` type with `spec-create` and `spec-update` prompt templates (reference for prompt design; spec pipe has its own prompts that can evolve independently)
- `internal/pipes/draft/draft.go` — reference implementation for the compile-templates + AI-generation pattern via `pipeutil`
- `internal/store/store.go` — `PutState`/`GetState` for `spec/active` working_state; no changes needed
- `internal/slug/slug.go` — `Slugify` for generating spec filenames from topic descriptions; no changes needed
- `internal/config/config.go` — auto-discovers `internal/pipes/*/pipe.yaml`; no changes needed
- `internal/pipehost/host.go` — subprocess harness; no changes needed
- `internal/bridge/bridge.go` — `Provider` and `StreamingProvider` interfaces; no changes needed
- `internal/runtime/runtime.go` — plan executor; no changes needed
- `internal/planner/aiplanner.go` — Layer 4 AI planner handles follow-up signals that don't match spec keywords; no changes needed (benefits from `spec/active` in working_state)
- `justfile` — add spec pipe to build targets

### New Files

- `internal/pipes/spec/pipe.yaml` — pipe config: triggers, keywords, vocabulary, flags, prompts, templates
- `internal/pipes/spec/spec.go` — handler: resolve spec file, read existing, generate via AI, write to disk, set working_state
- `internal/pipes/spec/spec_test.go` — unit tests
- `internal/pipes/spec/cmd/main.go` — subprocess binary entry point

## Implementation Plan

### Phase 1: Pipe Config

Create `pipe.yaml` with routing triggers, vocabulary, flags, system prompts, and template entries that compose the study → spec flow.

### Phase 2: Handler

Implement the spec pipe handler: resolve target spec file (substring match against `specs/` filenames, `spec/active` fallback, slug generation for new), read existing content, call AI to generate/update, write to disk, set `spec/active` working_state.

### Phase 3: Binary

Create the subprocess entry point following the `pipehost.RunWithStreaming` pattern.

### Phase 4: Build Integration

Add the spec pipe to the justfile build targets.

### Phase 5: Tests

Test spec file resolution, create vs update detection, filename generation, working_state management, and prompt construction.

## Step by Step Tasks

### 1. Create `internal/pipes/spec/pipe.yaml`

```yaml
name: spec
description: Interactive specification collaboration — generates, updates, and manages spec files.
category: dev
streaming: true
timeout: 3m
provider: anthropic
model: claude-opus-4-6

triggers:
  exact:
    - "write a spec"
    - "create a spec"
    - "spec this out"
  keywords:
    - spec
    - specification
    - requirements
    - design document
  patterns:
    - "spec out {topic}"
    - "spec {topic}"
    - "let's spec {topic}"
    - "create a spec for {topic}"
    - "write a spec for {topic}"

flags:
  topic:
    description: Feature description or search term for resolving existing spec.
    default: ""
  path:
    description: Explicit spec file path, overrides resolution.
    default: ""

memory:
  context:
    - type: working_state
    - type: recent_history
    - type: codebase
  budget: 2000

vocabulary:
  verbs:
    spec: [spec]
    specify: [spec]
  types:
    spec: [spec]
    specification: [spec]
  sources: {}
  modifiers: {}

templates:
  priority: 25
  entries:
    - requires: [verb]
      plan:
        - pipe: study
          flags:
            source: codebase
            role: planner
            budget: "6000"
        - pipe: spec

prompts:
  system: |
    You are a senior software architect collaborating on an implementation
    specification. You write specs for builders — no fluff, no business
    justification, no motivation sections. Every section earns its place
    by helping someone implement the feature correctly.

  templates:
    create: |
      Create an implementation specification for this feature.

      Feature request:
      {{.Signal}}

      {{if .CodebaseContext}}
      Codebase context:
      {{.CodebaseContext}}
      {{end}}

      Write a complete spec document in markdown with these sections:
      - **Metadata** — type: feat, task_id (slug from feature name), prompt (one-sentence summary)
      - **Feature Description** — what the feature does and why, written for builders
      - **User Story** — As a / I want / So that
      - **Relevant Files** — existing files that will be modified, with why. Subsection for new files.
      - **Implementation Plan** — phased approach (Foundation, Core, Integration)
      - **Step by Step Tasks** — numbered, ordered, with specific file paths and function names
      - **Testing Strategy** — unit tests, edge cases
      - **Risk Assessment** — what could break, migration concerns
      - **Validation Commands** — actual commands to verify (go test, go build, etc.)
      - **Open Questions** — anything you're uncertain about, with why it matters and your recommended answer

      Mark uncertain assumptions with [?]. Implementation steps must be independently verifiable.
      Be specific — name files, functions, types. Don't be vague.

    update: |
      Update this specification based on new input from the engineer.

      Current spec:
      {{.State}}

      Engineer's input:
      {{.Signal}}

      {{if .CodebaseContext}}
      Additional codebase context:
      {{.CodebaseContext}}
      {{end}}

      Rules:
      - Resolve any open questions addressed by the input — move them to a Decisions section with rationale
      - Update affected sections (scope, implementation plan, risks, step-by-step tasks)
      - Add any new open questions that arise from the decisions
      - Do NOT remove or rewrite decisions from previous rounds unless the engineer explicitly reverses them
      - If a new decision contradicts a previous one, flag it explicitly
      - Keep the document structure intact — update sections surgically, don't regenerate from scratch
      - Preserve all metadata and section headings

      Return the complete updated spec document.
```

### 2. Create `internal/pipes/spec/spec.go`

The handler implements this flow:

**a) Resolve spec file:**

```go
func resolveSpecFile(specsDir string, flags map[string]string, store specStore) (path string, exists bool) {
    // 1. Explicit path flag — use directly
    if p := flags["path"]; p != "" {
        full := filepath.Join(specsDir, p)
        _, err := os.Stat(full)
        return full, err == nil
    }

    // 2. Topic flag — substring match against specs/ filenames
    topic := flags["topic"]
    if topic == "" {
        topic = flags["signal"]
    }

    if topic != "" {
        if match := findSpec(specsDir, topic); match != "" {
            return match, true
        }
    }

    // 3. Fallback to spec/active working_state
    if store != nil {
        if active, ok, _ := store.GetState("spec", "active"); ok {
            full := filepath.Join(specsDir, active)
            if _, err := os.Stat(full); err == nil {
                return full, true
            }
        }
    }

    // 4. New spec — generate filename from topic
    if topic != "" {
        name := "feat-" + slug.Slugify(topic) + ".md"
        return filepath.Join(specsDir, name), false
    }

    return "", false
}
```

**b) Spec file matching:**

```go
func findSpec(specsDir, topic string) string {
    slugged := slug.Slugify(topic)
    entries, err := os.ReadDir(specsDir)
    if err != nil {
        return ""
    }
    var best string
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
            continue
        }
        if strings.Contains(e.Name(), slugged) {
            // Prefer shortest match to avoid over-broad hits
            if best == "" || len(e.Name()) < len(best) {
                best = e.Name()
            }
        }
    }
    if best != "" {
        return filepath.Join(specsDir, best)
    }
    return ""
}
```

**c) Handler implementation:**

```go
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, store specStore, specsDir string, logger *slog.Logger) pipe.Handler
```

**Prompt data struct** — three fields, matching the template placeholders:

```go
type promptData struct {
    Signal          string // user's original text
    State           string // existing spec content (empty on create)
    CodebaseContext string // upstream study output
}
```

**Handler logic:**
1. Call `resolveSpecFile` to find target path and whether it exists
2. If exists, read current content from disk → populate `State`
3. Template key is `"create"` if new file, `"update"` if existing — auto-detected, no flag needed
4. Build `promptData` with signal from `flags["signal"]`, codebase context from `input.Content` (upstream study output), and existing state
5. Call `provider.Complete` to generate spec content
6. Write spec content to resolved path via `os.WriteFile` (create parent dirs with `os.MkdirAll` if needed)
7. Set `spec/active` working_state to the filename (basename only)
8. Return envelope with spec content as `ContentText`

The streaming handler (`NewStreamHandler`) follows the same logic but uses `provider.CompleteStream`.

**d) specStore interface:**

```go
type specStore interface {
    GetState(namespace, key string) (string, bool, error)
    PutState(namespace, key, content string) error
}
```

This keeps the pipe testable without importing the full store package.

### 3. Create `internal/pipes/spec/cmd/main.go`

Follow the standard pipehost pattern:

```go
func main() {
    logger := pipehost.NewPipeLogger("spec")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("spec", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("spec", err.Error())
    }

    // Open store for spec/active working_state (nil if unavailable)
    var st spec.SpecStore
    if dbPath := os.Getenv(pipehost.EnvDBPath); dbPath != "" {
        if s, err := store.Open(dbPath); err == nil {
            st = s
            defer s.Close()
        }
    }

    specsDir := filepath.Join(os.Getenv(pipehost.EnvWorkDir), "specs")
    compiled := pipeutil.CompileTemplates(pc)

    logger.Info("initialized")

    pipehost.RunWithStreaming(provider,
        spec.NewHandlerWith(provider, pc, compiled, st, specsDir, logger),
        func(sp bridge.StreamingProvider) pipe.StreamHandler {
            return spec.NewStreamHandlerWith(sp, pc, compiled, st, specsDir, logger)
        },
    )
}
```

### 4. Update `justfile`

Add `spec` to the pipe build targets alongside existing pipes (calendar, chat, draft, etc.):

```
build-pipes: build-calendar build-chat build-draft ... build-spec
build-spec:
    go build -o internal/pipes/spec/run ./internal/pipes/spec/cmd/
```

### 5. Create `internal/pipes/spec/spec_test.go`

Test cases:

- `TestResolveSpecFile_ExplicitPath` — path flag set, file exists → returns that path
- `TestResolveSpecFile_ExplicitPathNew` — path flag set, file doesn't exist → returns path, exists=false
- `TestResolveSpecFile_TopicMatch` — topic "slack pipe" matches `specs/feat-slack-pipe.md`
- `TestResolveSpecFile_TopicNoMatch` — topic "quantum computing" matches nothing → generates new filename
- `TestResolveSpecFile_ActiveFallback` — no path/topic, `spec/active` = `feat-foo.md` → returns that
- `TestResolveSpecFile_NewFromTopic` — topic "notification system" → `specs/feat-notification-system.md`
- `TestFindSpec_SubstringMatch` — "slack" matches `feat-slack-pipe.md`
- `TestFindSpec_PrefersShortestMatch` — multiple candidates, returns shortest filename
- `TestFindSpec_EmptyDir` — empty specs dir → no match
- `TestHandler_CreateNewSpec` — no existing file, verify AI called with `create` template, file written, working_state set
- `TestHandler_UpdateExistingSpec` — existing file, verify AI called with `update` template containing current content, file overwritten
- `TestHandler_CodebaseContextPassthrough` — upstream study content passed through as CodebaseContext in prompt
- `TestHandler_WorkingStateSet` — verify `spec/active` set to basename after create and update

## Testing Strategy

### Unit Tests

- Spec file resolution: all paths through `resolveSpecFile` (explicit path, substring match, active fallback, new generation)
- Substring matching: match, no match, shortest-wins tiebreak, empty dir
- Prompt construction: create vs update template selection (auto-detected from file existence), field population (Signal, State, CodebaseContext)
- Handler integration with mock provider and temp directory: verify file I/O, working_state calls

### Edge Cases

- Signal with no identifiable topic — falls back to `spec/active`, then returns error if nothing found
- `specs/` directory doesn't exist — `os.MkdirAll` creates it on first write
- Spec file modified externally (by user in editor or Claude Code) between turns — file is read fresh each turn, updates merge cleanly
- Multiple specs with similar names (e.g., `feat-slack-pipe.md` and `feat-slack-integration.md`) — substring match returns shortest; user can disambiguate with path flag
- Upstream study pipe returns empty content — spec pipe generates from signal alone, codebase context is optional
- Store unavailable (nil) — `spec/active` fallback skipped, resolution still works via path/topic flags

## Risk Assessment

- **No existing pipe conflicts.** The `spec` vocabulary verbs (`spec`, `specify`) don't overlap with existing pipe vocabularies.
- **Substring matching false positives.** A topic like "pipe" would match many spec files. The shortest-match tiebreak helps, but broad single-word topics can still surprise. The `path` flag provides an explicit override.
- **AI planner follow-up quality.** Follow-up signals ("I think approach B is better") rely on the AI planner (Layer 4) to compose `[spec]`. The AI planner already handles this pattern for calendar follow-ups. The `spec/active` working_state surfaces in the planner's memory context via `listAllState` (used by `RetrieveContext`), so the planner sees which spec is in progress.
- **Build pipeline handoff is manual.** This spec does not implement automatic approval → build triggering. The user says "build specs/feat-X.md" as a separate signal. This is intentional — it preserves human-in-the-loop and avoids a new server-level pipeline handoff primitive.

## Validation Commands

```bash
go test ./internal/pipes/spec/... -v -count=1
go build ./internal/pipes/spec/cmd/
just test
```

## Decisions

1. **Provider/model**: Anthropic Claude Opus (`claude-opus-4-6`) — spec generation benefits from stronger reasoning than conversational chat. Worth the cost tradeoff for higher-quality architectural output.

2. **Filename prefix**: Default to `feat-` since most specs are features. Add a `type` flag later if needed for other spec types (`refactor-`, `perf-`, etc.).

## Sub-Tasks

Single task — no decomposition needed. The spec pipe follows established patterns (`pipeutil` for template compilation, `pipehost` for subprocess harness, `bridge.Provider` for AI calls). All four new files can be implemented in one pass.
