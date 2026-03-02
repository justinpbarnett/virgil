package chat

import (
	"context"
	"fmt"
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
