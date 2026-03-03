# Feature: Pipe-Level Format Templates

## Metadata

type: `feat`
task_id: `pipe-format-templates`
prompt: `Replace the removed universal chat synthesis with per-pipe format templates declared in pipe.yaml. The runtime applies them at the terminal boundary — only when the envelope is the last in a pipeline and its content_type isn't already text. Pipes never format their own output; they return structured data as before. Formatting is a presentation concern owned by the runtime at the boundary, using templates declared by the pipe.`

## Feature Description

With the removal of `ensureSynthesis` (which appended a chat pipe to every pipeline), raw pipe output now reaches the client unformatted. A calendar lookup returns a JSON list of events; a memory retrieval returns a numbered dump of entries. These are correct data in an ugly shape.

The fix is per-pipe format templates: an optional `format` section in `pipe.yaml` that declares Go `text/template` strings keyed by `content_type`. The runtime applies the matching template only when the envelope is terminal (last step in the pipeline) and the content type isn't already `text`. The pipe handler code doesn't change — formatting is metadata the runtime reads, not logic the pipe executes.

This preserves composition: mid-pipeline envelopes carry full structured data. Downstream pipes see lists, not prose. Formatting happens at the boundary, not inline.

## User Story

As a Virgil user
I want pipe output to read like natural language when it's shown to me
So that I get human-readable responses without losing structured data for downstream pipes

## Relevant Files

- `internal/config/config.go` — `PipeConfig` struct where the `format` field will be added, plus YAML loading
- `internal/config/config_test.go` — Tests for config loading; needs test for format field parsing
- `internal/runtime/runtime.go` — `Execute` and `ExecuteStream` methods where terminal formatting will be applied
- `internal/runtime/runtime_test.go` — Tests for runtime execution
- `internal/envelope/envelope.go` — `ContentType` constants and `ContentToText` function used as fallback
- `internal/pipe/pipe.go` — `Definition` struct (does not need format — format is a config/presentation concern, not a routing concern)
- `internal/pipes/calendar/pipe.yaml` — First pipe to get a format template (list of events)
- `internal/pipes/memory/pipe.yaml` — Second pipe to get a format template (list of entries on retrieve, text on store)
- `internal/pipes/calendar/calendar.go` — Reference for understanding calendar output shape (`[]Event` with Title, Start, End, Location)
- `internal/pipes/memory/memory.go` — Reference for understanding memory output shape (`[]store.Entry` on retrieve)
- `internal/store/store.go` — `Entry` struct definition
- `internal/server/api.go` — Where the runtime result is serialized to the client; no changes needed (formatting happens before this)
- `internal/tui/pipe.go` — Pipe mode uses `ContentToText`; will benefit from pre-formatted text envelopes
- `docs/pipe.md` — Pipe specification reference; needs a new "Format" section documenting the `format` field in pipe.yaml, and updates to the checklist and examples

### New Files

- `internal/runtime/format.go` — Format template compilation and application logic
- `internal/runtime/format_test.go` — Tests for format template rendering

## Implementation Plan

### Phase 1: Foundation

Add the `format` field to `PipeConfig` and pipe the format templates from config into the runtime. The format field is a `map[string]string` keyed by content type (e.g., `"list"`, `"structured"`), with values being Go `text/template` strings.

### Phase 2: Core Implementation

Build the formatting engine in the runtime package. Compile format templates at startup. Apply them at the terminal boundary: after the last step executes, if the envelope's `content_type` has a matching format template, render it and replace `Content` with the rendered string, setting `ContentType` to `"text"`.

### Phase 3: Integration

Write format templates for calendar and memory pipes. Verify end-to-end that formatted output reaches the TUI and pipe mode correctly, while mid-pipeline envelopes remain unformatted.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add `Format` field to `PipeConfig`

- In `internal/config/config.go`, add `Format map[string]string \`yaml:"format"\`` to the `PipeConfig` struct (after `Prompts`)
- No changes to `ToDefinition()` — format is not part of `pipe.Definition` (it's a presentation concern, not a routing/identity concern)

### 2. Thread format templates into the runtime

- In `internal/runtime/runtime.go`, add a `formats` field to the `Runtime` struct: `formats map[string]map[string]*template.Template` (pipe name → content type → compiled template)
- Add a `NewWithFormats` constructor (or extend `New`/`NewWithLevel`) that accepts `map[string]map[string]string` (raw format strings from config) and compiles them into `*template.Template` at startup
- Update `internal/server/server.go` to extract format maps from `config.Pipes` and pass them when constructing the runtime

### 3. Build the formatting engine

- Create `internal/runtime/format.go` with:
  - `compileFormats(raw map[string]map[string]string) (map[string]map[string]*template.Template, error)` — compiles all format template strings, returns error on invalid templates
  - `formatTerminal(env envelope.Envelope, pipe string, formats map[string]map[string]*template.Template) envelope.Envelope` — if `env.ContentType` is `"text"`, return unchanged; look up `formats[pipe][env.ContentType]`; if found, execute template against `env.Content`, set `env.Content` to rendered string, set `env.ContentType` to `"text"`; if not found, return unchanged
  - Template data preparation: convert `env.Content` into a map suitable for template execution. For `content_type: list`, provide `.Items` (the slice), `.Count` (length), and `.Signal` (from `env.Args["signal"]`). For `content_type: structured`, provide the map fields directly plus `.Signal`.

### 4. Apply formatting at the terminal boundary

- In `runtime.Execute()`, after the loop completes (line 108 area), call `formatTerminal` on the final `current` envelope using the last step's pipe name
- In `runtime.ExecuteStream()`, apply `formatTerminal` in the non-streaming fallback path (after the loop, line 146 area). For the streaming path (line 128 area), apply formatting to the result envelope returned by the stream handler — streaming pipes (chat, draft) already produce `content_type: text`, so this is effectively a no-op but keeps the contract consistent
- Do NOT format mid-pipeline envelopes. Only the return value of `Execute`/`ExecuteStream` gets formatted.

### 5. Write format templates for calendar pipe

- In `internal/pipes/calendar/pipe.yaml`, add:
  ```yaml
  format:
    list: |
      {{if eq .Count 0}}Your calendar is clear.{{else}}You have {{.Count}} event{{if gt .Count 1}}s{{end}} today:{{range .Items}}
      - {{.title}} at {{.start}}{{if .end}} - {{.end}}{{end}}{{if .location}} ({{.location}}){{end}}{{end}}{{end}}
  ```
- Note: template accesses fields via lowercase JSON keys since `env.Content` arrives as `[]any` (from JSON unmarshaling in subprocess path) or `[]Event` structs (from in-process handlers). The template data preparation step (task 3) must normalize both cases.

### 6. Write format templates for memory pipe

- In `internal/pipes/memory/pipe.yaml`, add:
  ```yaml
  format:
    list: |
      {{if eq .Count 0}}No memories found.{{else}}Found {{.Count}} memor{{if eq .Count 1}}y{{else}}ies{{end}}:{{range .Items}}
      - {{.content}}{{end}}{{end}}
  ```
- Memory store action returns `content_type: text` ("Remembered: ..."), so no format template needed for that path.

### 7. Update `docs/pipe.md`

- Add a new **Format (Optional)** section between "Templates" (line 159) and "Provider (Non-Deterministic Pipes Only)" (line 188). Document:
  - Purpose: declares Go `text/template` strings keyed by `content_type` that the runtime applies at the terminal boundary — only when the envelope is the last in a pipeline and its `content_type` isn't already `text`
  - The pipe handler never formats; formatting is metadata the runtime reads, not logic the pipe executes
  - Schema: `format:` map with content type keys (`list`, `structured`) and template string values
  - Template data available: `.Items` (the content slice for lists), `.Count` (length), `.Signal` (original user input from `env.Args["signal"]`); for structured content, map fields are available directly plus `.Signal`
  - Show the calendar pipe's format template as the example
  - Explain that `content_type: text` is never formatted (already human-readable), and mid-pipeline envelopes are never formatted (preserves composition)
- Update the **Deterministic Pipes > Example: `calendar`** section (line 507 area) to include the `format:` section in the example pipe.yaml
- Update the **Checklist** (line 833 area) to add under the "Definition" group:
  ```
  ☐  (optional) format templates declared for non-text content types
  ```
- Update the **Composition Rules > Content Type Conventions** table or surrounding text to note that pipes producing `list` or `structured` content should consider declaring format templates so the runtime can present them as readable text at the terminal boundary

### 8. Write tests for format template compilation and rendering

- Create `internal/runtime/format_test.go` with tests:
  - `TestCompileFormats` — valid templates compile without error
  - `TestCompileFormatsInvalid` — malformed template returns error
  - `TestFormatTerminalList` — list content gets formatted using template
  - `TestFormatTerminalText` — text content passes through unchanged
  - `TestFormatTerminalNoTemplate` — content type with no matching template passes through unchanged
  - `TestFormatTerminalEmptyList` — empty list hits the zero-count branch
  - `TestFormatTerminalUnknownPipe` — unknown pipe name passes through unchanged

### 9. Update runtime tests

- In `internal/runtime/runtime_test.go`, add tests verifying:
  - Terminal envelope gets formatted when format templates are configured
  - Mid-pipeline envelopes are NOT formatted (multi-step plan where first step returns a list)
  - Plans with no format templates work identically to before

### 10. Update integration tests

- In `internal/integration_test.go`, update assertions for scenarios 1-3:
  - `TestIntegration_CalendarToday` — `result.ContentType` should be `"text"` (formatted from list), content should contain human-readable schedule text
  - `TestIntegration_MemoryStore` — `result.ContentType` should be `"text"` (already text from store action, no format needed)
  - `TestIntegration_MemoryRetrieve` — `result.ContentType` should be `"text"` (formatted from list)
  - `TestIntegration_DraftFromNotes` — `result.ContentType` should be `"text"` (draft pipe already produces text), verify calendar list mid-pipeline is NOT formatted

### 11. Update config test

- In `internal/config/config_test.go`, add a test that loads a pipe.yaml with a `format` section and verifies the `Format` map is populated correctly on `PipeConfig`.

## Testing Strategy

### Unit Tests

- `internal/runtime/format_test.go` — Template compilation, rendering for each content type, edge cases (empty lists, missing templates, unknown pipes, text passthrough)
- `internal/runtime/runtime_test.go` — Terminal vs mid-pipeline formatting, plans with and without format templates
- `internal/config/config_test.go` — Format field parsing from YAML

### Edge Cases

- Empty list content (`[]` with count 0) — template should render the zero-count branch
- `content_type: text` at terminal — no formatting applied regardless of template existence
- Pipe with no format section — passes through unchanged (backwards compatible)
- Multi-step pipeline where intermediate step produces a list — must NOT be formatted
- Subprocess path where `Content` arrives as `[]any` (JSON-decoded) vs in-process path where `Content` is `[]Event` — template data prep must handle both
- Format template references a field that doesn't exist on the content — should fail gracefully (Go templates return `<no value>` by default, or error with `option("missingkey=zero")`)

## Risk Assessment

- **Low risk to existing pipes:** Format templates are additive. Pipes without a `format` section behave exactly as before. The only behavior change is for pipes that declare templates, and only at the terminal boundary.
- **Template data normalization is the hardest part:** `env.Content` can be a Go struct (in-process handlers), a `[]any` of `map[string]any` (subprocess JSON decoding), or a string. The template data preparation function must handle all cases. Use `json.Marshal` → `json.Unmarshal` into `map[string]any` as a normalization strategy for struct content.
- **Streaming pipes unaffected:** Chat and draft already produce `content_type: text`. The format step is a no-op for them.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go test ./... -v -count=1
go build ./...
```

## Open Questions (Unresolved)

1. **Should format templates use `text/template` or a simpler interpolation?** Recommendation: Use Go `text/template` — it's already used in the draft pipe's prompt templates, so the pattern is established. Set `Option("missingkey=zero")` to avoid crashes on missing fields.

2. **Should the formatted text be stored in a separate envelope field (e.g., `FormattedContent`) instead of replacing `Content`?** Recommendation: Replace `Content` and set `ContentType` to `"text"`. The structured data has already been consumed by the pipeline. At the terminal boundary, the client needs presentable text. If a future client wants raw data, it can request it via an API parameter (e.g., `?raw=true`), but that's a separate concern for later.

## Sub-Tasks

Single task — no decomposition needed.
