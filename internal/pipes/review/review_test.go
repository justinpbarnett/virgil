package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "review",
		Prompts: config.PromptsConfig{
			System: "You are a precise, thorough reviewer.",
			Templates: map[string]string{
				"correctness":     "Review for correctness.\n\nContent:\n{{.Content}}\n\n{{if .Topic}}Context: {{.Topic}}{{end}}",
				"style":           "Review for style.\n\nContent:\n{{.Content}}\n\n{{if .Topic}}Style guide: {{.Topic}}{{end}}",
				"test-coverage":   "Review for test coverage.\n\nContent:\n{{.Content}}\n\n{{if .Topic}}Tests: {{.Topic}}{{end}}",
				"spec-compliance": "Review for spec compliance.\n\nContent:\n{{.Content}}\n\n{{if .Topic}}Spec: {{.Topic}}{{end}}",
				"dev-review":      "Review PR diff.\n\nPR Diff:\n{{.Content}}\n\n{{if .Topic}}Context: {{.Topic}}{{end}}",
				"completeness":    "Review spec completeness.\n\nSpec:\n{{.Content}}\n\n{{if .Topic}}Context: {{.Topic}}{{end}}",
			},
		},
	}
}

func passJSON() string {
	return `{"outcome":"pass","summary":"All good.","findings":[]}`
}

func failJSON() string {
	return `{"outcome":"fail","summary":"Found issues.","findings":[{"severity":"error","location":"main.go:10","description":"nil pointer dereference","suggestion":"Add nil check before access."},{"severity":"warning","location":"main.go:20","description":"Unused variable","suggestion":"Remove or use the variable."}]}`
}

func devReviewPassJSON() string {
	return `{"outcome":"pass","summary":"Clean implementation.","findings":[]}`
}

func devReviewFailJSON() string {
	return `{"outcome":"fail","summary":"Major issues found.","findings":[{"category":"logic","severity":"major","file":"internal/handler.go","line":42,"issue":"nil pointer dereference on error path","action":"Add nil check before accessing resp.Body"},{"category":"style","severity":"nit","file":"internal/handler.go","line":10,"issue":"unused import","action":""}]}`
}

func needsRevisionJSON() string {
	return `{"outcome":"needs-revision","summary":"Spec has fixable gaps.","findings":[{"severity":"warning","location":"Section 3","description":"Vague scope boundary","suggestion":"Replace 'as needed' with concrete criteria"}]}`
}

// --- Existing tests (preserved) ---

func TestReviewPassResult(t *testing.T) {
	provider := &testutil.MockProvider{Response: passJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "func main() {}"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	b, _ := json.Marshal(result.Content)
	var rr ReviewResult
	if err := json.Unmarshal(b, &rr); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if rr.Outcome != "pass" {
		t.Errorf("expected outcome=pass, got %s", rr.Outcome)
	}
	if rr.Findings == nil {
		t.Error("expected findings to be non-nil")
	}
}

func TestReviewFailResult(t *testing.T) {
	provider := &testutil.MockProvider{Response: failJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "func main() { var x *int; fmt.Println(*x) }"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	b, _ := json.Marshal(result.Content)
	var rr ReviewResult
	json.Unmarshal(b, &rr)

	if rr.Outcome != "fail" {
		t.Errorf("expected outcome=fail, got %s", rr.Outcome)
	}
	if len(rr.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(rr.Findings))
	}
	if rr.Findings[0].Severity != "error" {
		t.Errorf("expected first finding severity=error, got %s", rr.Findings[0].Severity)
	}
	if rr.Findings[0].Suggestion == "" {
		t.Error("expected error finding to have a suggestion")
	}
	if rr.Findings[1].Severity != "warning" {
		t.Errorf("expected second finding severity=warning, got %s", rr.Findings[1].Severity)
	}
}

func TestReviewTemplateResolution(t *testing.T) {
	// dev-review uses parseDevReviewResult which expects DevReviewResult schema
	mockResponses := map[string]string{
		"dev-review": devReviewPassJSON(),
	}

	criteria := []string{"correctness", "style", "test-coverage", "spec-compliance", "dev-review", "completeness"}

	for _, c := range criteria {
		t.Run(c, func(t *testing.T) {
			response := passJSON()
			if r, ok := mockResponses[c]; ok {
				response = r
			}
			provider := &capturingProvider{response: response}
			handler := NewHandler(provider, testConfig(), nil)

			input := envelope.New("input", "test")
			input.Content = "test content"
			input.ContentType = "text"

			result := handler(input, map[string]string{"criteria": c, "topic": "some context"})
			if result.Error != nil {
				t.Fatalf("unexpected error: %v", result.Error)
			}

			if !strings.Contains(provider.lastUser, "test content") {
				t.Errorf("expected prompt to contain content, got: %s", provider.lastUser)
			}
		})
	}
}

func TestReviewEmptyContent(t *testing.T) {
	provider := &testutil.MockProvider{Response: passJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
}

func TestReviewUnknownCriteria(t *testing.T) {
	provider := &testutil.MockProvider{Response: passJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "foobar"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "unknown criteria") {
		t.Errorf("expected error about unknown criteria, got: %s", result.Error.Message)
	}
}

func TestReviewInvalidJSON(t *testing.T) {
	provider := &testutil.MockProvider{Response: "This is not JSON at all, just prose."}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "invalid JSON") {
		t.Errorf("expected error about invalid JSON, got: %s", result.Error.Message)
	}
}

func TestReviewJSONWithMarkdownFences(t *testing.T) {
	fenced := "```json\n" + passJSON() + "\n```"
	provider := &testutil.MockProvider{Response: fenced}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	b, _ := json.Marshal(result.Content)
	var rr ReviewResult
	json.Unmarshal(b, &rr)

	if rr.Outcome != "pass" {
		t.Errorf("expected outcome=pass after fence stripping, got %s", rr.Outcome)
	}
}

func TestReviewBadOutcome(t *testing.T) {
	provider := &testutil.MockProvider{Response: `{"outcome":"maybe","summary":"unsure","findings":[]}`}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "invalid outcome") {
		t.Errorf("expected error about invalid outcome, got: %s", result.Error.Message)
	}
}

func TestReviewMissingFindings(t *testing.T) {
	provider := &testutil.MockProvider{Response: `{"outcome":"pass","summary":"ok"}`}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	b, _ := json.Marshal(result.Content)
	var rr ReviewResult
	json.Unmarshal(b, &rr)

	if rr.Findings == nil {
		t.Error("expected findings to be normalized to empty slice, not nil")
	}
	if len(rr.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(rr.Findings))
	}
}

func TestReviewProviderTimeout(t *testing.T) {
	provider := &testutil.MockProvider{Err: context.DeadlineExceeded}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	if result.Error == nil {
		t.Fatal("expected error for timeout")
	}
	if !result.Error.Retryable {
		t.Error("expected timeout error to be retryable")
	}
}

func TestReviewProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: fmt.Errorf("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness"})

	testutil.AssertFatalError(t, result)
}

func TestReviewStrictnessPropagation(t *testing.T) {
	cases := []struct {
		strictness string
		wantInSys  string
	}{
		{"lenient", "lenient"},
		{"normal", "normal"},
		{"strict", "strict"},
	}

	for _, tc := range cases {
		t.Run(tc.strictness, func(t *testing.T) {
			provider := &capturingProvider{response: passJSON()}
			handler := NewHandler(provider, testConfig(), nil)

			input := envelope.New("input", "test")
			input.Content = "some code"
			input.ContentType = "text"

			result := handler(input, map[string]string{"criteria": "correctness", "strictness": tc.strictness})
			if result.Error != nil {
				t.Fatalf("unexpected error: %v", result.Error)
			}

			if !strings.Contains(strings.ToLower(provider.lastSystem), tc.wantInSys) {
				t.Errorf("expected system prompt to contain %q, got: %s", tc.wantInSys, provider.lastSystem)
			}
		})
	}
}

func TestReviewFormatSummary(t *testing.T) {
	provider := &capturingProvider{response: passJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "correctness", "format": "summary"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if !strings.Contains(strings.ToLower(provider.lastSystem), "summary") {
		t.Error("expected system prompt to contain format guidance for summary mode")
	}
}

func TestReviewEnvelopeCompliance(t *testing.T) {
	provider := &testutil.MockProvider{Response: passJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	flags := map[string]string{"criteria": "correctness"}
	result := handler(input, flags)

	testutil.AssertEnvelope(t, result, "review", "review")
	if result.Args == nil {
		t.Error("expected args to be set")
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

// --- New tests: PR-diff source ---

func TestReviewPRDiffSource(t *testing.T) {
	fetcher := &mockDiffFetcher{diff: "diff --git a/main.go b/main.go\n+func hello() {}"}
	provider := &capturingProvider{response: passJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, fetcher, nil)

	input := envelope.New("input", "test")
	input.Content = "this should be replaced"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"criteria": "correctness",
		"source":   "pr-diff",
		"pr":       "47",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify the diff content was used in the prompt
	if !strings.Contains(provider.lastUser, "diff --git") {
		t.Errorf("expected prompt to contain fetched diff, got: %s", provider.lastUser)
	}
	if strings.Contains(provider.lastUser, "this should be replaced") {
		t.Error("expected original content to be replaced by diff")
	}

	// Verify the fetcher was called with the right PR ID
	if fetcher.lastPR != "47" {
		t.Errorf("expected fetcher called with PR 47, got %s", fetcher.lastPR)
	}
}

func TestReviewPRDiffNoPR(t *testing.T) {
	fetcher := &mockDiffFetcher{diff: "some diff"}
	provider := &testutil.MockProvider{Response: passJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, fetcher, nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"criteria": "correctness",
		"source":   "pr-diff",
	})

	if result.Error == nil {
		t.Fatal("expected error when source=pr-diff but no PR identifier")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "no PR identifier") {
		t.Errorf("expected error about missing PR identifier, got: %s", result.Error.Message)
	}
}

func TestReviewPRDiffFetchError(t *testing.T) {
	fetcher := &mockDiffFetcher{err: fmt.Errorf("gh: not authenticated")}
	provider := &testutil.MockProvider{Response: passJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, fetcher, nil)

	input := envelope.New("input", "test")
	input.Content = "some code"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"criteria": "correctness",
		"source":   "pr-diff",
		"pr":       "47",
	})

	if result.Error == nil {
		t.Fatal("expected error when diff fetch fails")
	}
	if !strings.Contains(result.Error.Message, "fetch PR diff") {
		t.Errorf("expected error about fetch PR diff, got: %s", result.Error.Message)
	}
}

func TestReviewPRDiffFromEnvelope(t *testing.T) {
	fetcher := &mockDiffFetcher{diff: "diff from envelope PR"}
	provider := &capturingProvider{response: passJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, fetcher, nil)

	// Envelope has structured content with pr_url
	input := envelope.New("input", "test")
	input.Content = map[string]any{
		"pr_url": "https://github.com/owner/repo/pull/99",
	}
	input.ContentType = "structured"

	result := handler(input, map[string]string{
		"criteria": "correctness",
		"source":   "pr-diff",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if fetcher.lastPR != "https://github.com/owner/repo/pull/99" {
		t.Errorf("expected fetcher called with PR URL, got %s", fetcher.lastPR)
	}

	if !strings.Contains(provider.lastUser, "diff from envelope PR") {
		t.Errorf("expected prompt to contain fetched diff, got: %s", provider.lastUser)
	}
}

// --- New tests: Dev-review criteria ---

func TestReviewDevReviewPass(t *testing.T) {
	provider := &testutil.MockProvider{Response: devReviewPassJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, &mockDiffFetcher{}, nil)

	input := envelope.New("input", "test")
	input.Content = "diff --git a/main.go b/main.go\n+func hello() {}"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "dev-review"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	b, _ := json.Marshal(result.Content)
	var rr DevReviewResult
	if err := json.Unmarshal(b, &rr); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if rr.Outcome != "pass" {
		t.Errorf("expected outcome=pass, got %s", rr.Outcome)
	}
	if rr.Findings == nil {
		t.Error("expected findings to be non-nil empty slice")
	}
	if len(rr.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(rr.Findings))
	}
}

func TestReviewDevReviewFail(t *testing.T) {
	provider := &testutil.MockProvider{Response: devReviewFailJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, &mockDiffFetcher{}, nil)

	input := envelope.New("input", "test")
	input.Content = "diff --git a/handler.go b/handler.go\n+func handle() {}"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "dev-review"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	b, _ := json.Marshal(result.Content)
	var rr DevReviewResult
	if err := json.Unmarshal(b, &rr); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if rr.Outcome != "fail" {
		t.Errorf("expected outcome=fail, got %s", rr.Outcome)
	}
	if len(rr.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(rr.Findings))
	}
	if rr.Findings[0].Category != "logic" {
		t.Errorf("expected first finding category=logic, got %s", rr.Findings[0].Category)
	}
	if rr.Findings[0].Severity != "major" {
		t.Errorf("expected first finding severity=major, got %s", rr.Findings[0].Severity)
	}
	if rr.Findings[0].File != "internal/handler.go" {
		t.Errorf("expected first finding file=internal/handler.go, got %s", rr.Findings[0].File)
	}
	if rr.Findings[0].Line != 42 {
		t.Errorf("expected first finding line=42, got %d", rr.Findings[0].Line)
	}
	if rr.Findings[1].Category != "style" {
		t.Errorf("expected second finding category=style, got %s", rr.Findings[1].Category)
	}
	if rr.Findings[1].Severity != "nit" {
		t.Errorf("expected second finding severity=nit, got %s", rr.Findings[1].Severity)
	}
}

func TestReviewDevReviewParsing(t *testing.T) {
	// Test all valid category/severity combinations
	categories := []string{"architecture", "logic", "testing", "style", "security"}
	severities := []string{"major", "minor", "nit"}

	for _, cat := range categories {
		for _, sev := range severities {
			t.Run(cat+"/"+sev, func(t *testing.T) {
				raw := fmt.Sprintf(`{"outcome":"pass","summary":"ok","findings":[{"category":"%s","severity":"%s","file":"a.go","line":1,"issue":"test","action":"fix"}]}`, cat, sev)
				result, err := parseDevReviewResult(raw)
				if err != nil {
					t.Fatalf("unexpected error for %s/%s: %v", cat, sev, err)
				}
				if len(result.Findings) != 1 {
					t.Fatalf("expected 1 finding, got %d", len(result.Findings))
				}
				if result.Findings[0].Category != cat {
					t.Errorf("expected category=%s, got %s", cat, result.Findings[0].Category)
				}
				if result.Findings[0].Severity != sev {
					t.Errorf("expected severity=%s, got %s", sev, result.Findings[0].Severity)
				}
			})
		}
	}
}

func TestReviewDevReviewInvalidCategory(t *testing.T) {
	raw := `{"outcome":"pass","summary":"ok","findings":[{"category":"performance","severity":"major","file":"a.go","line":1,"issue":"slow","action":"optimize"}]}`
	_, err := parseDevReviewResult(raw)
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
	if !strings.Contains(err.Error(), "invalid category") {
		t.Errorf("expected error about invalid category, got: %v", err)
	}
}

func TestReviewDevReviewInvalidSeverity(t *testing.T) {
	raw := `{"outcome":"pass","summary":"ok","findings":[{"category":"logic","severity":"critical","file":"a.go","line":1,"issue":"bad","action":"fix"}]}`
	_, err := parseDevReviewResult(raw)
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
	if !strings.Contains(err.Error(), "invalid severity") {
		t.Errorf("expected error about invalid severity, got: %v", err)
	}
}

func TestReviewDevReviewMalformedJSON(t *testing.T) {
	provider := &testutil.MockProvider{Response: "This is not valid JSON"}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, &mockDiffFetcher{}, nil)

	input := envelope.New("input", "test")
	input.Content = "some diff"
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "dev-review"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "invalid JSON") {
		t.Errorf("expected error about invalid JSON, got: %s", result.Error.Message)
	}
}

// --- New tests: Completeness criteria / needs-revision ---

func TestParseReviewResult_NeedsRevision(t *testing.T) {
	raw := `{"outcome":"needs-revision","summary":"Spec has fixable gaps.","findings":[{"severity":"warning","location":"Section 3","description":"Vague scope","suggestion":"Be specific"}]}`
	result, err := parseReviewResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "needs-revision" {
		t.Errorf("expected outcome=needs-revision, got %s", result.Outcome)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
}

func TestParseReviewResult_ValidOutcomes(t *testing.T) {
	outcomes := []string{"pass", "fail", "needs-revision"}
	for _, outcome := range outcomes {
		t.Run(outcome, func(t *testing.T) {
			raw := fmt.Sprintf(`{"outcome":"%s","summary":"test","findings":[]}`, outcome)
			result, err := parseReviewResult(raw)
			if err != nil {
				t.Fatalf("unexpected error for outcome %q: %v", outcome, err)
			}
			if result.Outcome != outcome {
				t.Errorf("expected outcome=%s, got %s", outcome, result.Outcome)
			}
		})
	}
}

func TestParseReviewResult_InvalidOutcome(t *testing.T) {
	raw := `{"outcome":"maybe","summary":"unsure","findings":[]}`
	_, err := parseReviewResult(raw)
	if err == nil {
		t.Fatal("expected error for invalid outcome")
	}
	if !strings.Contains(err.Error(), "invalid outcome") {
		t.Errorf("expected error about invalid outcome, got: %v", err)
	}
	if !strings.Contains(err.Error(), "needs-revision") {
		t.Errorf("expected error message to mention needs-revision as valid option, got: %v", err)
	}
}

func TestPreparePrompt_Completeness(t *testing.T) {
	provider := &capturingProvider{response: passJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, &mockDiffFetcher{}, nil)

	input := envelope.New("input", "test")
	input.Content = "# Feature Spec\nThis is the spec content."
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "completeness", "topic": "feature X"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify the completeness template was used
	if !strings.Contains(provider.lastUser, "spec completeness") {
		t.Errorf("expected prompt to use completeness template, got: %s", provider.lastUser)
	}
	if !strings.Contains(provider.lastUser, "Feature Spec") {
		t.Errorf("expected prompt to contain spec content, got: %s", provider.lastUser)
	}
	if !strings.Contains(provider.lastUser, "feature X") {
		t.Errorf("expected prompt to contain topic, got: %s", provider.lastUser)
	}
}

func TestNewHandler_Completeness(t *testing.T) {
	provider := &testutil.MockProvider{Response: needsRevisionJSON()}
	compiled := CompileTemplates(testConfig())
	handler := NewHandlerWith(provider, testConfig(), compiled, &mockDiffFetcher{}, nil)

	input := envelope.New("input", "test")
	input.Content = "# Spec\nSome spec content with vague language."
	input.ContentType = "text"

	result := handler(input, map[string]string{"criteria": "completeness"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	b, _ := json.Marshal(result.Content)
	var rr ReviewResult
	if err := json.Unmarshal(b, &rr); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if rr.Outcome != "needs-revision" {
		t.Errorf("expected outcome=needs-revision, got %s", rr.Outcome)
	}
	if len(rr.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(rr.Findings))
	}
	if rr.Findings[0].Severity != "warning" {
		t.Errorf("expected finding severity=warning, got %s", rr.Findings[0].Severity)
	}
}

// --- Test helpers ---

// capturingProvider captures the system and user prompts for assertion.
type capturingProvider struct {
	response   string
	lastSystem string
	lastUser   string
}

func (p *capturingProvider) Complete(_ context.Context, system, user string) (string, error) {
	p.lastSystem = system
	p.lastUser = user
	return p.response, nil
}

// mockDiffFetcher implements DiffFetcher for tests.
type mockDiffFetcher struct {
	diff   string
	err    error
	lastPR string
}

func (m *mockDiffFetcher) FetchPRDiff(_ context.Context, prIdentifier string) (string, error) {
	m.lastPR = prIdentifier
	if m.err != nil {
		return "", m.err
	}
	return m.diff, nil
}
