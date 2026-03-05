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

func (m *MockProvider) CompleteStream(_ context.Context, _, _ string, onChunk func(string)) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	onChunk(m.Response)
	return m.Response, nil
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

func TestMockProviderSatisfiesStreamingInterface(t *testing.T) {
	var p StreamingProvider = &MockProvider{Response: "hello"}
	var chunks []string
	result, err := p.CompleteStream(context.Background(), "system", "user", func(c string) {
		chunks = append(chunks, c)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected one chunk 'hello', got %v", chunks)
	}
}

func TestCreateProviderClaude(t *testing.T) {
	p, err := CreateProvider(ProviderConfig{
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

func TestCreateProviderUnknown(t *testing.T) {
	_, err := CreateProvider(ProviderConfig{Name: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestCreateProviderAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := CreateProvider(ProviderConfig{Name: "anthropic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestCreateProviderOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := CreateProvider(ProviderConfig{Name: "openai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestCreateProviderXAI(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-key")
	p, err := CreateProvider(ProviderConfig{Name: "xai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestCreateProviderGemini(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := CreateProvider(ProviderConfig{Name: "gemini"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestProvidersImplementUsageReporter(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("XAI_API_KEY", "test-key")
	t.Setenv("GEMINI_API_KEY", "test-key")

	cases := []struct {
		name     string
		provider func() (Provider, error)
	}{
		{"anthropic", func() (Provider, error) { return AnthropicProvider(ProviderConfig{Name: "anthropic"}) }},
		{"openai", func() (Provider, error) { return OpenAIProvider(ProviderConfig{Name: "openai"}, "https://api.openai.com/v1", "OPENAI_API_KEY") }},
		{"xai", func() (Provider, error) { return OpenAIProvider(ProviderConfig{Name: "xai"}, "https://api.x.ai/v1", "XAI_API_KEY") }},
		{"gemini", func() (Provider, error) { return GeminiProvider(ProviderConfig{Name: "gemini"}) }},
		{"claude", func() (Provider, error) { return ClaudeProvider(ProviderConfig{Name: "claude"}), nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.provider()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := p.(UsageReporter); !ok {
				t.Errorf("provider %s does not implement UsageReporter", tc.name)
			}
		})
	}
}

func TestCreateProviderMissingAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := CreateProvider(ProviderConfig{Name: "anthropic"})
	if err == nil {
		t.Fatal("expected error for missing anthropic API key")
	}
}

func TestParseClaudeResponseJSON(t *testing.T) {
	data := []byte(`{"result":"Hello, world!"}`)
	result, _ := parseClaudeResponseWithUsage(data, "")
	if result != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got '%s'", result)
	}
}

func TestParseClaudeResponseContentBlocks(t *testing.T) {
	data := []byte(`[{"type":"text","text":"Hello"},{"type":"text","text":" world"}]`)
	result, _ := parseClaudeResponseWithUsage(data, "")
	if result != "Hello\n world" {
		t.Errorf("expected 'Hello\\n world', got '%s'", result)
	}
}

func TestParseClaudeResponsePlainText(t *testing.T) {
	data := []byte("Just plain text response")
	result, _ := parseClaudeResponseWithUsage(data, "")
	if result != "Just plain text response" {
		t.Errorf("expected plain text, got '%s'", result)
	}
}

func TestParseClaudeResponseEmpty(t *testing.T) {
	result, _ := parseClaudeResponseWithUsage([]byte(""), "")
	if result != "" {
		t.Errorf("expected empty result, got '%s'", result)
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
			p := ClaudeProvider(ProviderConfig{
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
