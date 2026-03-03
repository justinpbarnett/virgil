# Feature: Visualize Pipe

## Metadata

type: `feat`
task_id: `visualize-pipe`
prompt: `Create a non-deterministic visualize pipe that generates 3Blue1Brown-style mathematical/educational animations using Manim Community Edition. The AI generates Manim Python code from a concept description, executes it to render the visualization, and returns the output file path.`

## Feature Description

The visualize pipe turns concepts into 3Blue1Brown-style animated visualizations. Given a description ("visualize the Fourier transform", "animate how binary search works"), it uses an AI provider to generate Manim Community Edition Python code, then executes that code to render a video, GIF, or image. The output is a structured envelope containing the file path, format, and a text description of what was visualized.

This is a two-phase pipe internally: AI code generation, then subprocess execution. The AI phase is non-deterministic (generates Manim Python). The execution phase is deterministic (runs `manim render`). This pattern is the inverse of the review pipe (which fetches data first, then sends to AI) — here, AI goes first, then the result is executed.

The pipe does not stream. Manim rendering is a batch operation — the user waits for the full render. The timeout is extended to 120s to accommodate rendering time.

External dependencies: Manim Community Edition (Python), ffmpeg, and optionally LaTeX for mathematical typesetting. The pipe validates these at invocation time and returns actionable fatal errors if missing.

## User Story

As a Virgil user
I want to say "visualize the Fourier transform" and get back a 3Blue1Brown-style animated video
So that I can understand and present complex concepts through high-quality mathematical animations

## Relevant Files

### Existing Files (Reference or Modified)

- `internal/bridge/bridge.go` — `Provider` interface for AI completion (code generation step)
- `internal/config/config.go` — `PipeConfig` struct, `PromptsConfig`, `VocabularyConfig`
- `internal/envelope/envelope.go` — `Envelope`, `ContentToText`, content type constants, error constructors
- `internal/pipe/pipe.go` — `Handler` type definition (no streaming for this pipe)
- `internal/pipehost/host.go` — `Run`, `BuildProviderFromEnvWithLogger`, `LoadPipeConfig`, `NewPipeLogger`, `Fatal`
- `internal/pipeutil/pipeutil.go` — `CompileTemplates`, `ExecuteTemplate`, `FlagOrDefault`, `Executor`/`OSExecutor` (for manim subprocess), `StripMarkdownFences` (for extracting code from AI response)
- `internal/pipes/draft/draft.go` — reference for AI prompt resolution pattern (`preparePrompt`, `templateData`, `CompileTemplates`)
- `internal/pipes/review/review.go` — reference for structured output and external subprocess abstraction (`DiffFetcher` pattern → `ManimRenderer` pattern)

### New Files

- `internal/pipes/visualize/pipe.yaml` — pipe definition with triggers, flags, vocabulary, Manim code generation prompts
- `internal/pipes/visualize/visualize.go` — handler implementation: AI code gen + Manim execution
- `internal/pipes/visualize/visualize_test.go` — unit tests
- `internal/pipes/visualize/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml` with the visualization identity: triggers for visual/animation signals, flags for format/quality/style/duration, vocabulary contributions, and prompt templates that instruct the AI to produce valid Manim CE code with 3B1B aesthetics.

### Phase 2: Renderer Abstraction

Define a `Renderer` interface that wraps Manim execution, following the review pipe's `DiffFetcher` pattern. This enables testing with a mock renderer. The concrete `ManimRenderer` calls `manim render` via `pipeutil.OSExecutor`.

### Phase 3: Handler

Build the handler: resolve prompt from flags → call AI provider for Manim code → strip markdown fences → write code to temp file → call renderer → return structured envelope with file path.

### Phase 4: Subprocess Entry Point

Wire up `cmd/main.go` using `pipehost.Run` (no streaming). Validate Manim availability at startup.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create `internal/pipes/visualize/pipe.yaml`

```yaml
name: visualize
description: Creates 3Blue1Brown-style animated visualizations from concept descriptions using Manim.
category: comms
streaming: false
timeout: 120s

triggers:
  exact:
    - "visualize this"
    - "make an animation"
    - "create a visualization"
  keywords:
    - visualize
    - animate
    - animation
    - illustrate
    - diagram
    - visualization
    - manim
  patterns:
    - "visualize {topic}"
    - "animate {topic}"
    - "illustrate {topic}"
    - "create animation of {topic}"
    - "make a visualization of {topic}"

flags:
  format:
    description: Output format for the rendered visualization.
    values: [mp4, gif, png]
    default: mp4
  quality:
    description: Render quality — affects resolution and render time.
    values: [low, medium, high]
    default: low
  style:
    description: Visual style for the animation.
    values: [3b1b, light, minimal]
    default: 3b1b
  duration:
    description: Target animation duration.
    values: [short, medium, long]
    default: short

vocabulary:
  verbs:
    visualize: visualize
    animate: visualize
    illustrate: visualize
  types:
    animation: animation
    video: video
    diagram: diagram
    visualization: visualization
  sources: {}
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, topic: "{topic}", limit: "10" }
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }

    - requires: [verb, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, topic: "{topic}" }
        - pipe: "{verb}"
          flags: { topic: "{topic}" }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }

    - requires: [verb]
      plan:
        - pipe: "{verb}"
          flags: { topic: "{topic}" }

prompts:
  system: |
    You are an expert at creating 3Blue1Brown-style mathematical animations
    using Manim Community Edition (the `manim` Python package, NOT manimgl).

    Your job: given a concept, produce a COMPLETE, RUNNABLE Manim CE Python
    script that creates a beautiful, pedagogically clear animation.

    Rules:
    1. Output ONLY the Python code. No explanation, no commentary.
    2. Use `from manim import *` at the top.
    3. Define exactly ONE Scene subclass.
    4. The class name MUST be `GeneratedScene`.
    5. Use smooth animations: Transform, FadeIn, FadeOut, Create, Write,
       MoveToTarget, AnimationGroup. Avoid jarring cuts.
    6. Use self.wait() between logical steps so the viewer can absorb.
    7. Use color coding to distinguish mathematical objects:
       - BLUE for primary objects
       - YELLOW for highlights and emphasis
       - WHITE for text and labels
       - GREEN for positive/correct
       - RED for negative/warnings
    8. Use MathTex for mathematical expressions, Text for labels.
    9. Keep text concise — this is a visual medium, not a textbook.
    10. Build complexity gradually: start simple, add layers.
    11. Position objects deliberately — use .to_edge(), .next_to(), .shift().
    12. The animation should be self-contained and tell a complete visual story.

    Common Manim CE imports and objects you can use:
    - Geometric: Circle, Square, Rectangle, Line, Arrow, Dot, Polygon, Arc
    - Text: Text, MathTex, Tex, Title, BulletedList
    - Graphs: Axes, NumberPlane, FunctionGraph
    - Animations: Create, Write, FadeIn, FadeOut, Transform, ReplacementTransform,
      GrowFromCenter, DrawBorderThenFill, Indicate, Circumscribe
    - Groups: VGroup, AnimationGroup
    - Constants: UP, DOWN, LEFT, RIGHT, ORIGIN, PI, TAU, DEGREES

  templates:
    math: |
      Create a Manim animation that visually explains the following
      mathematical concept. Build intuition through geometric
      representations, step-by-step derivations, and visual proofs
      where possible.

      Concept: {{.Topic}}
      {{if .Content}}
      Additional context:
      {{.Content}}
      {{end}}
      {{if .Duration}}Target duration: {{.Duration}} ({{.DurationGuide}}){{end}}
      {{if .Style}}Visual style: {{.StyleGuide}}{{end}}

    concept: |
      Create a Manim animation that explains the following concept
      visually. Use analogies, diagrams, and step-by-step build-up
      to make the concept intuitive.

      Concept: {{.Topic}}
      {{if .Content}}
      Additional context:
      {{.Content}}
      {{end}}
      {{if .Duration}}Target duration: {{.Duration}} ({{.DurationGuide}}){{end}}
      {{if .Style}}Visual style: {{.StyleGuide}}{{end}}

    code: |
      Create a Manim animation that visualizes how the following
      algorithm or code works. Show data structures, pointer movements,
      state changes, and control flow visually.

      Algorithm/Code: {{.Topic}}
      {{if .Content}}
      Code or description:
      {{.Content}}
      {{end}}
      {{if .Duration}}Target duration: {{.Duration}} ({{.DurationGuide}}){{end}}
      {{if .Style}}Visual style: {{.StyleGuide}}{{end}}

    data: |
      Create a Manim animation that visualizes the following data
      or statistical concept. Use charts, graphs, distributions,
      and animated transitions to tell the data's story.

      Data/Concept: {{.Topic}}
      {{if .Content}}
      Data or context:
      {{.Content}}
      {{end}}
      {{if .Duration}}Target duration: {{.Duration}} ({{.DurationGuide}}){{end}}
      {{if .Style}}Visual style: {{.StyleGuide}}{{end}}

format:
  structured: |
    {{if .error}}Visualization failed: {{.error}}{{else}}Visualization rendered: {{.path}}
    Format: {{.format}} | Quality: {{.quality}}
    {{.description}}{{end}}
```

### 2. Create `internal/pipes/visualize/visualize.go`

Define the renderer interface, template data, prompt resolution, and handler.

**Renderer interface** (following review pipe's `DiffFetcher` pattern):

```go
// Renderer abstracts Manim execution for testability.
type Renderer interface {
    Render(ctx context.Context, code string, format string, quality string) (outputPath string, err error)
}

// ManimRenderer implements Renderer using manim CLI.
type ManimRenderer struct {
    Executor pipeutil.Executor
    OutputDir string // base directory for rendered files
}
```

**Template data struct:**

```go
type templateData struct {
    Content       string
    Topic         string
    Duration      string
    DurationGuide string // "5-15 seconds", "15-30 seconds", "30-60 seconds"
    Style         string
    StyleGuide    string // expanded style description
}
```

**Key functions to implement:**

- `var CompileTemplates = pipeutil.CompileTemplates`

- `durationGuide(duration string) string` — maps flag values to descriptive guidance: `"short"` → `"5-15 seconds, 3-5 animation steps"`, `"medium"` → `"15-30 seconds, 5-10 animation steps"`, `"long"` → `"30-60 seconds, 10-20 animation steps"`.

- `styleGuide(style string) string` — maps flag values to AI-readable descriptions: `"3b1b"` → `"Dark background (#1a1a2e or BLACK), blue/yellow/white color scheme, clean sans-serif typography, smooth transformations"`, `"light"` → `"White background, dark text, muted color palette, professional presentation style"`, `"minimal"` → `"Black background, white objects only, no color, stark geometric aesthetic"`.

- `qualityFlag(quality string) string` — maps to manim CLI flags: `"low"` → `"-ql"`, `"medium"` → `"-qm"`, `"high"` → `"-qh"`.

- `formatFlag(format string) string` — maps to manim CLI flags: `"gif"` → `"--format=gif"`, `"png"` → `"--format=png -s"` (save last frame), `"mp4"` → `""` (default).

- `preparePrompt(compiled, pipeConfig, input, flags) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError)` — extract content from input via `envelope.ContentToText`. Read `topic` from flags (fall back to content). Determine template key from `type` flag (default `"concept"`). Build `templateData` with resolved duration/style guides. Render template. Return system prompt + user prompt.

- `extractCode(response string) string` — strip markdown fences from AI response using `pipeutil.StripMarkdownFences`, then validate it contains `class GeneratedScene` and `from manim import`. Return cleaned code.

- `(r *ManimRenderer) Render(ctx, code, format, quality string) (string, error)`:
  1. Write `code` to a temp file (`os.CreateTemp("", "visualize-*.py")`)
  2. Build manim command: `manim render {qualityFlag} {formatFlag} {tempfile} GeneratedScene`
  3. Execute via `r.Executor.Execute(ctx, cmd, "")`
  4. If exit code != 0, return error with stderr
  5. Find output file in manim's media directory (parse stdout for output path, or glob `media/videos/*/GeneratedScene.*`)
  6. Return output file path

- `NewHandler(provider, pipeConfig, compiled, renderer, logger) pipe.Handler`:
  1. Create output envelope: `envelope.New("visualize", "render")`
  2. Call `preparePrompt` — return error envelope if it fails
  3. Call `provider.Complete(ctx, systemPrompt, userPrompt)` with 60s timeout for code generation
  4. Call `extractCode(response)` — if validation fails, return fatal error
  5. Call `renderer.Render(ctx, code, format, quality)` with remaining time budget
  6. On success: return `ContentStructured` envelope with `map[string]any{"path": outputPath, "format": format, "quality": quality, "description": "Visualization of: " + topic}`
  7. On render error: return retryable error (the AI might generate better code on retry)
  8. On provider error: use `envelope.ClassifyError`

**Implementation notes:**

- The pipe uses `pipeutil.Executor` interface (not raw `os/exec`) so Manim execution is fully mockable in tests.
- The handler has a two-phase timeout: 60s for AI code generation, remaining time (up to 60s more given 120s total pipe timeout) for rendering.
- `extractCode` is deliberately defensive — if the AI returns prose mixed with code, it strips fences and validates the essential structure.
- The `ManimRenderer` writes temp files to `os.TempDir()` and cleans them up after rendering. The output file in manim's media directory is the final artifact.

### 3. Create `internal/pipes/visualize/cmd/main.go`

```go
package main

import (
    "os"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipeutil"
    "github.com/justinpbarnett/virgil/internal/pipes/visualize"
)

func main() {
    logger := pipehost.NewPipeLogger("visualize")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("visualize", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("visualize", err.Error())
    }

    compiled := visualize.CompileTemplates(pc)

    renderer := &visualize.ManimRenderer{
        Executor:  &pipeutil.OSExecutor{},
        OutputDir: os.TempDir(),
    }

    logger.Info("initialized")
    pipehost.Run(
        visualize.NewHandler(provider, pc, compiled, renderer, logger),
        nil,
    )
}
```

No streaming — `pipehost.Run` with nil stream handler. Manim availability is checked at render time, not startup (the pipe can still be registered even if manim isn't installed yet — it will return a clear fatal error on first invocation).

### 4. Write tests in `internal/pipes/visualize/visualize_test.go`

**Mock types:**

```go
// MockRenderer implements visualize.Renderer for testing.
type MockRenderer struct {
    OutputPath string
    Err        error
}

func (m *MockRenderer) Render(ctx context.Context, code, format, quality string) (string, error) {
    return m.OutputPath, m.Err
}
```

**Test cases:**

- `TestCompileTemplates` — verify all four templates (`math`, `concept`, `code`, `data`) compile from a valid `PipeConfig`.
- `TestPreparePrompt_ConceptDefault` — no `type` flag. Verify `concept` template selected, topic appears in rendered prompt.
- `TestPreparePrompt_Math` — `type=math`, `topic="Fourier transform"`. Verify `math` template selected.
- `TestPreparePrompt_WithDuration` — `duration=medium`. Verify duration guide appears in rendered prompt.
- `TestPreparePrompt_WithStyle` — `style=3b1b`. Verify style guide appears in rendered prompt.
- `TestPreparePrompt_NoContent` — empty input envelope, topic from flags. Verify prompt still renders (topic used as content).
- `TestPreparePrompt_NoTopicNoContent` — empty input, no topic flag. Verify fatal error returned.
- `TestExtractCode_CleanPython` — raw Python with no fences. Verify returned as-is.
- `TestExtractCode_MarkdownFenced` — code wrapped in ````python ... ````. Verify fences stripped.
- `TestExtractCode_MissingScene` — code without `GeneratedScene`. Verify error.
- `TestExtractCode_MissingImport` — code without `from manim import`. Verify error.
- `TestDurationGuide` — verify all three flag values map to correct descriptions.
- `TestStyleGuide` — verify all three flag values map to correct descriptions.
- `TestQualityFlag` — verify `low` → `-ql`, `medium` → `-qm`, `high` → `-qh`.
- `TestFormatFlag` — verify `mp4` → `""`, `gif` → `"--format=gif"`, `png` → `"--format=png -s"`.
- `TestNewHandler_Success` — mock provider returns valid Manim code, mock renderer returns path. Verify envelope: `pipe: "visualize"`, `action: "render"`, `content_type: "structured"`, content has `path`, `format`, `quality`, `description`.
- `TestNewHandler_ProviderTimeout` — mock provider returns timeout error. Verify retryable error in envelope.
- `TestNewHandler_ProviderFatal` — mock provider returns auth error. Verify fatal error.
- `TestNewHandler_InvalidCode` — mock provider returns prose (no valid Manim code). Verify fatal error.
- `TestNewHandler_RenderFailure` — valid code, mock renderer returns error. Verify retryable error (render failures are retryable since re-generating code might fix it).
- `TestNewHandler_EnvelopeCompliance` — verify all required envelope fields present: `pipe`, `action`, `args`, `timestamp`, `duration`, `content`, `content_type`, `error`.

## Testing Strategy

### Unit Tests

- Template compilation and rendering for all prompt variants
- Prompt resolution with all flag combinations
- Code extraction and validation (fence stripping, structure validation)
- Handler integration with mock provider and mock renderer
- Error classification for both AI and render failures
- Flag mapping functions (quality, format, duration guide, style guide)

### Edge Cases

- No topic or content — fatal error, not a crash
- AI returns code with extra prose — `extractCode` strips it
- AI returns code that compiles but renders incorrectly — no way to detect, returns the output as-is
- Manim not installed — renderer returns clear error with install instructions
- LaTeX not installed — manim itself handles this (falls back or errors); the pipe surfaces manim's stderr
- Very long render time — 120s timeout kills the subprocess
- Upstream content is a list (from memory/calendar) — `ContentToText` flattens it to string context for the AI prompt
- Format flag `png` — renders a single frame (last frame), useful for static diagrams

## Risk Assessment

- **Vocabulary conflict:** `"animate"` and `"illustrate"` are not currently claimed by any pipe. `"show"` is claimed by calendar (`show: calendar`), so it is deliberately NOT mapped to visualize. If a future pipe claims `"animate"`, the config loader will catch it at startup.
- **External dependency:** Manim CE, ffmpeg, and LaTeX are system-level dependencies. Unlike other pipes (which depend only on Go stdlib or HTTP APIs), this pipe won't work without a Python environment. The pipe degrades clearly — fatal error with install instructions — but users must install manim separately.
- **AI code quality:** Generated Manim code may not always render successfully. The retryable error on render failure allows the runtime to retry (generating different code), but success is not guaranteed. The system prompt is heavily constrained to produce valid Manim CE code, but this will need iteration.
- **File system artifacts:** Rendered files accumulate in the temp directory. No cleanup mechanism is implemented in v1. A future improvement could add a cleanup sweep or use a dedicated output directory with rotation.
- **Security:** The pipe executes AI-generated Python code via `manim render`. Manim runs in a Python process with full system access. This is an inherent risk of code generation + execution. Mitigation: run manim in a sandboxed environment (future work), and constrain the system prompt to produce only Manim API calls.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go test ./internal/pipes/visualize/... -v -count=1
go build ./internal/pipes/visualize/cmd/
```

## Open Questions (Unresolved)

- **Sandbox execution:** AI-generated code executed on the host is a security surface. Should the pipe require manim to run inside a container or sandbox? **Recommendation:** Ship v1 without sandboxing — the pipe already only runs on the user's local machine for their own content. Add a `sandbox` flag or config option in a follow-up if the pipe is exposed to untrusted input.

- **Output directory management:** Where should rendered files live? `os.TempDir()` means files are cleaned up on reboot but could accumulate during a session. A dedicated `~/.config/virgil/visualize/` output directory would be more predictable. **Recommendation:** Use `os.TempDir()` for v1. The structured output includes the full path, so the user can find and manage the files. Add configurable output directory later.

- **Retry strategy for bad code:** When the AI generates code that fails to render, should the pipe include the manim error in a retry prompt to help the AI fix the code? This would require the runtime to support "retry with modified input" semantics. **Recommendation:** For v1, return a retryable error and let the runtime retry with the same input. The AI will generate different code due to non-determinism. Error-informed retry is a good v2 improvement.

- **`type` flag inference:** The pipe has prompt templates keyed by type (`math`, `concept`, `code`, `data`), but the user may not specify a type. Should the pipe use the AI to classify the topic into a type before generating code, or default to `concept`? **Recommendation:** Default to `concept`. The concept template is general enough to handle most inputs. Users who want specific treatment can pass `--type=math` explicitly. AI-based classification adds latency and complexity for marginal benefit.

## Sub-Tasks

Single task — no decomposition needed.
