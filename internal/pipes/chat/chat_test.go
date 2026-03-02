package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func TestChatResponse(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Hello! How can I help you?"}
	handler := NewHandler(provider, "test system prompt", nil)

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
	handler := NewHandler(provider, "test system prompt", nil)

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
	handler := NewHandler(provider, "test system prompt", nil)

	input := envelope.New("input", "test")
	input.Content = "hello"
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
	handler := NewStreamHandler(provider, "test system prompt", nil)

	input := envelope.New("input", "test")
	input.Content = "hello"
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
	handler := NewStreamHandler(provider, "test system prompt", nil)

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
	handler := NewStreamHandler(provider, "test system prompt", nil)

	input := envelope.New("input", "test")
	input.Content = "hello"
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
