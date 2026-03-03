package draft

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "draft",
		Prompts: config.PromptsConfig{
			System: "You are a professional writer.",
			Templates: map[string]string{
				"blog":        "Write a blog post.\n\nSource material:\n{{.Content}}\n\n{{if .Topic}}Focus on: {{.Topic}}{{end}}",
				"email":       "Draft an email.\n\nContext:\n{{.Content}}",
				"spec-create": "Create spec.\n\nAnalysis:\n{{.Content}}\n\n{{if .CodebaseContext}}Codebase context:\n{{.CodebaseContext}}\n{{end}}",
				"spec-update": "Update spec.\n\nCurrent spec:\n{{.State}}\n\nNew analysis:\n{{.Content}}",
			},
		},
	}
}

func TestDraftWithType(t *testing.T) {
	provider := &testutil.MockProvider{Response: "A great blog post about OAuth..."}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("memory", "retrieve")
	input.Content = "OAuth uses short-lived tokens"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "blog"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "A great blog post about OAuth..." {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestDraftNoType(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Generated content"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "Some input text"
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "Generated content" {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestDraftEmptyContent(t *testing.T) {
	provider := &testutil.MockProvider{Response: "something"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestDraftProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: fmt.Errorf("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "blog"})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandler(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "Full streamed blog post"},
		Chunks:       []string{"Full ", "streamed ", "blog post"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("memory", "retrieve")
	input.Content = "OAuth uses short-lived tokens"
	input.ContentType = "text"

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"type": "blog"}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "Full streamed blog post" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestStreamHandlerProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: fmt.Errorf("stream timeout")},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"type": "blog"}, func(string) {})

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

func TestDraftTemplateResolution(t *testing.T) {
	cases := []struct {
		draftType string
		wantErr   bool
	}{
		{"blog", false},
		{"email", false},
		{"unknown", false}, // falls back to raw content
	}

	for _, tc := range cases {
		t.Run(tc.draftType, func(t *testing.T) {
			provider := &testutil.MockProvider{Response: "output"}
			handler := NewHandler(provider, testConfig(), nil)

			input := envelope.New("input", "test")
			input.Content = "test content"
			input.ContentType = "text"

			result := handler(input, map[string]string{"type": tc.draftType})
			if tc.wantErr && result.Error == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && result.Error != nil {
				t.Errorf("unexpected error: %v", result.Error)
			}
		})
	}
}

func TestPreparePrompt_SpecCreate(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("study", "analyze")
	input.Content = "Analysis of the codebase shows..."
	input.ContentType = "text"

	flags := map[string]string{
		"type":    "spec",
		"phase":   "create",
		"context": "func main() { ... }",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Create spec.") {
		t.Errorf("expected spec-create template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Analysis of the codebase shows...") {
		t.Errorf("expected content in prompt, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "func main() { ... }") {
		t.Errorf("expected codebase context in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_SpecCreateDefaultPhase(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("study", "analyze")
	input.Content = "Analysis output"
	input.ContentType = "text"

	// phase absent defaults to create in the pipe config, so the caller
	// would pass "create". Verify the compound key resolves.
	flags := map[string]string{
		"type":  "spec",
		"phase": "create",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Create spec.") {
		t.Errorf("expected spec-create template, got: %s", userPrompt)
	}
}

func TestPreparePrompt_SpecUpdate(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("study", "analyze")
	input.Content = "New findings from round 2"
	input.ContentType = "text"

	flags := map[string]string{
		"type":  "spec",
		"phase": "update",
		"state": "# Existing Spec\n## Summary\nThis is the current spec.",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Update spec.") {
		t.Errorf("expected spec-update template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "# Existing Spec") {
		t.Errorf("expected state in prompt, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "New findings from round 2") {
		t.Errorf("expected content in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_CompoundKeyFallback(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "some content"
	input.ContentType = "text"

	// phase=nonexistent means compound key "spec-nonexistent" won't match.
	// No plain "spec" template exists either, so it falls back to raw content.
	flags := map[string]string{
		"type":  "spec",
		"phase": "nonexistent",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	// Should fall back to raw content since neither "spec-nonexistent" nor "spec" exist
	if userPrompt != "some content" {
		t.Errorf("expected raw content fallback, got: %s", userPrompt)
	}
}

func TestPreparePrompt_NonSpecPhaseIgnored(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "blog material"
	input.ContentType = "text"

	// type=blog, phase=update => compound key "blog-update" doesn't exist,
	// falls back to "blog" template.
	flags := map[string]string{
		"type":  "blog",
		"phase": "update",
	}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Write a blog post.") {
		t.Errorf("expected blog template, got: %s", userPrompt)
	}
}

func TestTemplateData_NewFields(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	// Verify State is accessible in spec-update template
	data := templateData{
		Content: "analysis results",
		State:   "existing spec document",
	}
	result := executeTemplate(compiled, "spec-update", data)
	if !strings.Contains(result, "existing spec document") {
		t.Errorf("expected State in output, got: %s", result)
	}

	// Verify CodebaseContext is accessible in spec-create template
	data2 := templateData{
		Content:         "analysis output",
		CodebaseContext: "relevant code snippets",
	}
	result2 := executeTemplate(compiled, "spec-create", data2)
	if !strings.Contains(result2, "relevant code snippets") {
		t.Errorf("expected CodebaseContext in output, got: %s", result2)
	}
}
