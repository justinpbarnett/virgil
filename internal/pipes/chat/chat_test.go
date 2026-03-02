package chat

import (
	"context"
	"fmt"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

func TestChatResponse(t *testing.T) {
	provider := &mockProvider{response: "Hello! How can I help you?"}
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
}

func TestChatEmptyInput(t *testing.T) {
	provider := &mockProvider{response: "anything"}
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
}

func TestChatProviderError(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("provider down")}
	handler := NewHandler(provider, "test system prompt", nil)

	input := envelope.New("input", "test")
	input.Content = "hello"
	input.ContentType = "text"

	result := handler(input, nil)

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if result.Error.Severity != "fatal" {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
}
