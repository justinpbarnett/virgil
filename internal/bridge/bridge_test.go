package bridge

import (
	"context"
	"testing"
)

// MockProvider for testing pipes that depend on Provider interface
type MockProvider struct {
	Response string
	Err      error
}

func (m *MockProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return m.Response, m.Err
}

func TestMockProviderSatisfiesInterface(t *testing.T) {
	var p Provider = &MockProvider{Response: "hello"}
	result, err := p.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestNewProviderClaude(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:  "claude",
		Model: "sonnet",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewProviderUnknown(t *testing.T) {
	_, err := NewProvider(ProviderConfig{Name: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestParseClaudeResponseJSON(t *testing.T) {
	data := []byte(`{"result":"Hello, world!"}`)
	result, err := parseClaudeResponse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got '%s'", result)
	}
}

func TestParseClaudeResponseContentBlocks(t *testing.T) {
	data := []byte(`[{"type":"text","text":"Hello"},{"type":"text","text":" world"}]`)
	result, err := parseClaudeResponse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello\n world" {
		t.Errorf("expected 'Hello\\n world', got '%s'", result)
	}
}

func TestParseClaudeResponsePlainText(t *testing.T) {
	data := []byte("Just plain text response")
	result, err := parseClaudeResponse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Just plain text response" {
		t.Errorf("expected plain text, got '%s'", result)
	}
}

func TestParseClaudeResponseEmpty(t *testing.T) {
	_, err := parseClaudeResponse([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}
