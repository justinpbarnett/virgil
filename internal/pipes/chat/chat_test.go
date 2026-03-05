package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func TestChatResponse(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Hello! How can I help you?"}
	handler := NewHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.Content = "hello there"
	input.ContentType = "text"

	result := handler(input, nil)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

func TestChatEmptyInput(t *testing.T) {
	provider := &testutil.MockProvider{Response: "anything"}
	handler := NewHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, nil)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if s == "" {
		t.Error("expected non-empty response for empty input")
	}
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

func TestChatProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: fmt.Errorf("provider down")}
	handler := NewHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.Content = "tell me about the weather"
	input.ContentType = "text"

	result := handler(input, nil)

	testutil.AssertFatalError(t, result)
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

func TestStreamResponse(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "Hello world"},
		Chunks:       []string{"Hello ", "world"},
	}
	handler := NewStreamHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.Content = "tell me about the weather"
	input.ContentType = "text"

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, nil, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "Hello world" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if len(received) != 2 || received[0] != "Hello " || received[1] != "world" {
		t.Errorf("unexpected chunks: %v", received)
	}
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

func TestStreamEmptyInput(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "anything"},
	}
	handler := NewStreamHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, nil, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if s == "" {
		t.Error("expected non-empty response for empty input")
	}
	if len(received) != 0 {
		t.Errorf("expected no chunks for empty input, got %v", received)
	}
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

func TestSynthesisPrompt(t *testing.T) {
	// When flags contain "signal" and content differs, prepareChat should
	// build a synthesis prompt combining both.
	input := envelope.New("memory", "retrieve")
	input.Content = "1. content: remember that my name is Justin"
	input.ContentType = "text"

	flags := map[string]string{"signal": "do you remember my name?"}

	_, content, empty := prepareChat(input, flags)
	if empty {
		t.Fatal("expected non-empty")
	}
	if content == "1. content: remember that my name is Justin" {
		t.Error("expected synthesis prompt, got raw content")
	}
	// Should contain both the original signal and the context
	if !strings.Contains(content, "do you remember my name?") {
		t.Error("synthesis prompt missing original signal")
	}
	if !strings.Contains(content, "remember that my name is Justin") {
		t.Error("synthesis prompt missing context")
	}
}

func TestSynthesisSkippedForDirectChat(t *testing.T) {
	// When signal equals content (direct chat, no upstream pipe), no synthesis
	input := envelope.New("signal", "input")
	input.Content = "hello there"
	input.ContentType = "text"

	flags := map[string]string{"signal": "hello there"}

	_, content, empty := prepareChat(input, flags)
	if empty {
		t.Fatal("expected non-empty")
	}
	if content != "hello there" {
		t.Errorf("expected raw content for direct chat, got %q", content)
	}
}

func TestStreamProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: fmt.Errorf("provider down")},
	}
	handler := NewStreamHandler(provider, "test system prompt", nil, nil)

	input := envelope.New("input", "test")
	input.Content = "tell me about the weather"
	input.ContentType = "text"

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, nil, sink)

	testutil.AssertFatalError(t, result)
	if len(received) != 0 {
		t.Errorf("expected no chunks on error, got %v", received)
	}
	testutil.AssertEnvelope(t, result, "chat", "respond")
}

// --- Role-based system prompt tests ---

func TestResolveSystemPrompt_Default(t *testing.T) {
	base := "You are Virgil."
	prompts := map[string]string{"general": "general prompt"}

	got := resolveSystemPrompt(prompts, base, map[string]string{})
	if got != base {
		t.Errorf("empty role: got %q, want %q", got, base)
	}
}

func TestResolveSystemPrompt_General(t *testing.T) {
	base := "You are Virgil."
	prompts := map[string]string{"general": "general prompt"}

	got := resolveSystemPrompt(prompts, base, map[string]string{"role": "general"})
	if got != base {
		t.Errorf("role=general: got %q, want %q", got, base)
	}
}

func TestResolveSystemPrompt_SpecCollaboratorInitial(t *testing.T) {
	base := "You are Virgil."
	want := "initial analysis prompt"
	prompts := map[string]string{
		"spec-collaborator-initial":  want,
		"spec-collaborator-progress": "progress prompt",
		"spec-collaborator-complete": "complete prompt",
	}

	got := resolveSystemPrompt(prompts, base, map[string]string{
		"role":  "spec-collaborator",
		"phase": "initial",
	})
	if got != want {
		t.Errorf("spec-collaborator/initial: got %q, want %q", got, want)
	}
}

func TestResolveSystemPrompt_SpecCollaboratorProgress(t *testing.T) {
	base := "You are Virgil."
	want := "progress prompt"
	prompts := map[string]string{
		"spec-collaborator-initial":  "initial prompt",
		"spec-collaborator-progress": want,
		"spec-collaborator-complete": "complete prompt",
	}

	got := resolveSystemPrompt(prompts, base, map[string]string{
		"role":  "spec-collaborator",
		"phase": "progress",
	})
	if got != want {
		t.Errorf("spec-collaborator/progress: got %q, want %q", got, want)
	}
}

func TestResolveSystemPrompt_SpecCollaboratorComplete(t *testing.T) {
	base := "You are Virgil."
	want := "complete prompt"
	prompts := map[string]string{
		"spec-collaborator-initial":  "initial prompt",
		"spec-collaborator-progress": "progress prompt",
		"spec-collaborator-complete": want,
	}

	got := resolveSystemPrompt(prompts, base, map[string]string{
		"role":  "spec-collaborator",
		"phase": "complete",
	})
	if got != want {
		t.Errorf("spec-collaborator/complete: got %q, want %q", got, want)
	}
}

func TestResolveSystemPrompt_UnknownRole(t *testing.T) {
	base := "You are Virgil."
	prompts := map[string]string{
		"spec-collaborator-progress": "progress prompt",
	}

	got := resolveSystemPrompt(prompts, base, map[string]string{
		"role":  "nonexistent",
		"phase": "progress",
	})
	if got != base {
		t.Errorf("unknown role: got %q, want %q", got, base)
	}
}

func TestResolveSystemPrompt_RoleWithoutPhase(t *testing.T) {
	base := "You are Virgil."
	prompts := map[string]string{
		"spec-collaborator-initial":  "initial prompt",
		"spec-collaborator-progress": "progress prompt",
	}

	// No phase flag — compound key can't match, role-only key doesn't exist, falls back to base.
	got := resolveSystemPrompt(prompts, base, map[string]string{
		"role": "spec-collaborator",
	})
	if got != base {
		t.Errorf("role without phase: got %q, want %q", got, base)
	}
}

func TestCompileSystemPrompts(t *testing.T) {
	pc := config.PipeConfig{
		Prompts: config.PromptsConfig{
			System: "base system prompt",
			Templates: map[string]string{
				"general":                    "general prompt",
				"spec-collaborator-initial":  "initial prompt",
				"spec-collaborator-progress": "progress prompt",
				"spec-collaborator-complete": "complete prompt",
			},
		},
	}

	got := CompileSystemPrompts(pc)

	if len(got) != 4 {
		t.Fatalf("expected 4 prompts, got %d", len(got))
	}

	expected := map[string]string{
		"general":                    "general prompt",
		"spec-collaborator-initial":  "initial prompt",
		"spec-collaborator-progress": "progress prompt",
		"spec-collaborator-complete": "complete prompt",
	}

	for key, want := range expected {
		if got[key] != want {
			t.Errorf("CompileSystemPrompts[%q]: got %q, want %q", key, got[key], want)
		}
	}
}

func TestCompileSystemPrompts_Empty(t *testing.T) {
	pc := config.PipeConfig{
		Prompts: config.PromptsConfig{
			System: "base prompt",
		},
	}

	got := CompileSystemPrompts(pc)
	if len(got) != 0 {
		t.Fatalf("expected 0 prompts, got %d", len(got))
	}
}

func TestStreamHandler_SpecCollaboratorRole(t *testing.T) {
	want := "spec-collaborator-initial system prompt"
	provider := &capturingStreamProvider{
		MockStreamProvider: testutil.MockStreamProvider{
			MockProvider: testutil.MockProvider{Response: "analysis response"},
			Chunks:       []string{"analysis ", "response"},
		},
	}
	prompts := map[string]string{
		"spec-collaborator-initial": want,
	}
	handler := NewStreamHandler(provider, "base prompt", prompts, nil)

	input := envelope.New("input", "test")
	input.Content = "spec analysis context"
	input.ContentType = "text"

	flags := map[string]string{
		"role":  "spec-collaborator",
		"phase": "initial",
	}

	var received []string
	sink := func(chunk string) { received = append(received, chunk) }

	result := handler(context.Background(), input, flags, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if provider.lastSystem != want {
		t.Errorf("expected system prompt %q, got %q", want, provider.lastSystem)
	}
}

// capturingStreamProvider wraps MockStreamProvider to capture the system prompt.
type capturingStreamProvider struct {
	testutil.MockStreamProvider
	lastSystem string
}

func (p *capturingStreamProvider) CompleteStream(ctx context.Context, system, user string, sink func(string)) (string, error) {
	p.lastSystem = system
	return p.MockStreamProvider.CompleteStream(ctx, system, user, sink)
}

func TestPrepareChat_Unchanged(t *testing.T) {
	// prepareChat should be unaffected by role changes — it handles content
	// extraction and pipeline synthesis, not system prompt selection.

	t.Run("empty content", func(t *testing.T) {
		input := envelope.Envelope{
			Content:     nil,
			ContentType: envelope.ContentText,
		}
		out, content, empty := prepareChat(input, map[string]string{})
		if !empty {
			t.Fatal("expected empty=true for nil content")
		}
		if content != "" {
			t.Errorf("expected empty content, got %q", content)
		}
		if out.Content != "I didn't catch that. Could you try again?" {
			t.Errorf("unexpected fallback message: %q", out.Content)
		}
	})

	t.Run("plain content with role flags", func(t *testing.T) {
		input := envelope.Envelope{
			Content:     "hello",
			ContentType: envelope.ContentText,
		}
		_, content, empty := prepareChat(input, map[string]string{
			"role":  "spec-collaborator",
			"phase": "initial",
		})
		if empty {
			t.Fatal("expected empty=false for non-empty content")
		}
		if content != "hello" {
			t.Errorf("expected content %q, got %q", "hello", content)
		}
	})

	t.Run("pipeline synthesis with role flags", func(t *testing.T) {
		input := envelope.Envelope{
			Content:     "retrieved context here",
			ContentType: envelope.ContentText,
		}
		flags := map[string]string{
			"signal": "original question",
			"role":   "spec-collaborator",
			"phase":  "progress",
		}
		_, content, empty := prepareChat(input, flags)
		if empty {
			t.Fatal("expected empty=false")
		}
		if content == "retrieved context here" {
			t.Error("expected pipeline synthesis to modify content")
		}
		if !strings.Contains(content, "original question") {
			t.Error("content should contain the original signal")
		}
		if !strings.Contains(content, "retrieved context here") {
			t.Error("content should contain the upstream context")
		}
	})
}
