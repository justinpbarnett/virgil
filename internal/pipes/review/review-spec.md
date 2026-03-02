# Review Pipe Spec

## Metadata

type: `feat`
task_id: `review-pipe`

## What It Does

Non-deterministic pipe with structured output. Takes content (code, text, specs) and produces a structured review: pass/fail outcome, findings as a list with severity and description, and actionable suggestions.

The `--criteria` flag selects what to evaluate against (correctness, style, test coverage, spec compliance). The structured output is critical — downstream pipes and pipeline conditions need to read `review.outcome == "pass"` programmatically, not parse prose.

This pipe becomes the verify step in every quality loop.

---

## File Layout

```
internal/pipes/review/
├── pipe.yaml
├── review.go
├── review_test.go
└── cmd/main.go
```

---

## pipe.yaml

```yaml
name: review
description: Reviews content against criteria and produces structured pass/fail findings.
category: dev
streaming: false
timeout: 90s

triggers:
  exact:
    - "review this"
    - "check this"
  keywords:
    - review
    - check
    - evaluate
    - audit
    - verify
    - critique
    - assess
  patterns:
    - "review {type}"
    - "check {type} for {topic}"
    - "review {source} against {topic}"

flags:
  criteria:
    description: What to evaluate against.
    values: [correctness, style, test-coverage, spec-compliance]
    default: correctness

  strictness:
    description: How strict the review should be.
    values: [lenient, normal, strict]
    default: normal

  format:
    description: Output detail level.
    values: [summary, full]
    default: full

vocabulary:
  verbs:
    review: review
    check: review
    evaluate: review
    audit: review
    verify: review
    critique: review
    assess: review
  types:
    code: code
    spec: spec
    text: text
    pr: pr
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb, source, topic]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, topic: "{topic}" }
        - pipe: "{verb}"
          flags: { criteria: spec-compliance }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { criteria: "{type}" }

    - requires: [verb]
      plan:
        - pipe: "{verb}"

prompts:
  system: |
    You are a precise, thorough reviewer. You evaluate content against
    specific criteria and produce structured assessments. You are direct
    about problems — you don't soften findings or hedge. When something
    passes, say so clearly. When something fails, explain exactly why
    and what to fix.

    You MUST respond with valid JSON matching this exact schema:

    {
      "outcome": "pass" | "fail",
      "summary": "one-sentence overall assessment",
      "findings": [
        {
          "severity": "error" | "warning" | "info",
          "location": "where in the content (line, section, function — whatever applies)",
          "description": "what the issue is",
          "suggestion": "how to fix it"
        }
      ]
    }

    Rules:
    - outcome is "fail" if ANY finding has severity "error"
    - outcome is "pass" if all findings are "warning" or "info" (or no findings)
    - findings array is always present, even if empty
    - location should be as specific as possible; use "" if not applicable
    - every error and warning MUST have a suggestion
    - info findings are observations, not problems — suggestions are optional
    - order findings by severity: errors first, then warnings, then info

  templates:
    correctness: |
      Review the following content for correctness.

      Check for: logical errors, incorrect assumptions, broken invariants,
      missing edge cases, undefined behavior, and factual inaccuracies.

      Content:
      {{.Content}}

      {{if .Topic}}Context: {{.Topic}}{{end}}

    style: |
      Review the following content for style and clarity.

      Check for: inconsistent naming, unclear structure, unnecessary complexity,
      missing documentation where non-obvious, dead code, and violations of
      the project's conventions.

      Content:
      {{.Content}}

      {{if .Topic}}Style guide or conventions: {{.Topic}}{{end}}

    test-coverage: |
      Review the following content for test coverage adequacy.

      Check for: untested code paths, missing edge case tests, missing error
      handling tests, brittle assertions, tests that don't actually verify
      behavior, and gaps between the implementation and its test suite.

      Content:
      {{.Content}}

      {{if .Topic}}Related tests or context: {{.Topic}}{{end}}

    spec-compliance: |
      Review the following content for compliance with the given specification.

      Check for: unimplemented requirements, deviations from the spec,
      missing acceptance criteria, and functionality that contradicts
      the spec.

      Content:
      {{.Content}}

      {{if .Topic}}Specification: {{.Topic}}{{end}}
```

---

## Structured Output Schema

The review pipe's `content` field is a JSON object (not prose). `content_type` is `structured`.

```json
{
  "outcome": "pass",
  "summary": "All calendar API error paths return correctly structured envelopes.",
  "findings": [
    {
      "severity": "warning",
      "location": "calendar.go:47",
      "description": "Timeout error returns severity 'error' but message says 'warning'.",
      "suggestion": "Align the message text with the severity field."
    },
    {
      "severity": "info",
      "location": "calendar.go:12-15",
      "description": "Good use of ClassifyError for automatic timeout detection.",
      "suggestion": ""
    }
  ]
}
```

**Why structured over text:** Downstream consumers need to branch on `outcome`. A pipeline that runs `draft → review` and only proceeds if review passes needs `review.outcome == "pass"` as a machine-readable value. The findings list enables automated issue tracking, severity filtering, and aggregation across multiple reviews.

**Go types for the handler:**

```go
type ReviewResult struct {
    Outcome  string    `json:"outcome"`
    Summary  string    `json:"summary"`
    Findings []Finding `json:"findings"`
}

type Finding struct {
    Severity    string `json:"severity"`
    Location    string `json:"location"`
    Description string `json:"description"`
    Suggestion  string `json:"suggestion"`
}
```

---

## Handler Implementation

### Entry point: `cmd/main.go`

```go
func main() {
    logger := pipehost.NewPipeLogger("review")

    provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
    if err != nil {
        pipehost.Fatal("review", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("review", err.Error())
    }

    compiled := review.CompileTemplates(pc)
    pipehost.Run(review.NewHandler(provider, pc, compiled, logger), nil)
}
```

No streaming — review output is structured JSON, not incremental prose. The user sees the complete result at once.

### Handler: `review.go`

The handler follows the same pattern as `draft.go`:

1. Extract content from input envelope via `envelope.ContentToText()`
2. Resolve the prompt template from `--criteria` flag
3. Build template data with content, topic, and flag values
4. Call the provider with system prompt + resolved template
5. Parse the provider's JSON response into `ReviewResult`
6. Return envelope with `content_type: "structured"` and the parsed result

**Critical difference from draft:** The handler must parse the AI response as JSON. If the model returns invalid JSON, the handler should:

1. Attempt a lenient parse (strip markdown fences, trim whitespace)
2. If still invalid, return a fatal error — not a text fallback. A review that can't be parsed programmatically is useless.

### Template data struct

```go
type templateData struct {
    Content     string
    Topic       string
    Strictness  string
}
```

The `Strictness` flag doesn't select a template — it modifies the system prompt behavior. The system prompt should include strictness guidance:

- `lenient`: only report clear errors, ignore style nitpicks
- `normal`: report errors and significant warnings
- `strict`: report everything including minor style issues

Inject strictness into the system prompt dynamically rather than maintaining separate system prompts per strictness level.

### JSON parsing

```go
func parseReviewResult(raw string) (ReviewResult, error) {
    cleaned := stripMarkdownFences(raw)
    var result ReviewResult
    if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
        return ReviewResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
    }
    if result.Outcome != "pass" && result.Outcome != "fail" {
        return ReviewResult{}, fmt.Errorf("invalid outcome: %q (must be pass or fail)", result.Outcome)
    }
    if result.Findings == nil {
        result.Findings = []Finding{}
    }
    return result, nil
}

func stripMarkdownFences(s string) string {
    // Strip ```json ... ``` wrapping if present
}
```

### Output envelope

```go
out := envelope.New("review", "review")
out.Args = flags
out.Content = result       // ReviewResult struct
out.ContentType = envelope.ContentStructured
out.Duration = time.Since(out.Timestamp)
```

---

## Error Handling

| Scenario | Severity | Retryable | Content |
|---|---|---|---|
| No content provided | fatal | no | nil |
| Unknown criteria value | fatal | no | nil |
| Provider timeout | error | yes | nil |
| Provider auth failure | fatal | no | nil |
| Model returns invalid JSON | fatal | no | nil — raw response in error message |
| Model returns valid JSON, bad schema | fatal | no | nil — include what was missing |

Invalid JSON from the model is fatal, not retryable. Retrying the same prompt to the same model rarely fixes JSON formatting. If this becomes a pattern, the fix is prompt engineering, not retries.

---

## Testing

### `review_test.go`

**Test with mock provider.** The provider interface is `bridge.Provider` — create a mock that returns canned JSON responses.

**Cases to cover:**

1. **Happy path — pass result.** Mock returns valid JSON with outcome "pass", zero errors. Assert envelope has `content_type: "structured"`, outcome is "pass", findings list exists.

2. **Happy path — fail result.** Mock returns valid JSON with outcome "fail", findings with errors. Assert outcome is "fail", findings have correct severities, error findings have suggestions.

3. **Each criteria template resolves.** For each of `[correctness, style, test-coverage, spec-compliance]`, verify the correct prompt template is selected and rendered with content/topic/strictness.

4. **Empty content.** Input envelope has empty content and no topic flag. Assert fatal error, no provider call made.

5. **Invalid JSON from model.** Mock returns prose instead of JSON. Assert fatal error envelope with descriptive message.

6. **JSON with markdown fences.** Mock returns ````json\n{...}\n````. Assert successful parse after fence stripping.

7. **Bad outcome value.** Mock returns `{"outcome": "maybe", ...}`. Assert fatal error.

8. **Missing findings array.** Mock returns `{"outcome": "pass", "summary": "ok"}`. Assert findings is normalized to empty array, not nil.

9. **Provider timeout.** Mock returns `context.DeadlineExceeded`. Assert retryable error.

10. **Provider error.** Mock returns generic error. Assert fatal error.

11. **Strictness flag propagation.** Verify that strictness value appears in the system prompt sent to the provider.

12. **Format flag — summary mode.** When `--format=summary`, the handler should still return structured JSON but may instruct the model to limit findings. (Verify the prompt includes format guidance.)

13. **Envelope compliance.** Every returned envelope has all required fields: pipe, action, args, timestamp, duration, content_type, error (null or populated).

---

## Composition Patterns

The review pipe is most useful mid-chain as a gate:

```
draft --type=email → review --criteria=style → [conditional: pass → send, fail → revise]
```

```
memory.retrieve → review --criteria=spec-compliance
```

```
code-review (future) → review --criteria=correctness
```

The template priority is 40 (lower than draft's 50) because review templates should match before draft templates when both could apply — "review the spec" should route to review, not draft.

The review pipe does not declare `sources` in its vocabulary because it doesn't provide context to other pipes — it consumes context and produces a verdict.

---

## Prompt Engineering Notes

The system prompt is the most important part of this pipe. Guidelines for iteration:

1. **JSON compliance is non-negotiable.** The system prompt must be aggressive about requesting JSON. Include the exact schema. Say "respond ONLY with JSON" and "do NOT include any text outside the JSON object." Test with multiple models.

2. **Severity calibration matters.** "error" means the review fails. The prompt should define what constitutes an error vs. a warning precisely enough that the model is consistent across invocations. Vague criteria produce inconsistent outcomes.

3. **Location specificity.** Push the model to give specific locations. "somewhere in the code" is not useful. Line numbers, function names, section headers — whatever the content type supports.

4. **Suggestion quality.** "Fix this" is not a suggestion. The prompt should require actionable, specific suggestions that a developer or writer could execute without further interpretation.

5. **Strictness is a dial, not a switch.** The difference between lenient and strict should be which findings are included, not how they're written. All findings should be equally well-described regardless of strictness — strictness just raises or lowers the bar for what gets reported.
