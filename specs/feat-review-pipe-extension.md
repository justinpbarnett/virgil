# Feature: Review Pipe Extension — PR Diff Source, Dev Findings Schema & Completeness Criteria

## Metadata

type: `feat`
task_id: `review-pipe-extension`
prompt: `Extend the existing review pipe with three capabilities: (1) a pr-diff source mode that fetches diffs via gh CLI, (2) a dev-review criteria with a richer findings schema (category/severity/file/line/issue/action) for build pipe rework cycles, and (3) a completeness criteria for spec readiness gating with a needs-revision outcome. Existing review functionality is unchanged.`

## Feature Description

The existing review pipe reviews arbitrary content against criteria (correctness, style, test-coverage, spec-compliance). It works well for general-purpose evaluation. Two pipelines need extensions:

The `dev-feature` pipeline needs:

1. **PR-diff source mode.** Instead of receiving content in the input envelope, the review pipe fetches the PR diff directly from GitHub via `gh pr diff`. This enforces the trust boundary: the reviewer sees only the diff, never touches the codebase.

2. **Dev-review criteria with richer findings schema.** The pipeline needs findings with `category` (architecture, logic, testing, style, security), `severity` (major, minor, nit), `file`, `line`, `issue`, and `action` fields. The current schema uses `severity`, `location`, `description`, `suggestion`. The new schema is more structured and actionable.

The `spec` pipeline needs:

3. **Completeness criteria with needs-revision outcome.** Evaluates whether a specification document is ready for implementation handoff. The completeness review is deliberately adversarial: its job is to find what's missing, what's contradictory, and what a developer (or dev pipeline) would get stuck on. It checks for unresolved questions, vague scope boundaries, missing interface contracts, untestable acceptance criteria, and internal contradictions. Introduces a third outcome — `needs-revision` — for specs with issues fixable without user input (vague language, missing inferrable details). This is distinct from `fail` (user input needed).

All three extensions are additive. The existing review functionality — criteria modes, strictness, format — is unchanged.

## User Story

As a pipeline step
I want to review content from multiple sources against specialized criteria with actionable findings
So that downstream pipes can act on structured review output in automated workflows

## Relevant Files

### Existing Files (Modified)

- `internal/pipes/review/pipe.yaml` — add `source` flag, add `dev-review` and `completeness` criteria templates, add `DevFinding` schema to prompts, extend format template for `needs-revision`
- `internal/pipes/review/review.go` — add `source: pr-diff` handling, add `DevReviewResult` type, add `dev-review` parsing, extend `parseReviewResult` to accept `needs-revision`
- `internal/pipes/review/review_test.go` — add tests for new source mode, both criteria, and outcome values

### No New Files

This is an extension to an existing pipe, not a new pipe.

## Implementation Plan

### Phase 1: Schema Extension

Add the `DevReviewResult` and `DevFinding` types alongside the existing `ReviewResult` and `Finding` types. Extend `parseReviewResult` to accept `needs-revision` as a valid outcome. The handler selects which result type to parse based on the criteria flag.

### Phase 2: PR-Diff Source

Add the `source` flag to `pipe.yaml`. When `source: pr-diff`, the handler extracts the PR URL/number from the input envelope and fetches the diff via `gh pr diff`.

### Phase 3: Dev-Review & Completeness Prompts

Add the `dev-review` and `completeness` criteria templates to `pipe.yaml`.

### Phase 4: Tests

Test the new source mode, both criteria templates, findings parsing, and outcome validation.

## Step by Step Tasks

### 1. Add `DevReviewResult` and `DevFinding` types

In `internal/pipes/review/review.go`, add alongside existing types:

```go
type DevReviewResult struct {
    Outcome  string       `json:"outcome"`
    Summary  string       `json:"summary"`
    Findings []DevFinding `json:"findings"`
}

type DevFinding struct {
    Category string `json:"category"` // architecture, logic, testing, style, security
    Severity string `json:"severity"` // major, minor, nit
    File     string `json:"file"`
    Line     int    `json:"line"`
    Issue    string `json:"issue"`
    Action   string `json:"action"`
}
```

Add a parser:

```go
func parseDevReviewResult(raw string) (DevReviewResult, error) {
    cleaned := stripMarkdownFences(raw)
    var result DevReviewResult
    if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
        return DevReviewResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
    }
    if result.Outcome != "pass" && result.Outcome != "fail" {
        return DevReviewResult{}, fmt.Errorf("invalid outcome: %q", result.Outcome)
    }
    if result.Findings == nil {
        result.Findings = []DevFinding{}
    }
    // Validate finding fields
    for i, f := range result.Findings {
        if !isValidCategory(f.Category) {
            return DevReviewResult{}, fmt.Errorf("finding %d: invalid category %q", i, f.Category)
        }
        if !isValidDevSeverity(f.Severity) {
            return DevReviewResult{}, fmt.Errorf("finding %d: invalid severity %q", i, f.Severity)
        }
    }
    return result, nil
}

func isValidCategory(c string) bool {
    switch c {
    case "architecture", "logic", "testing", "style", "security":
        return true
    }
    return false
}

func isValidDevSeverity(s string) bool {
    switch s {
    case "major", "minor", "nit":
        return true
    }
    return false
}
```

### 2. Extend `parseReviewResult` for `needs-revision` outcome

Update `parseReviewResult` to accept `needs-revision` as a valid outcome:

```go
func parseReviewResult(raw string) (ReviewResult, error) {
    cleaned := stripMarkdownFences(raw)
    var result ReviewResult
    if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
        return ReviewResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
    }
    switch result.Outcome {
    case "pass", "fail", "needs-revision":
        // valid
    default:
        return ReviewResult{}, fmt.Errorf("invalid outcome: %q (must be pass, fail, or needs-revision)", result.Outcome)
    }
    if result.Findings == nil {
        result.Findings = []Finding{}
    }
    return result, nil
}
```

This replaces the current `if result.Outcome != "pass" && result.Outcome != "fail"` check.

### 3. Add PR-diff source support

In `internal/pipes/review/review.go`, add a diff fetcher:

```go
type DiffFetcher interface {
    FetchPRDiff(ctx context.Context, prIdentifier string) (string, error)
}

type GHDiffFetcher struct{}

func (f *GHDiffFetcher) FetchPRDiff(ctx context.Context, prIdentifier string) (string, error) {
    cmd := exec.CommandContext(ctx, "gh", "pr", "diff", prIdentifier)
    out, err := cmd.Output()
    if err != nil {
        return "", fmt.Errorf("gh pr diff: %w", err)
    }
    return string(out), nil
}
```

Modify `NewHandler` to accept an optional `DiffFetcher`:

```go
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
    return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), &GHDiffFetcher{}, logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, fetcher DiffFetcher, logger *slog.Logger) pipe.Handler {
```

In the handler function, before `preparePrompt`:

```go
// If source is pr-diff, fetch the diff and use it as content
source := flags["source"]
if source == "pr-diff" {
    prID := extractPRIdentifier(input, flags)
    if prID == "" {
        out.Error = envelope.FatalError("source=pr-diff but no PR identifier found")
        out.Duration = time.Since(out.Timestamp)
        return out
    }
    diffContent, err := fetcher.FetchPRDiff(ctx, prID)
    if err != nil {
        out.Error = envelope.ClassifyError("fetch PR diff", err)
        out.Duration = time.Since(out.Timestamp)
        return out
    }
    // Override input content with the fetched diff
    input.Content = diffContent
    input.ContentType = envelope.ContentText
}
```

Add `extractPRIdentifier` helper:

```go
func extractPRIdentifier(input envelope.Envelope, flags map[string]string) string {
    // Check flags first
    if pr := flags["pr"]; pr != "" {
        return pr
    }
    // Check input envelope for structured content with pr_url or pr_number
    if m, ok := input.Content.(map[string]any); ok {
        if url, ok := m["pr_url"].(string); ok && url != "" {
            return url
        }
        if num, ok := m["pr_number"]; ok {
            return fmt.Sprintf("%v", num)
        }
    }
    return ""
}
```

### 4. Update `pipe.yaml`

Add the following to `internal/pipes/review/pipe.yaml`:

**New flags** (add after existing `format` flag):

```yaml
  source:
    description: Where to get content from. Empty uses envelope content. pr-diff fetches from GitHub.
    values: ["", pr-diff]
    default: ""

  pr:
    description: PR number or URL when source is pr-diff.
    default: ""
```

**Updated criteria values** — add `dev-review` and `completeness`:

```yaml
  criteria:
    description: What to evaluate against.
    values: [correctness, style, test-coverage, spec-compliance, dev-review, completeness]
    default: correctness
```

**Updated format template** — handle `needs-revision` outcome:

```yaml
format:
  structured: |
    {{if eq .outcome "pass"}}PASS{{else if eq .outcome "needs-revision"}}NEEDS REVISION{{else}}FAIL{{end}}: {{.summary}}{{if .findings}}
    {{range .findings}}
    [{{.severity}}] {{if .location}}{{.location}}: {{end}}{{.description}}{{if .suggestion}}
      → {{.suggestion}}{{end}}
    {{end}}{{end}}
```

**Updated system prompt** — add `needs-revision` to the outcome schema and rules:

In the JSON schema comment, change:
```
"outcome": "pass" | "fail",
```
to:
```
"outcome": "pass" | "fail" | "needs-revision",
```

Add to the rules section:
```
- outcome is "needs-revision" when issues exist that can be fixed without user input
- "needs-revision" is only valid for the completeness criteria
```

**New prompt templates** — add both in the `prompts.templates` section:

```yaml
    dev-review: |
      Review the following pull request diff for a feature implementation.

      Evaluate:
      - Architectural fit — is code in the right layers and packages?
      - Logic correctness — are there bugs, race conditions, or wrong assumptions?
      - Testing quality — are tests comprehensive, do they cover edge cases?
      - Style consistency — does new code match existing project conventions?
      - Security — are there injection risks, auth bypasses, or data leaks?

      PR Diff:
      {{.Content}}

      {{if .Topic}}Feature spec or context: {{.Topic}}{{end}}

      You MUST respond with valid JSON matching this exact schema:

      {
        "outcome": "pass" | "fail",
        "summary": "one-sentence overall assessment",
        "findings": [
          {
            "category": "architecture" | "logic" | "testing" | "style" | "security",
            "severity": "major" | "minor" | "nit",
            "file": "path/to/file",
            "line": 42,
            "issue": "what the issue is",
            "action": "what to do about it"
          }
        ]
      }

      Rules:
      - outcome is "fail" if ANY finding has severity "major"
      - outcome is "pass" if all findings are "minor" or "nit" (or no findings)
      - findings array is always present, even if empty
      - every major and minor finding MUST have an action
      - nit findings are style observations — actions are optional
      - order findings: major first, then minor, then nit
      - file paths must match exactly what appears in the diff
      - line numbers refer to the NEW file (post-change), not the old file
      - respond ONLY with JSON

    completeness: |
      Review this specification document for implementation readiness.

      This spec will be handed to a development pipeline that cannot ask
      clarifying questions. If the spec is ambiguous, the pipeline will
      guess wrong. Find the ambiguities.

      Spec:
      {{.Content}}

      {{if .Topic}}Additional context: {{.Topic}}{{end}}

      Check for:
      1. Open questions that are still unresolved
      2. Scope boundaries that are vague ("as needed", "if applicable",
         "where appropriate") — these MUST be resolved to concrete decisions
      3. Missing interface contracts (what goes in, what comes out)
      4. Implementation steps that skip details a developer would need
      5. Risks without mitigation strategies
      6. Acceptance criteria that aren't testable (subjective language
         like "should work well" or "performs adequately")
      7. Internal contradictions between sections
      8. Implicit assumptions that should be explicit

      Use outcome "pass" if the spec is ready for implementation.
      Use outcome "fail" if the spec needs answers from the user (unresolved
      open questions, missing requirements that only the user can provide).
      Use outcome "needs-revision" if the spec has fixable issues — vague
      language, missing details that can be inferred, structural problems
      — that don't require user input to resolve.

      For findings:
      - "error" severity for issues that would block implementation
      - "warning" for issues that could cause confusion or rework
      - "info" for suggestions that would improve clarity
```

**New vocabulary** — add `pr-diff` to sources:

```yaml
  sources:
    pr-diff: pr-diff
    diff: pr-diff
    pr: pr-diff
```

### 5. Modify result parsing in the handler

In the handler, after getting the raw provider response, select the parser based on criteria:

```go
criteria := flags["criteria"]
if criteria == "dev-review" {
    result, err := parseDevReviewResult(raw)
    if err != nil {
        // ...error handling...
    }
    out.Content = result
} else {
    result, err := parseReviewResult(raw)
    if err != nil {
        // ...error handling...
    }
    out.Content = result
}
```

### 6. Update the `preparePrompt` function

The existing `preparePrompt` compiles templates by name from `pipeConfig.Prompts.Templates`, so adding the `dev-review` and `completeness` templates to `pipe.yaml` works automatically. Both use `{{.Content}}` and `{{.Topic}}`, same as existing templates.

### 7. Write tests

Add to `internal/pipes/review/review_test.go`:

**PR-diff source tests:**
- **TestReviewPRDiffSource** — `source: pr-diff`, `pr: 47`. MockDiffFetcher returns a diff string. Verify the diff is used as content in the prompt.
- **TestReviewPRDiffNoPR** — `source: pr-diff` but no PR identifier. Returns fatal error.
- **TestReviewPRDiffFetchError** — `gh pr diff` fails. Returns classified error.
- **TestReviewPRDiffFromEnvelope** — no `pr` flag but input envelope has `pr_url` in structured content. Extracts PR identifier.

**Dev-review criteria tests:**
- **TestReviewDevReviewPass** — provider returns pass with no findings. Verify outcome is "pass", findings is empty array.
- **TestReviewDevReviewFail** — provider returns fail with major findings. Verify `DevReviewResult` with correct fields.
- **TestReviewDevReviewParsing** — verify `parseDevReviewResult` handles valid JSON with all categories and severities.
- **TestReviewDevReviewInvalidCategory** — finding with unknown category, returns parse error.
- **TestReviewDevReviewInvalidSeverity** — finding with unknown severity, returns parse error.
- **TestReviewDevReviewMalformedJSON** — provider returns invalid JSON, returns fatal error.

**Completeness criteria tests:**
- **TestParseReviewResult_NeedsRevision** — JSON with `"outcome": "needs-revision"` parses correctly.
- **TestParseReviewResult_ValidOutcomes** — table-driven test confirming `pass`, `fail`, and `needs-revision` all parse.
- **TestParseReviewResult_InvalidOutcome** — `"outcome": "maybe"` still returns error.
- **TestPreparePrompt_Completeness** — criteria=completeness selects the completeness template, content is populated.
- **TestNewHandler_Completeness** — mock provider returns needs-revision JSON, verify envelope has structured content with correct outcome.

**Existing tests unchanged** — verify all existing tests still pass (no regressions).

### 8. Update subprocess entry point

The existing entry point calls `review.NewHandler(provider, pc, logger)`. This internally calls `NewHandlerWith` with a default `GHDiffFetcher`. No change needed to `cmd/main.go` — the fetcher is injected internally.

## Testing Strategy

### Unit Tests
- `internal/pipes/review/review_test.go` — PR-diff source, dev-review criteria, completeness criteria, parsing, outcome validation, error handling

### Edge Cases
- PR diff is empty (no changes — should this be a pass or an error?)
- PR diff is very large (exceeds provider context — should truncate with a note?)
- `gh` CLI not installed (clear error message)
- `gh` not authenticated (clear error message)
- Dev-review finds nit-only issues (outcome should be "pass", not "fail")
- Finding has line 0 (valid — some issues are file-level, not line-level)
- `needs-revision` outcome with no findings — technically valid but unusual
- Completeness review on a very short spec — should produce `fail` with many gaps
- Existing criteria unaffected — verify `correctness` review still works with the expanded outcome validation (accepts `needs-revision` for all criteria; trust the prompt to use it only for completeness)

## Risk Assessment

- **Low risk for existing functionality.** All changes are additive. The new `source` flag defaults to empty (existing behavior). The new criteria templates exist alongside existing ones. The `needs-revision` outcome is accepted but only prompted for in the completeness template.
- **`gh` CLI dependency matches publish pipe.** Both publish and review-with-pr-diff depend on `gh`. This is a pipeline-level prerequisite, not a per-pipe surprise.
- **Dev-review prompt quality determines pipeline effectiveness.** The reviewer must produce findings specific enough for the builder to act on. Vague findings like "consider improving" break the rework cycle. The prompt strongly constrains the output format.
- **Completeness prompt must reliably distinguish `fail` from `needs-revision`.** `fail` means user input needed. `needs-revision` means fixable without user. This is a prompt engineering concern. Test with representative spec documents.
- **Two result types adds parsing complexity.** The handler must select between `parseReviewResult` and `parseDevReviewResult` based on criteria. Manageable but adds a branch. If more criteria types need custom schemas later, consider a parser registry.

## Validation Commands

```bash
go test ./internal/pipes/review/... -v -count=1
go build ./internal/pipes/review/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
