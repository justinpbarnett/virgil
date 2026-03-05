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

func TestOpenAIProviderMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := OpenAIProvider(ProviderConfig{Name: "openai"}, "https://api.openai.com/v1", "OPENAI_API_KEY")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("expected error to mention OPENAI_API_KEY, got: %v", err)
	}
}

func TestOpenAIProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("expected Authorization 'Bearer test-key', got %q", auth)
		}
		if r.Header.Get("content-type") != "application/json" {
			t.Errorf("expected content-type application/json")
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream"]; ok {
			t.Error("stream should not be set for Complete()")
		}

		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "Hello from OpenAI"}},
			},
			"usage": map[string]int{
				"prompt_tokens":     150,
				"completion_tokens": 75,
			},
			"model": "gpt-4o",
		})
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := OpenAIProvider(ProviderConfig{Name: "openai", Model: "gpt-4o"}, srv.URL, "OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := p.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from OpenAI" {
		t.Errorf("expected 'Hello from OpenAI', got %q", result)
	}

	usage := p.LastUsage()
	if usage.InputTokens != 150 {
		t.Errorf("expected 150 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 75 {
		t.Errorf("expected 75 output tokens, got %d", usage.OutputTokens)
	}
	if usage.Cost == 0 {
		t.Error("expected non-zero cost for known model")
	}
}

func TestOpenAIProviderSystemAsMessage(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p, _ := OpenAIProvider(ProviderConfig{Name: "openai"}, srv.URL, "OPENAI_API_KEY")

	p.Complete(context.Background(), "my system", "my user")

	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("expected first message role 'system', got %v", sys["role"])
	}
	if sys["content"] != "my system" {
		t.Errorf("expected system content 'my system', got %v", sys["content"])
	}
	usr := msgs[1].(map[string]any)
	if usr["role"] != "user" {
		t.Errorf("expected second message role 'user', got %v", usr["role"])
	}
}

func TestOpenAIProviderBaseURLOverride(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "xAI response"}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("XAI_API_KEY", "xai-key")
	p, err := OpenAIProvider(ProviderConfig{Name: "xai"}, srv.URL, "XAI_API_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := p.Complete(context.Background(), "", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected request to be made to xAI server")
	}
	if result != "xAI response" {
		t.Errorf("expected 'xAI response', got %q", result)
	}
}

func TestOpenAIProviderStream(t *testing.T) {
	events := []string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":25},"model":"gpt-4o"}`,
		`data: [DONE]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Error("expected stream:true in request body")
		}
		// Verify stream_options is set
		opts, _ := body["stream_options"].(map[string]any)
		if opts["include_usage"] != true {
			t.Error("expected stream_options.include_usage:true in request body")
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintln(w, e)
		}
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p, _ := OpenAIProvider(ProviderConfig{Name: "openai"}, srv.URL, "OPENAI_API_KEY")

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

	usage := p.LastUsage()
	if usage.InputTokens != 50 {
		t.Errorf("expected 50 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 25 {
		t.Errorf("expected 25 output tokens, got %d", usage.OutputTokens)
	}
	if usage.Cost == 0 {
		t.Error("expected non-zero cost for known model")
	}
}

func TestOpenAIProviderError400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	p, _ := OpenAIProvider(ProviderConfig{Name: "openai"}, srv.URL, "OPENAI_API_KEY")

	_, err := p.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got: %v", err)
	}
}
