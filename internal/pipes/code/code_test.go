package code

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
		Name: "code",
		Prompts: config.PromptsConfig{
			System: "You are a precise code generator.",
			Templates: map[string]string{
				"function": "Write a function.\n\nSpecification:\n{{.Content}}\n\n{{if .Topic}}Function purpose: {{.Topic}}{{end}}\n{{if .Lang}}Language: {{.Lang}}{{end}}\n{{if .Style}}Style: {{.Style}}{{end}}",
				"module":   "Write a complete module.\n\nSpecification:\n{{.Content}}\n\n{{if .Topic}}Module purpose: {{.Topic}}{{end}}\n{{if .Lang}}Language: {{.Lang}}{{end}}",
				"test":     "Write tests for the following code.\n\nSource code:\n{{.Content}}\n\n{{if .Topic}}Focus on: {{.Topic}}{{end}}\n{{if .Lang}}Language: {{.Lang}}{{end}}",
				"refactor": "Refactor the following code.\n\nSource code:\n{{.Content}}\n\n{{if .Topic}}Focus: {{.Topic}}{{end}}\n{{if .Lang}}Language: {{.Lang}}{{end}}",
			},
		},
	}
}

func TestCodeWithType(t *testing.T) {
	provider := &testutil.MockProvider{Response: "func Add(a, b int) int { return a + b }"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "Add two integers and return the sum"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "function"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "func Add(a, b int) int { return a + b }" {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestCodeModule(t *testing.T) {
	provider := &testutil.MockProvider{Response: "package math\n\nfunc Add(a, b int) int { return a + b }"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "A math utilities module with Add and Subtract"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "module"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
}

func TestCodeTest(t *testing.T) {
	provider := &testutil.MockProvider{Response: "func TestAdd(t *testing.T) { ... }"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "func Add(a, b int) int { return a + b }"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "test"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
}

func TestCodeRefactor(t *testing.T) {
	provider := &testutil.MockProvider{Response: "func Add(a, b int) int {\n\treturn a + b\n}"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "func add(a int, b int) int { x := a + b; return x }"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "refactor"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
}

func TestCodeNoType(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Generated code"}
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "Parse a CSV file"
	input.ContentType = "text"

	// Verify preparePrompt defaults to function template
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{})

	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Write a function") {
		t.Errorf("expected function template, got: %s", userPrompt)
	}

	// Verify handler works end-to-end
	handler := NewHandler(provider, pc, nil)
	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "Generated code" {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestCodeEmptyContent(t *testing.T) {
	provider := &testutil.MockProvider{Response: "something"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestCodeTopicFallback(t *testing.T) {
	provider := &testutil.MockProvider{Response: "func ParseCSV() {}"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{"topic": "CSV parser"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "func ParseCSV() {}" {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestCodeProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: errors.New("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "function"})

	testutil.AssertFatalError(t, result)
}

// emptyProvider always returns an empty string with no error.
type emptyProvider struct{}

func (p *emptyProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (p *emptyProvider) CompleteStream(_ context.Context, _, _ string, _ func(string)) (string, error) {
	return "", nil
}

func TestCodeProviderEmpty(t *testing.T) {
	handler := NewHandler(&emptyProvider{}, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "function"})

	if result.Error == nil {
		t.Fatal("expected warn error for empty provider response")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
	if result.Error.Retryable {
		t.Error("expected retryable=false")
	}
}

func TestStreamHandlerProviderEmpty(t *testing.T) {
	handler := NewStreamHandler(&emptyProvider{}, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"type": "function"}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected warn error for empty provider response")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
}

func TestStreamHandler(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "func Add(a, b int) int { return a + b }"},
		Chunks:       []string{"func ", "Add(a, b int) int ", "{ return a + b }"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "Add two integers"
	input.ContentType = "text"

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"type": "function"}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "func Add(a, b int) int { return a + b }" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestStreamHandlerProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: errors.New("stream timeout")},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"type": "function"}, func(string) {})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandlerEmptyContent(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "something"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestCodeTemplateResolution(t *testing.T) {
	cases := []struct {
		codeType string
		wantErr  bool
	}{
		{"function", false},
		{"module", false},
		{"test", false},
		{"refactor", false},
		{"unknown", false}, // falls back to raw content
	}

	for _, tc := range cases {
		t.Run(tc.codeType, func(t *testing.T) {
			provider := &testutil.MockProvider{Response: "output"}
			handler := NewHandler(provider, testConfig(), nil)

			input := envelope.New("input", "test")
			input.Content = "test content"
			input.ContentType = "text"

			result := handler(input, map[string]string{"type": tc.codeType})
			if tc.wantErr && result.Error == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && result.Error != nil {
				t.Errorf("unexpected error: %v", result.Error)
			}
		})
	}
}

func TestCodeLangFlag(t *testing.T) {
	provider := &testutil.MockProvider{Response: "def add(a, b): return a + b"}
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("input", "test")
	input.Content = "Add two numbers"
	input.ContentType = "text"

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"type": "function", "lang": "python"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}

	if !strings.Contains(userPrompt, "python") {
		t.Errorf("expected prompt to contain 'python', got: %s", userPrompt)
	}

	handler := NewHandler(provider, pc, nil)
	result := handler(input, map[string]string{"type": "function", "lang": "python"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}
