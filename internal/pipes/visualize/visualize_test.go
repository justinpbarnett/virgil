package visualize

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// MockRenderer implements Renderer for testing.
type MockRenderer struct {
	OutputPath string
	Err        error
}

func (m *MockRenderer) Render(_ context.Context, _, _, _ string) (string, error) {
	return m.OutputPath, m.Err
}

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "visualize",
		Prompts: config.PromptsConfig{
			System: "You are an expert Manim animator.",
			Templates: map[string]string{
				"math":    "MATH TEMPLATE\n\nConcept: {{.Topic}}\n{{if .Content}}Context: {{.Content}}\n{{end}}{{if .Duration}}Duration: {{.Duration}} ({{.DurationGuide}})\n{{end}}{{if .Style}}Style: {{.StyleGuide}}\n{{end}}",
				"concept": "CONCEPT TEMPLATE\n\nConcept: {{.Topic}}\n{{if .Content}}Context: {{.Content}}\n{{end}}{{if .Duration}}Duration: {{.Duration}} ({{.DurationGuide}})\n{{end}}{{if .Style}}Style: {{.StyleGuide}}\n{{end}}",
				"code":    "CODE TEMPLATE\n\nAlgorithm: {{.Topic}}\n{{if .Content}}Code: {{.Content}}\n{{end}}{{if .Duration}}Duration: {{.Duration}} ({{.DurationGuide}})\n{{end}}{{if .Style}}Style: {{.StyleGuide}}\n{{end}}",
				"data":    "DATA TEMPLATE\n\nData: {{.Topic}}\n{{if .Content}}Context: {{.Content}}\n{{end}}{{if .Duration}}Duration: {{.Duration}} ({{.DurationGuide}})\n{{end}}{{if .Style}}Style: {{.StyleGuide}}\n{{end}}",
			},
		},
	}
}

const validManimCode = `from manim import *

class GeneratedScene(Scene):
    def construct(self):
        circle = Circle()
        self.play(Create(circle))
        self.wait()`

func TestCompileTemplates(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	for _, key := range []string{"math", "concept", "code", "data"} {
		if _, ok := compiled[key]; !ok {
			t.Errorf("expected template %q to be compiled", key)
		}
	}
}

func TestPreparePrompt_ConceptDefault(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "binary search"}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "CONCEPT TEMPLATE") {
		t.Errorf("expected concept template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "binary search") {
		t.Errorf("expected topic in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_Math(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "Fourier transform", "type": "math"}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "MATH TEMPLATE") {
		t.Errorf("expected math template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Fourier transform") {
		t.Errorf("expected topic in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_WithDuration(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "recursion", "duration": "medium"}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "15-30 seconds, 5-10 animation steps") {
		t.Errorf("expected medium duration guide in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_WithStyle(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "sorting", "style": "3b1b"}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Dark background (#1a1a2e or BLACK)") {
		t.Errorf("expected 3b1b style guide in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_NoContent(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	// No content in envelope, topic comes from flags
	flags := map[string]string{"topic": "Pythagorean theorem"}

	_, userPrompt, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("expected success when topic is in flags, got: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Pythagorean theorem") {
		t.Errorf("expected topic in prompt, got: %s", userPrompt)
	}
}

func TestPreparePrompt_NoTopicNoContent(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	flags := map[string]string{}

	_, _, errEnv := preparePrompt(compiled, cfg, input, flags)
	if errEnv == nil {
		t.Fatal("expected fatal error when no topic or content provided")
	}
	if errEnv.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", errEnv.Severity)
	}
}

func TestExtractCode_CleanPython(t *testing.T) {
	code, err := extractCode(validManimCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(code, "from manim import *") {
		t.Errorf("expected import preserved, got: %s", code)
	}
	if !strings.Contains(code, "class GeneratedScene") {
		t.Errorf("expected class preserved, got: %s", code)
	}
}

func TestExtractCode_MarkdownFenced(t *testing.T) {
	fenced := "```python\n" + validManimCode + "\n```"
	code, err := extractCode(fenced)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(code, "```") {
		t.Errorf("expected fences stripped, got: %s", code)
	}
	if strings.HasPrefix(code, "python") {
		t.Errorf("expected language identifier stripped, got: %s", code)
	}
	if !strings.Contains(code, "from manim import *") {
		t.Errorf("expected import preserved, got: %s", code)
	}
}

func TestExtractCode_MissingScene(t *testing.T) {
	code := "from manim import *\n\nclass WrongName(Scene):\n    def construct(self):\n        pass"
	_, err := extractCode(code)
	if err == nil {
		t.Fatal("expected error for missing GeneratedScene class")
	}
}

func TestExtractCode_MissingImport(t *testing.T) {
	code := "import manim\n\nclass GeneratedScene(Scene):\n    def construct(self):\n        pass"
	_, err := extractCode(code)
	if err == nil {
		t.Fatal("expected error for missing 'from manim import'")
	}
}

func TestDurationGuide(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"short", "5-15 seconds, 3-5 animation steps"},
		{"medium", "15-30 seconds, 5-10 animation steps"},
		{"long", "30-60 seconds, 10-20 animation steps"},
	}
	for _, tc := range cases {
		got := durationGuide(tc.input)
		if got != tc.want {
			t.Errorf("durationGuide(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStyleGuide(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"3b1b", "Dark background (#1a1a2e or BLACK), blue/yellow/white color scheme, clean sans-serif typography, smooth transformations"},
		{"light", "White background, dark text, muted color palette, professional presentation style"},
		{"minimal", "Black background, white objects only, no color, stark geometric aesthetic"},
	}
	for _, tc := range cases {
		got := styleGuide(tc.input)
		if got != tc.want {
			t.Errorf("styleGuide(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestQualityFlag(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"low", "-ql"},
		{"medium", "-qm"},
		{"high", "-qh"},
	}
	for _, tc := range cases {
		got := qualityFlag(tc.input)
		if got != tc.want {
			t.Errorf("qualityFlag(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFormatFlag(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"mp4", ""},
		{"gif", "--format=gif"},
		{"png", "--format=png -s"},
	}
	for _, tc := range cases {
		got := formatFlag(tc.input)
		if got != tc.want {
			t.Errorf("formatFlag(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNewHandler_Success(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	provider := &testutil.MockProvider{Response: validManimCode}
	renderer := &MockRenderer{OutputPath: "/tmp/manim/out.mp4"}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{
		"topic":   "Fourier transform",
		"format":  "mp4",
		"quality": "low",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "visualize" {
		t.Errorf("expected pipe=visualize, got %s", result.Pipe)
	}
	if result.Action != "render" {
		t.Errorf("expected action=render, got %s", result.Action)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	m, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected content to be map[string]any, got %T", result.Content)
	}
	if m["path"] != "/tmp/manim/out.mp4" {
		t.Errorf("expected path=/tmp/manim/out.mp4, got %v", m["path"])
	}
	if m["format"] != "mp4" {
		t.Errorf("expected format=mp4, got %v", m["format"])
	}
	if m["quality"] != "low" {
		t.Errorf("expected quality=low, got %v", m["quality"])
	}
	desc, _ := m["description"].(string)
	if !strings.Contains(desc, "Fourier transform") {
		t.Errorf("expected description to contain topic, got %v", desc)
	}
}

func TestNewHandler_ProviderTimeout(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	provider := &testutil.MockProvider{Err: context.DeadlineExceeded}
	renderer := &MockRenderer{}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "Fourier transform"}

	result := handler(input, flags)

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Errorf("expected retryable error, got severity=%s retryable=%v", result.Error.Severity, result.Error.Retryable)
	}
}

func TestNewHandler_ProviderFatal(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	provider := &testutil.MockProvider{Err: fmt.Errorf("auth failed")}
	renderer := &MockRenderer{}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "Fourier transform"}

	result := handler(input, flags)

	testutil.AssertFatalError(t, result)
}

func TestNewHandler_InvalidCode(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	// Provider returns prose without valid Manim code
	provider := &testutil.MockProvider{Response: "Here is an explanation of the Fourier transform without any code."}
	renderer := &MockRenderer{}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "Fourier transform"}

	result := handler(input, flags)

	testutil.AssertFatalError(t, result)
}

func TestNewHandler_RenderFailure(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	provider := &testutil.MockProvider{Response: validManimCode}
	renderer := &MockRenderer{Err: fmt.Errorf("manim not installed")}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{"topic": "Fourier transform"}

	result := handler(input, flags)

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Errorf("expected retryable error for render failure, got severity=%s retryable=%v", result.Error.Severity, result.Error.Retryable)
	}
}

func TestNewHandler_EnvelopeCompliance(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	provider := &testutil.MockProvider{Response: validManimCode}
	renderer := &MockRenderer{OutputPath: "/tmp/out.mp4"}

	handler := NewHandler(provider, cfg, compiled, renderer, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{
		"topic":   "binary search",
		"format":  "mp4",
		"quality": "low",
	}

	result := handler(input, flags)

	// Verify required envelope fields
	if result.Pipe == "" {
		t.Error("expected non-empty pipe")
	}
	if result.Action == "" {
		t.Error("expected non-empty action")
	}
	if result.Args == nil {
		t.Error("expected non-nil args")
	}
	if result.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
	if result.Content == nil {
		t.Error("expected non-nil content on success")
	}
	if result.ContentType == "" {
		t.Error("expected non-empty content_type on success")
	}
	// error field is nil on success — that's correct
}
