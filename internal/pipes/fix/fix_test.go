package fix

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "fix",
		Prompts: config.PromptsConfig{
			System: "You are a precise debugger. You fix specific errors with minimal changes.",
			Templates: map[string]string{
				"targeted": `Fix the following verification failures.

## Test Failures
{{range .TestFailures}}
- **{{.Package}}/{{.Test}}** ({{.File}})
  Error: {{.Error}}
  {{if .Expected}}Expected: {{.Expected}}{{end}}
  {{if .Actual}}Actual: {{.Actual}}{{end}}
{{end}}

{{if .LintErrors}}
## Lint Errors
{{range .LintErrors}}
- **{{.File}}:{{.Line}}** [{{.Rule}}] {{.Message}}
{{end}}
{{end}}

Fix each failure with the minimum change necessary.
{{if gt .Attempt 1}}
This is attempt {{.Attempt}}. Previous fix attempts did not resolve
all failures. Try a different approach for persistent errors.
{{end}}
`,
				"broad": `Fix the following verification failures, and scan for similar issues
in related files.

## Test Failures
{{range .TestFailures}}
- **{{.Package}}/{{.Test}}** ({{.File}})
  Error: {{.Error}}
  {{if .Expected}}Expected: {{.Expected}}{{end}}
  {{if .Actual}}Actual: {{.Actual}}{{end}}
{{end}}

{{if .LintErrors}}
## Lint Errors
{{range .LintErrors}}
- **{{.File}}:{{.Line}}** [{{.Rule}}] {{.Message}}
{{end}}
{{end}}

Fix each reported failure, then check nearby code for the same
patterns and fix those too.
`,
			},
		},
	}
}

// verifyInput creates an input envelope with structured verify output content.
func verifyInput(testFailures []TestFailure, lintErrors []LintError) envelope.Envelope {
	input := envelope.New("verify", "check")
	content := map[string]any{}
	if len(testFailures) > 0 {
		// Convert to []any via map representation for JSON fidelity.
		failures := make([]any, len(testFailures))
		for i, f := range testFailures {
			item := map[string]any{
				"file":    f.File,
				"test":    f.Test,
				"package": f.Package,
				"error":   f.Error,
			}
			if f.Expected != "" {
				item["expected"] = f.Expected
			}
			if f.Actual != "" {
				item["actual"] = f.Actual
			}
			failures[i] = item
		}
		content["test_result"] = map[string]any{
			"failures": failures,
		}
	} else {
		content["test_result"] = map[string]any{
			"failures": []any{},
		}
	}
	if len(lintErrors) > 0 {
		errors := make([]any, len(lintErrors))
		for i, e := range lintErrors {
			errors[i] = map[string]any{
				"file":    e.File,
				"line":    e.Line,
				"column":  e.Column,
				"rule":    e.Rule,
				"message": e.Message,
			}
		}
		content["lint_result"] = map[string]any{
			"errors": errors,
		}
	} else {
		content["lint_result"] = map[string]any{
			"errors": []any{},
		}
	}
	input.Content = content
	input.ContentType = envelope.ContentStructured
	return input
}

func TestFixTargetedTestFailures(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Fixed: updated return value in Add()"}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:     "math_test.go",
			Test:     "TestAdd",
			Package:  "math",
			Error:    "expected 5, got 4",
			Expected: "5",
			Actual:   "4",
		},
		{
			File:    "math_test.go",
			Test:    "TestSubtract",
			Package: "math",
			Error:   "nil pointer dereference",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(input, map[string]string{"scope": "targeted"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	// Verify the prompt rendered test failure details.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"scope": "targeted"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "math/TestAdd") {
		t.Errorf("expected prompt to contain 'math/TestAdd', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "math_test.go") {
		t.Errorf("expected prompt to contain 'math_test.go', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "expected 5, got 4") {
		t.Errorf("expected prompt to contain error message, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Expected: 5") {
		t.Errorf("expected prompt to contain 'Expected: 5', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Actual: 4") {
		t.Errorf("expected prompt to contain 'Actual: 4', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "math/TestSubtract") {
		t.Errorf("expected prompt to contain 'math/TestSubtract', got: %s", userPrompt)
	}
}

func TestFixTargetedLintErrors(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Fixed: added error check"}
	handler := NewHandler(provider, testConfig(), nil)

	lintErrors := []LintError{
		{
			File:    "handler.go",
			Line:    42,
			Column:  5,
			Rule:    "errcheck",
			Message: "Error return value of `db.Close` is not checked",
		},
		{
			File:    "handler.go",
			Line:    88,
			Column:  12,
			Rule:    "unused",
			Message: "field `name` is unused",
		},
	}

	input := verifyInput(nil, lintErrors)
	result := handler(input, map[string]string{"scope": "targeted"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	// Verify the prompt rendered lint error details.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"scope": "targeted"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "handler.go:42") {
		t.Errorf("expected prompt to contain 'handler.go:42', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "[errcheck]") {
		t.Errorf("expected prompt to contain '[errcheck]', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "handler.go:88") {
		t.Errorf("expected prompt to contain 'handler.go:88', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "[unused]") {
		t.Errorf("expected prompt to contain '[unused]', got: %s", userPrompt)
	}
}

func TestFixBothFailureTypes(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Fixed test and lint errors"}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "calc_test.go",
			Test:    "TestDivide",
			Package: "calc",
			Error:   "division by zero",
		},
	}
	lintErrors := []LintError{
		{
			File:    "calc.go",
			Line:    15,
			Column:  1,
			Rule:    "govet",
			Message: "unreachable code",
		},
	}

	input := verifyInput(failures, lintErrors)
	result := handler(input, map[string]string{"scope": "targeted"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify prompt contains both sections.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"scope": "targeted"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "## Test Failures") {
		t.Errorf("expected prompt to contain test failures section")
	}
	if !strings.Contains(userPrompt, "calc/TestDivide") {
		t.Errorf("expected prompt to contain 'calc/TestDivide', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "## Lint Errors") {
		t.Errorf("expected prompt to contain lint errors section")
	}
	if !strings.Contains(userPrompt, "calc.go:15") {
		t.Errorf("expected prompt to contain 'calc.go:15', got: %s", userPrompt)
	}
}

func TestFixNoFailures(t *testing.T) {
	provider := &testutil.MockProvider{Response: "should not be called"}
	handler := NewHandler(provider, testConfig(), nil)

	input := verifyInput(nil, nil)
	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	fixOut, ok := result.Content.(FixOutput)
	if !ok {
		t.Fatalf("expected FixOutput content, got %T", result.Content)
	}
	if fixOut.Summary != "Nothing to fix" {
		t.Errorf("expected summary='Nothing to fix', got %s", fixOut.Summary)
	}
}

func TestFixBroadScope(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Fixed errors and scanned nearby code"}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "api_test.go",
			Test:    "TestGetUser",
			Package: "api",
			Error:   "status 500, want 200",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(input, map[string]string{"scope": "broad"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify the broad template was used.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"scope": "broad"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "scan for similar issues") {
		t.Errorf("expected broad template with 'scan for similar issues', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "check nearby code") {
		t.Errorf("expected broad template with 'check nearby code', got: %s", userPrompt)
	}
}

func TestFixAttemptTracking(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Tried different approach for persistent error"}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "server_test.go",
			Test:    "TestStart",
			Package: "server",
			Error:   "port already in use",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(input, map[string]string{"scope": "targeted", "attempt": "3"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	fixOut, ok := result.Content.(FixOutput)
	if !ok {
		t.Fatalf("expected FixOutput content, got %T", result.Content)
	}
	if fixOut.Attempt != 3 {
		t.Errorf("expected attempt=3, got %d", fixOut.Attempt)
	}

	// Verify the prompt includes attempt tracking instructions.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"scope": "targeted", "attempt": "3"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "attempt 3") {
		t.Errorf("expected prompt to contain 'attempt 3', got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "different approach") {
		t.Errorf("expected prompt to contain 'different approach', got: %s", userPrompt)
	}
}

func TestFixProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: errors.New("rate limit exceeded")}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "test.go",
			Test:    "TestFoo",
			Package: "pkg",
			Error:   "failed",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
}

func TestFixMalformedInput(t *testing.T) {
	provider := &testutil.MockProvider{Response: "should not be called"}
	handler := NewHandler(provider, testConfig(), nil)

	// Input content is a string instead of structured map.
	input := envelope.New("verify", "check")
	input.Content = "this is just a string, not structured"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for malformed input")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "not a structured object") {
		t.Errorf("expected error about structured object, got: %s", result.Error.Message)
	}
}

func TestFixStreamHandler(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "Fixed the issue by updating return value"},
		Chunks:       []string{"Fixed ", "the issue ", "by updating return value"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "math_test.go",
			Test:    "TestAdd",
			Package: "math",
			Error:   "expected 5, got 4",
		},
	}

	input := verifyInput(failures, nil)

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"scope": "targeted"}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}

	fixOut, ok := result.Content.(FixOutput)
	if !ok {
		t.Fatalf("expected FixOutput content, got %T", result.Content)
	}
	if fixOut.Summary != "Fixed the issue by updating return value" {
		t.Errorf("unexpected summary: %s", fixOut.Summary)
	}
}

func TestFixStreamHandlerProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: errors.New("stream timeout")},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "test.go",
			Test:    "TestFoo",
			Package: "pkg",
			Error:   "failed",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	testutil.AssertFatalError(t, result)
}

func TestFixStreamHandlerNoFailures(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "should not be called"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := verifyInput(nil, nil)
	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	fixOut, ok := result.Content.(FixOutput)
	if !ok {
		t.Fatalf("expected FixOutput content, got %T", result.Content)
	}
	if fixOut.Summary != "Nothing to fix" {
		t.Errorf("expected summary='Nothing to fix', got %s", fixOut.Summary)
	}
}

func TestFixNilInput(t *testing.T) {
	provider := &testutil.MockProvider{Response: "should not be called"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("verify", "check")
	input.Content = nil
	input.ContentType = envelope.ContentStructured

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for nil input")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
}

// emptyProvider always returns an empty string with no error.
type emptyProvider struct{}

func (p *emptyProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (p *emptyProvider) CompleteStream(_ context.Context, _, _ string, _ func(string)) (string, error) {
	return "", nil
}

func TestFixProviderEmpty(t *testing.T) {
	handler := NewHandler(&emptyProvider{}, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "test.go",
			Test:    "TestFoo",
			Package: "pkg",
			Error:   "failed",
		},
	}

	input := verifyInput(failures, nil)
	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected warn error for empty provider response")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
}

func TestFixAttemptDefault(t *testing.T) {
	provider := &testutil.MockProvider{Response: "fixed"}
	handler := NewHandler(provider, testConfig(), nil)

	failures := []TestFailure{
		{
			File:    "test.go",
			Test:    "TestFoo",
			Package: "pkg",
			Error:   "failed",
		},
	}

	input := verifyInput(failures, nil)
	// No attempt flag specified — should default to 1.
	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	fixOut, ok := result.Content.(FixOutput)
	if !ok {
		t.Fatalf("expected FixOutput content, got %T", result.Content)
	}
	if fixOut.Attempt != 1 {
		t.Errorf("expected attempt=1, got %d", fixOut.Attempt)
	}

	// Verify the prompt does NOT contain attempt tracking for attempt 1.
	pc := testConfig()
	compiled := CompileTemplates(pc)
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if strings.Contains(userPrompt, "different approach") {
		t.Errorf("attempt 1 should not contain 'different approach' instruction, got: %s", userPrompt)
	}
}

func TestFixCompileTemplates(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	if _, ok := compiled["targeted"]; !ok {
		t.Error("expected 'targeted' template to be compiled")
	}
	if _, ok := compiled["broad"]; !ok {
		t.Error("expected 'broad' template to be compiled")
	}
}
