package educate

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
		Name: "educate",
		Prompts: config.PromptsConfig{
			System: "You are a Socratic tutor.",
			Templates: map[string]string{
				"assess": "Gauge understanding.\n\nSubject: {{.Topic}}\n{{if .Content}}Context:\n{{.Content}}\n{{end}}{{if .Level}}Level: {{.Level}}{{end}}",
				"teach":  "Continue teaching.\n\nSubject: {{.Topic}}\n\nStudent's response:\n{{.Content}}\n\n{{if .Style}}Teaching style: {{.Style}}{{end}}",
				"consolidate": "Wrap up session.\n\nSubject: {{.Topic}}\n\nConversation:\n{{.Content}}\n",
			},
		},
	}
}

func TestCompileTemplates(t *testing.T) {
	compiled := CompileTemplates(testConfig())

	for _, name := range []string{"assess", "teach", "consolidate"} {
		if _, ok := compiled[name]; !ok {
			t.Errorf("missing compiled template: %s", name)
		}
	}

	if len(compiled) != 3 {
		t.Errorf("expected 3 templates, got %d", len(compiled))
	}
}

func TestResolvePrompt_Assess(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	flags := map[string]string{"phase": "assess", "topic": "kubernetes"}

	system, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if system != cfg.Prompts.System {
		t.Errorf("expected system prompt %q, got %q", cfg.Prompts.System, system)
	}
	if !strings.Contains(user, "kubernetes") {
		t.Errorf("expected topic in user prompt, got: %s", user)
	}
	if !strings.Contains(user, "Gauge understanding.") {
		t.Errorf("expected assess template, got: %s", user)
	}
}

func TestResolvePrompt_Teach(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "I think pods are like containers"
	input.ContentType = "text"

	flags := map[string]string{"phase": "teach", "topic": "kubernetes"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "Continue teaching.") {
		t.Errorf("expected teach template, got: %s", user)
	}
	if !strings.Contains(user, "I think pods are like containers") {
		t.Errorf("expected student response in prompt, got: %s", user)
	}
}

func TestResolvePrompt_Consolidate(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "Full conversation history here"
	input.ContentType = "text"

	flags := map[string]string{"phase": "consolidate", "topic": "recursion"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "Wrap up session.") {
		t.Errorf("expected consolidate template, got: %s", user)
	}
	if !strings.Contains(user, "recursion") {
		t.Errorf("expected topic in prompt, got: %s", user)
	}
}

func TestResolvePrompt_DefaultPhase(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	// No phase flag — should default to assess
	flags := map[string]string{"topic": "git"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "Gauge understanding.") {
		t.Errorf("expected assess template as default, got: %s", user)
	}
}

func TestResolvePrompt_WithLevel(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	flags := map[string]string{"phase": "assess", "topic": "docker", "level": "beginner"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "beginner") {
		t.Errorf("expected level in prompt, got: %s", user)
	}
}

func TestResolvePrompt_WithStyle(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "I know the basics"
	input.ContentType = "text"

	flags := map[string]string{"phase": "teach", "topic": "golang", "style": "challenge"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "challenge") {
		t.Errorf("expected style in prompt, got: %s", user)
	}
}

func TestResolvePrompt_EmptyContent(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	flags := map[string]string{"phase": "assess", "topic": "networking"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	// Should still work — assess phase doesn't require prior content
	if !strings.Contains(user, "networking") {
		t.Errorf("expected topic in prompt, got: %s", user)
	}
}

func TestResolvePrompt_TopicFromFlags(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "some input"
	input.ContentType = "text"

	flags := map[string]string{"phase": "assess", "topic": "distributed systems"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "distributed systems") {
		t.Errorf("expected topic from flags in prompt, got: %s", user)
	}
}

func TestResolvePrompt_UnknownPhase(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "some content"
	input.ContentType = "text"

	flags := map[string]string{"phase": "nonexistent", "topic": "testing"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	// Should fall back to raw content
	if user != "some content" {
		t.Errorf("expected raw content fallback for unknown phase, got: %s", user)
	}
}

func TestResolvePrompt_TopicFallsBackToContent(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("input", "test")
	input.Content = "teach me about kubernetes"
	input.ContentType = "text"

	// No topic flag — should use content as topic
	flags := map[string]string{"phase": "assess"}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "teach me about kubernetes") {
		t.Errorf("expected content as topic fallback, got: %s", user)
	}
}

func TestNewHandler_Success(t *testing.T) {
	provider := &testutil.MockProvider{Response: "What do you think a Pod actually runs?"}
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	handler := NewHandler(provider, cfg, compiled, nil)

	input := envelope.New("input", "test")
	input.Content = "teach me kubernetes"
	input.ContentType = "text"

	result := handler(input, map[string]string{"phase": "assess", "topic": "kubernetes"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "What do you think a Pod actually runs?" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	testutil.AssertEnvelope(t, result, "educate", "teach")
}

func TestNewHandler_ProviderTimeout(t *testing.T) {
	provider := &testutil.MockProvider{Err: context.DeadlineExceeded}
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	handler := NewHandler(provider, cfg, compiled, nil)

	input := envelope.New("input", "test")
	input.Content = "teach me golang"
	input.ContentType = "text"

	result := handler(input, map[string]string{"topic": "golang"})

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable error for timeout")
	}
	testutil.AssertEnvelope(t, result, "educate", "teach")
}

func TestNewHandler_ProviderFatal(t *testing.T) {
	provider := &testutil.MockProvider{Err: fmt.Errorf("auth failed")}
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	handler := NewHandler(provider, cfg, compiled, nil)

	input := envelope.New("input", "test")
	input.Content = "teach me"
	input.ContentType = "text"

	result := handler(input, map[string]string{"topic": "anything"})

	testutil.AssertFatalError(t, result)
	testutil.AssertEnvelope(t, result, "educate", "teach")
}

func TestNewStreamHandler_Success(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "What comes to mind when you hear recursion?"},
		Chunks:       []string{"What comes ", "to mind ", "when you hear recursion?"},
	}
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	handler := NewStreamHandler(provider, cfg, compiled, nil)

	input := envelope.New("input", "test")
	input.Content = "teach me recursion"
	input.ContentType = "text"

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, map[string]string{"phase": "assess", "topic": "recursion"}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "What comes to mind when you hear recursion?" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if len(received) != 3 {
		t.Errorf("expected 3 chunks, got %d: %v", len(received), received)
	}
	testutil.AssertEnvelope(t, result, "educate", "teach")
}

func TestNewStreamHandler_ProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: fmt.Errorf("stream failed")},
	}
	cfg := testConfig()
	compiled := CompileTemplates(cfg)
	handler := NewStreamHandler(provider, cfg, compiled, nil)

	input := envelope.New("input", "test")
	input.Content = "teach me"
	input.ContentType = "text"

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, map[string]string{"topic": "anything"}, sink)

	testutil.AssertFatalError(t, result)
	if len(received) != 0 {
		t.Errorf("expected no chunks on error, got %v", received)
	}
	testutil.AssertEnvelope(t, result, "educate", "teach")
}

func TestResolvePrompt_PipelineSynthesis(t *testing.T) {
	cfg := testConfig()
	compiled := CompileTemplates(cfg)

	input := envelope.New("memory", "retrieve")
	input.Content = "Previous notes about kubernetes networking"
	input.ContentType = "text"

	flags := map[string]string{
		"phase":  "teach",
		"topic":  "kubernetes",
		"signal": "teach me about kubernetes networking",
	}

	_, user, errEnv := resolvePrompt(compiled, cfg, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(user, "teach me about kubernetes networking") {
		t.Errorf("expected original signal in prompt, got: %s", user)
	}
	if !strings.Contains(user, "Previous notes about kubernetes networking") {
		t.Errorf("expected upstream context in prompt, got: %s", user)
	}
}
