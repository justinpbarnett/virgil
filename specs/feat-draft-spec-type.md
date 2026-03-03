# Feature: Draft Pipe — Spec Type

## Metadata

type: `feat`
task_id: `draft-spec-type`
prompt: `Extend the draft pipe with a spec content type. Add create and update prompt templates, expand the template data struct to include state and codebase context fields, and add spec to the type flag values and vocabulary.`

## Feature Description

The draft pipe already handles blog, email, pr, memo, and summary content types. Adding `spec` makes it capable of producing and updating structured implementation specification documents.

The spec type differs from existing types in two ways:

1. **It has two templates** — `spec-create` and `spec-update` — selected by a compound key of `type` + `phase`. On create, it generates a new spec document from analysis output. On update, it surgically modifies an existing spec document based on new analysis, preserving prior decisions.

2. **Its template data needs additional fields** — `State` (the current spec document for updates) and `CodebaseContext` (relevant code context from study). The existing `templateData` struct has `Content`, `Topic`, `Tone`, and `Length`.

The code changes are minimal. The draft pipe's template dispatch already handles unknown types gracefully (falls back to raw content). Adding a new type is: add the flag value, add prompt templates, expand the template data struct.

## User Story

As a Virgil pipeline
I want the draft pipe to produce and update spec documents
So that the spec pipeline can use existing infrastructure instead of a bespoke drafting pipe

## Relevant Files

- `internal/pipes/draft/pipe.yaml` — add `spec` to type values, add `phase` flag, add vocabulary, add prompt templates
- `internal/pipes/draft/draft.go` — expand `templateData` struct, update `preparePrompt` to populate new fields and handle compound template keys
- `internal/pipes/draft/draft_test.go` — tests for new template selection and data population

### New Files

None.

## Implementation Plan

### Phase 1: Config

Extend `pipe.yaml` with the spec type, phase flag, and prompt templates.

### Phase 2: Code

Expand template data and template selection logic to support compound keys.

### Phase 3: Tests

Add test cases for spec-create and spec-update template paths.

## Step by Step Tasks

### 1. Update `internal/pipes/draft/pipe.yaml`

Add `spec` to the type flag values list:

```yaml
flags:
  type:
    description: What kind of content to produce.
    values: [blog, email, pr, memo, spec]
    default: ""
  phase:
    description: Create new content or update existing. Only used by spec type.
    values: [create, update]
    default: create
  # tone and length remain unchanged
```

Add vocabulary entry:

```yaml
vocabulary:
  verbs:
    # existing entries unchanged
    spec: draft
    specify: draft
  types:
    # existing entries unchanged
    spec: spec
```

Add prompt templates — note the compound keys `spec-create` and `spec-update`:

```yaml
prompts:
  templates:
    # existing templates (blog, email, pr, memo, summary) unchanged

    spec-create: |
      Create an implementation specification document from this analysis.

      Analysis:
      {{.Content}}

      {{if .CodebaseContext}}
      Codebase context:
      {{.CodebaseContext}}
      {{end}}

      Write for builders. No fluff, no motivation sections, no business
      justification. The document must include: Summary, Scope (in/out/boundary
      decisions), Architecture (approach, components, interfaces), Implementation
      Plan (ordered steps, file-level scope), Risks (with mitigations),
      Decisions (empty initially), Open Questions (each with why-it-matters
      and known options), and Acceptance Criteria (testable yes/no questions).

      Mark all open questions explicitly. Mark uncertain assumptions with [?].
      Implementation steps must be independently verifiable.

    spec-update: |
      Update this specification document with new analysis results.

      Current spec:
      {{.State}}

      New analysis (resolved questions, implications, updated risks):
      {{.Content}}

      Rules:
      - Move resolved questions from Open Questions to Decisions with rationale
      - Update affected sections (scope, implementation plan, risks)
      - Add any new open questions
      - Do not remove or modify decisions from previous rounds
      - If a new decision contradicts a previous one, flag it explicitly
      - Keep the document structure intact — update sections surgically
```

### 2. Expand `templateData` in `internal/pipes/draft/draft.go`

Add two fields to the struct:

```go
type templateData struct {
    Content         string
    Topic           string
    Tone            string
    Length          string
    State           string // existing document for updates
    CodebaseContext string // relevant code context
}
```

### 3. Update `preparePrompt` in `internal/pipes/draft/draft.go`

Two changes:

**a) Populate new fields from flags:**

After the existing content extraction and before `executeTemplate`, add:

```go
data := templateData{
    Content:         content,
    Topic:           flags["topic"],
    Tone:            flags["tone"],
    Length:          flags["length"],
    State:           flags["state"],
    CodebaseContext: flags["context"],
}
```

Currently the `templateData` is constructed inline in the `executeTemplate` call. Move it to a local variable so the new fields can be set.

**b) Handle compound template keys:**

Update `executeTemplate` (or the call site) to try a compound key first, then fall back to the simple key. When `flags["type"]` is `"spec"` and `flags["phase"]` is `"update"`, look up `"spec-update"`. If not found, fall back to `"spec"`.

In `preparePrompt`, compute the template key:

```go
templateKey := flags["type"]
if phase := flags["phase"]; phase != "" && flags["type"] != "" {
    compound := flags["type"] + "-" + phase
    if _, ok := compiled[compound]; ok {
        templateKey = compound
    }
}
```

Then pass `templateKey` to `executeTemplate` instead of `flags["type"]`.

### 4. Update tests in `internal/pipes/draft/draft_test.go`

Add test cases:

- `TestPreparePrompt_SpecCreate` — type=spec, phase=create (or phase absent, defaults to create). Verify `spec-create` template is selected, content and codebase context are populated in the prompt.
- `TestPreparePrompt_SpecUpdate` — type=spec, phase=update. Verify `spec-update` template is selected, state field is populated in the prompt.
- `TestPreparePrompt_CompoundKeyFallback` — type=spec, phase=nonexistent. Verify falls back to `spec` key, then to raw content.
- `TestPreparePrompt_NonSpecPhaseIgnored` — type=blog, phase=update. Verify `blog` template is used (compound key `blog-update` doesn't exist, falls back to `blog`).
- `TestTemplateData_NewFields` — verify `State` and `CodebaseContext` are accessible in templates via `{{.State}}` and `{{.CodebaseContext}}`.

## Testing Strategy

### Unit Tests

- Template selection with compound keys
- Template data population with new fields
- Fallback behavior when compound key doesn't exist
- Existing types still work identically (regression)

### Edge Cases

- `phase` flag set but `type` is empty — compound key is `-update`, which won't match anything, falls through to normal behavior
- `state` flag is empty on spec-update — template renders with empty state section, which is fine (the `{{if}}` guard handles it)
- Very large state document — no truncation in draft, that's the pipeline's job

## Validation Commands

```bash
go test ./internal/pipes/draft/... -v -count=1
go build ./internal/pipes/draft/cmd/
```
