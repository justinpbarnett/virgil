package analyze

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "analyze",
		Prompts: config.PromptsConfig{
			System: "You are a senior engineer conducting a technical analysis.",
			Templates: map[string]string{
				"initial": "Analyze this proposed change.\n\nRequest:\n{{.Content}}\n\n{{if .CodebaseContext}}Codebase context:\n{{.CodebaseContext}}\n{{end}}\n\nDepth: {{.Depth}}",
				"refine":  "Process the user's input against the current state.\n\nCurrent state:\n{{.State}}\n\nUser's latest input:\n{{.Content}}\n\n{{if .OpenQuestions}}Previous open questions:\n{{.OpenQuestions}}\n{{end}}",
			},
		},
	}
}

func validInitialJSON() string {
	return `{
		"scope": {
			"in": ["user authentication"],
			"out": ["admin panel"],
			"boundary": ["session management"]
		},
		"components": [
			{
				"name": "auth handler",
				"description": "handles login flow",
				"dependencies": ["database"],
				"complexity": "medium"
			}
		],
		"risks": [
			{
				"description": "session fixation",
				"severity": "high",
				"likelihood": "medium",
				"mitigation": "regenerate session ID on login"
			}
		],
		"approaches": [
			{
				"name": "JWT tokens",
				"description": "stateless auth with JWT",
				"tradeoffs": "no server-side session store needed but harder to revoke",
				"recommendation": true
			},
			{
				"name": "server-side sessions",
				"description": "traditional session cookies",
				"tradeoffs": "simple revocation but requires session store",
				"recommendation": false
			}
		],
		"open_questions": [
			{
				"question": "what is the expected session duration?",
				"why_it_matters": "determines token expiry and refresh strategy",
				"options": ["short-lived (15m)", "long-lived (24h)"]
			}
		],
		"resolved": []
	}`
}

func validRefineJSON() string {
	return `{
		"scope": {
			"in": ["user authentication", "session management"],
			"out": ["admin panel"],
			"boundary": []
		},
		"components": [
			{
				"name": "auth handler",
				"description": "handles login flow",
				"dependencies": ["database"],
				"complexity": "medium"
			}
		],
		"risks": [
			{
				"description": "token theft via XSS",
				"severity": "high",
				"likelihood": "low",
				"mitigation": "use httpOnly cookies"
			}
		],
		"approaches": [],
		"open_questions": [],
		"resolved": [
			{
				"question": "what is the expected session duration?",
				"answer": "short-lived 15 minute tokens with refresh",
				"implications": "need refresh token rotation implementation"
			}
		]
	}`
}

// TestPreparePrompt_Initial verifies template selection and field population for initial phase.
func TestPreparePrompt_Initial(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "add user authentication"
	input.ContentType = "text"

	flags := map[string]string{"phase": "initial", "depth": "standard"}

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "add user authentication") {
		t.Errorf("expected prompt to contain content, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Depth: standard") {
		t.Errorf("expected prompt to contain depth, got: %s", userPrompt)
	}
	// Initial phase should not contain state references
	if strings.Contains(userPrompt, "Current state:") {
		t.Error("initial phase prompt should not contain state section")
	}
}

// TestPreparePrompt_Refine verifies template selection and field population for refine phase.
func TestPreparePrompt_Refine(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "use short-lived tokens"
	input.ContentType = "text"

	flags := map[string]string{
		"phase":          "refine",
		"state":          `{"scope":{"in":["auth"]}}`,
		"open_questions": "What token duration?",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "use short-lived tokens") {
		t.Errorf("expected prompt to contain content, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Current state:") {
		t.Error("refine phase prompt should contain state section")
	}
	if !strings.Contains(userPrompt, `{"scope":{"in":["auth"]}}`) {
		t.Errorf("expected state to be populated, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "What token duration?") {
		t.Errorf("expected open questions to be populated, got: %s", userPrompt)
	}
}

// TestPreparePrompt_NoContent verifies fatal error when no content is provided.
func TestPreparePrompt_NoContent(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	_, _, errEnv := preparePrompt(compiled, pc, input, map[string]string{})
	if errEnv == nil {
		t.Fatal("expected error for empty content")
	}
	if errEnv.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", errEnv.Severity)
	}
	if !strings.Contains(errEnv.Message, "no content") {
		t.Errorf("expected error about no content, got: %s", errEnv.Message)
	}
}

// TestPreparePrompt_DepthFlag verifies depth flag flows through to template data.
func TestPreparePrompt_DepthFlag(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	flags := map[string]string{"phase": "initial", "depth": "deep"}

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "Depth: deep") {
		t.Errorf("expected prompt to contain 'Depth: deep', got: %s", userPrompt)
	}
}

// TestParseAnalysisResult_Valid verifies well-formed JSON parses correctly.
func TestParseAnalysisResult_Valid(t *testing.T) {
	result, err := parseAnalysisResult(validInitialJSON())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Scope.In) != 1 || result.Scope.In[0] != "user authentication" {
		t.Errorf("unexpected scope.in: %v", result.Scope.In)
	}
	if len(result.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(result.Components))
	}
	if len(result.Risks) != 1 {
		t.Errorf("expected 1 risk, got %d", len(result.Risks))
	}
	if len(result.Approaches) != 2 {
		t.Errorf("expected 2 approaches, got %d", len(result.Approaches))
	}
	if len(result.OpenQuestions) != 1 {
		t.Errorf("expected 1 open question, got %d", len(result.OpenQuestions))
	}
	if len(result.Resolved) != 0 {
		t.Errorf("expected 0 resolved, got %d", len(result.Resolved))
	}
}

// TestParseAnalysisResult_WithFences verifies JSON wrapped in markdown fences parses correctly.
func TestParseAnalysisResult_WithFences(t *testing.T) {
	fenced := "```json\n" + validInitialJSON() + "\n```"
	result, err := parseAnalysisResult(fenced)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Scope.In) != 1 {
		t.Errorf("expected 1 scope.in item after fence stripping, got %d", len(result.Scope.In))
	}
}

// TestParseAnalysisResult_Invalid verifies malformed JSON returns error.
func TestParseAnalysisResult_Invalid(t *testing.T) {
	_, err := parseAnalysisResult("This is not JSON at all, just prose.")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("expected error about invalid JSON, got: %v", err)
	}
}

// TestParseAnalysisResult_MissingScope verifies JSON with empty scope.in returns error.
func TestParseAnalysisResult_MissingScope(t *testing.T) {
	emptyScope := `{"scope":{"in":[],"out":[],"boundary":[]},"components":[],"risks":[],"approaches":[],"open_questions":[],"resolved":[]}`
	_, err := parseAnalysisResult(emptyScope)
	if err == nil {
		t.Fatal("expected error for empty scope.in")
	}
	if !strings.Contains(err.Error(), "scope.in") {
		t.Errorf("expected error about scope.in, got: %v", err)
	}
}

// TestNewHandler_Initial verifies handler with mock provider returning valid initial JSON.
func TestNewHandler_Initial(t *testing.T) {
	provider := &testutil.MockProvider{Response: validInitialJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "add user authentication"
	input.ContentType = "text"

	result := handler(input, map[string]string{"phase": "initial"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	b, _ := json.Marshal(result.Content)
	var ar AnalysisResult
	if err := json.Unmarshal(b, &ar); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if len(ar.Scope.In) != 1 {
		t.Errorf("expected 1 scope.in item, got %d", len(ar.Scope.In))
	}
	if len(ar.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(ar.Components))
	}
	if ar.Components[0].Name != "auth handler" {
		t.Errorf("expected component name 'auth handler', got %s", ar.Components[0].Name)
	}
}

// TestNewHandler_Refine verifies handler with mock provider returning refine JSON with resolved items.
func TestNewHandler_Refine(t *testing.T) {
	provider := &testutil.MockProvider{Response: validRefineJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "use short-lived tokens"
	input.ContentType = "text"

	flags := map[string]string{
		"phase":          "refine",
		"state":          `{"scope":{"in":["auth"]}}`,
		"open_questions": "What token duration?",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	b, _ := json.Marshal(result.Content)
	var ar AnalysisResult
	if err := json.Unmarshal(b, &ar); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if len(ar.Resolved) != 1 {
		t.Fatalf("expected 1 resolved item, got %d", len(ar.Resolved))
	}
	if ar.Resolved[0].Answer != "short-lived 15 minute tokens with refresh" {
		t.Errorf("unexpected resolved answer: %s", ar.Resolved[0].Answer)
	}
}

// TestNewHandler_ProviderError verifies retryable error classification on provider failure.
func TestNewHandler_ProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: context.DeadlineExceeded}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	result := handler(input, map[string]string{"phase": "initial"})

	if result.Error == nil {
		t.Fatal("expected error for provider failure")
	}
	if !result.Error.Retryable {
		t.Error("expected timeout error to be retryable")
	}
}

// TestNewHandler_NonRetryableError verifies fatal error classification for non-timeout errors.
func TestNewHandler_NonRetryableError(t *testing.T) {
	provider := &testutil.MockProvider{Err: errors.New("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	result := handler(input, map[string]string{"phase": "initial"})

	testutil.AssertFatalError(t, result)
}

// TestNewHandler_EnvelopeCompliance verifies envelope fields are set correctly.
func TestNewHandler_EnvelopeCompliance(t *testing.T) {
	provider := &testutil.MockProvider{Response: validInitialJSON()}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	flags := map[string]string{"phase": "initial"}
	result := handler(input, flags)

	testutil.AssertEnvelope(t, result, "analyze", "analyze")
	if result.Args == nil {
		t.Error("expected args to be set")
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

// TestPreparePrompt_DefaultPhase verifies that missing phase defaults to initial.
func TestPreparePrompt_DefaultPhase(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	// Should use initial template (contains Depth)
	if !strings.Contains(userPrompt, "Depth: standard") {
		t.Errorf("expected default phase=initial with depth=standard, got: %s", userPrompt)
	}
}

// TestPreparePrompt_DefaultDepth verifies that missing depth defaults to standard.
func TestPreparePrompt_DefaultDepth(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	flags := map[string]string{"phase": "initial"}

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "Depth: standard") {
		t.Errorf("expected default depth=standard, got: %s", userPrompt)
	}
}

// TestParseAnalysisResult_EmptyArrays verifies that empty arrays are valid (not everything has risks or approaches).
func TestParseAnalysisResult_EmptyArrays(t *testing.T) {
	minimal := `{"scope":{"in":["one thing"]},"components":[],"risks":[],"approaches":[],"open_questions":[],"resolved":[]}`
	result, err := parseAnalysisResult(minimal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Components) != 0 {
		t.Errorf("expected 0 components, got %d", len(result.Components))
	}
	if len(result.Risks) != 0 {
		t.Errorf("expected 0 risks, got %d", len(result.Risks))
	}
}

// TestParseAnalysisResult_ExtraFields verifies that extra fields in JSON are ignored for forward compatibility.
func TestParseAnalysisResult_ExtraFields(t *testing.T) {
	withExtra := `{"scope":{"in":["auth"],"out":[],"boundary":[]},"components":[],"risks":[],"approaches":[],"open_questions":[],"resolved":[],"future_field":"ignored"}`
	result, err := parseAnalysisResult(withExtra)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Scope.In) != 1 {
		t.Errorf("expected 1 scope.in item, got %d", len(result.Scope.In))
	}
}

// TestPreparePrompt_CodebaseContext verifies context flag flows through to template data.
func TestPreparePrompt_CodebaseContext(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "analyze something"
	input.ContentType = "text"

	flags := map[string]string{
		"phase":   "initial",
		"context": "The codebase uses Go with a pipe architecture.",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "The codebase uses Go with a pipe architecture.") {
		t.Errorf("expected prompt to contain codebase context, got: %s", userPrompt)
	}
}

