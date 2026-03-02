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

func TestClaudeProviderMaxTurnsInArgs(t *testing.T) {
	cases := []struct {
		name     string
		maxTurns int
		want     string
	}{
		{"zero turns", 0, "0"},
		{"one turn", 1, "1"},
		{"three turns", 3, "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewClaudeProvider(ProviderConfig{
				Name:     "claude",
				Model:    "sonnet",
				MaxTurns: tc.maxTurns,
			})
			args := p.buildArgs("")
			// Find --max-turns and its value
			found := false
			for i, arg := range args {
				if arg == "--max-turns" && i+1 < len(args) {
					found = true
					if args[i+1] != tc.want {
						t.Errorf("expected --max-turns %s, got %s", tc.want, args[i+1])
					}
					break
				}
			}
			if !found {
				t.Error("--max-turns not found in args")
			}
		})
	}
}
