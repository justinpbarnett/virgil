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

func TestGeminiProviderMissingKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	_, err := GeminiProvider(ProviderConfig{Name: "gemini"})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("expected error to mention GEMINI_API_KEY, got: %v", err)
	}
}

func TestGeminiProviderURLConstruction(t *testing.T) {
	var capturedPath string
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.RawQuery
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{{"text": "Hello from Gemini"}},
				}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := GeminiProvider(ProviderConfig{Name: "gemini", Model: "gemini-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p.baseURL = srv.URL

	p.Complete(context.Background(), "", "test")

	if !strings.Contains(capturedPath, "gemini-test") {
		t.Errorf("expected model in URL path, got %q", capturedPath)
	}
	if !strings.Contains(capturedPath, "generateContent") {
		t.Errorf("expected generateContent in URL path, got %q", capturedPath)
	}
	if !strings.Contains(capturedQuery, "key=test-key") {
		t.Errorf("expected key in query, got %q", capturedQuery)
	}
}

func TestGeminiProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{{"text": "Hello from Gemini"}},
				}},
			},
			"usageMetadata": map[string]int{
				"promptTokenCount":     120,
				"candidatesTokenCount": 60,
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	p, _ := GeminiProvider(ProviderConfig{Name: "gemini", Model: "gemini-2.0-flash"})
	p.baseURL = srv.URL

	result, err := p.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from Gemini" {
		t.Errorf("expected 'Hello from Gemini', got %q", result)
	}

	usage := p.LastUsage()
	if usage.InputTokens != 120 {
		t.Errorf("expected 120 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 60 {
		t.Errorf("expected 60 output tokens, got %d", usage.OutputTokens)
	}
	if usage.Cost == 0 {
		t.Error("expected non-zero cost for known model gemini-2.0-flash")
	}
}

func TestGeminiProviderSystemInstructionPlacement(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{{"text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	p, _ := GeminiProvider(ProviderConfig{Name: "gemini"})
	p.baseURL = srv.URL

	p.Complete(context.Background(), "my system", "my user")

	si, ok := capturedBody["system_instruction"].(map[string]any)
	if !ok {
		t.Fatal("expected system_instruction in request body")
	}
	parts, _ := si["parts"].([]any)
	if len(parts) == 0 {
		t.Fatal("expected parts in system_instruction")
	}
	part := parts[0].(map[string]any)
	if part["text"] != "my system" {
		t.Errorf("expected system text 'my system', got %v", part["text"])
	}
}

func TestGeminiProviderNoSystemInstruction(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{{"text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	p, _ := GeminiProvider(ProviderConfig{Name: "gemini"})
	p.baseURL = srv.URL

	p.Complete(context.Background(), "", "my user")

	if _, ok := capturedBody["system_instruction"]; ok {
		t.Error("system_instruction should not be set when system prompt is empty")
	}
}

func TestGeminiProviderStream(t *testing.T) {
	events := []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "alt=sse") {
			t.Error("expected alt=sse in stream URL")
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintln(w, e)
		}
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	p, _ := GeminiProvider(ProviderConfig{Name: "gemini"})
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

func TestGeminiProviderError401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"API key invalid"}}`))
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "bad-key")
	p, _ := GeminiProvider(ProviderConfig{Name: "gemini"})
	p.baseURL = srv.URL

	_, err := p.Complete(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}
