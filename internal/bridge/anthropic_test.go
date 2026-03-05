package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveAnthropicModel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "claude-sonnet-4-20250514"},
		{"sonnet", "claude-sonnet-4-20250514"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"opus", "claude-opus-4-6"},
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022"},
	}
	for _, tc := range cases {
		if got := resolveAnthropicModel(tc.in); got != tc.want {
			t.Errorf("resolveAnthropicModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAnthropicProviderMissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("expected error to mention ANTHROPIC_API_KEY, got: %v", err)
	}
}

func TestAnthropicProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request structure
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header 'test-key', got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header, got %q", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("content-type") != "application/json" {
			t.Errorf("expected content-type application/json")
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] == nil {
			t.Error("expected model in request body")
		}
		if _, ok := body["stream"]; ok {
			t.Error("stream should not be set for Complete()")
		}

		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Anthropic"},
			},
			"usage": map[string]int{
				"input_tokens":  100,
				"output_tokens": 50,
			},
			"model": "claude-sonnet-4-20250514",
		})
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := AnthropicProvider(ProviderConfig{Name: "anthropic", Model: "claude-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p.baseURL = srv.URL

	result, err := p.Complete(context.Background(), "system prompt", "user input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from Anthropic" {
		t.Errorf("expected 'Hello from Anthropic', got %q", result)
	}

	usage := p.LastUsage()
	if usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", usage.OutputTokens)
	}
	if usage.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got %q", usage.Model)
	}
	if usage.Cost == 0 {
		t.Error("expected non-zero cost for known model")
	}
}

func TestAnthropicProviderCompleteSystemPlacement(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	p.Complete(context.Background(), "my system", "my user")

	if capturedBody["system"] != "my system" {
		t.Errorf("expected system field 'my system', got %v", capturedBody["system"])
	}
	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("expected role 'user', got %v", msg["role"])
	}
}

func TestAnthropicProviderCompleteNoSystem(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	p.Complete(context.Background(), "", "my user")

	if _, ok := capturedBody["system"]; ok {
		t.Error("system field should not be set when system prompt is empty")
	}
}

func TestAnthropicProviderStream(t *testing.T) {
	events := []string{
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}`,
		`data: {"type":"message_stop"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Error("expected stream:true in request body")
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintln(w, e)
		}
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	var chunks []string
	result, err := p.CompleteStream(context.Background(), "sys", "user", func(c string) {
		chunks = append(chunks, c)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result)
	}
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
}

func TestAnthropicProviderStreamUsage(t *testing.T) {
	events := []string{
		`data: {"type":"message_start","message":{"model":"claude-sonnet-4-20250514","usage":{"input_tokens":200}}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}`,
		`data: {"type":"message_delta","usage":{"output_tokens":100}}`,
		`data: {"type":"message_stop"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintln(w, e)
		}
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	_, err := p.CompleteStream(context.Background(), "sys", "user", func(c string) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	usage := p.LastUsage()
	if usage.InputTokens != 200 {
		t.Errorf("expected 200 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 100 {
		t.Errorf("expected 100 output tokens, got %d", usage.OutputTokens)
	}
	if usage.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model from message_start, got %q", usage.Model)
	}
	if usage.Cost == 0 {
		t.Error("expected non-zero cost for known model")
	}
}

func TestAnthropicProviderError401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "bad-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	_, err := p.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestAnthropicProviderError429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, _ := AnthropicProvider(ProviderConfig{Name: "anthropic"})
	p.baseURL = srv.URL

	_, err := p.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	if !strings.Contains(err.Error(), "retry after 30") {
		t.Errorf("expected retry-after in error, got: %v", err)
	}
}
