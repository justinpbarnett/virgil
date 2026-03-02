package draft

import (
	"context"
	"fmt"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "draft",
		Prompts: config.PromptsConfig{
			System: "You are a professional writer.",
			Templates: map[string]string{
				"blog": "Write a blog post.\n\nSource material:\n{{.Content}}\n\n{{if .Topic}}Focus on: {{.Topic}}{{end}}",
				"email": "Draft an email.\n\nContext:\n{{.Content}}",
			},
		},
	}
}

func TestDraftWithType(t *testing.T) {
	provider := &mockProvider{response: "A great blog post about OAuth..."}
	handler := NewHandler(provider, testConfig())

	input := envelope.New("memory", "retrieve")
	input.Content = "OAuth uses short-lived tokens"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "blog"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "A great blog post about OAuth..." {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestDraftNoType(t *testing.T) {
	provider := &mockProvider{response: "Generated content"}
	handler := NewHandler(provider, testConfig())

	input := envelope.New("input", "test")
	input.Content = "Some input text"
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "Generated content" {
		t.Errorf("unexpected content: %v", result.Content)
	}
}

func TestDraftEmptyContent(t *testing.T) {
	provider := &mockProvider{response: "something"}
	handler := NewHandler(provider, testConfig())

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestDraftProviderError(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("auth failed")}
	handler := NewHandler(provider, testConfig())

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "blog"})

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if result.Error.Severity != "fatal" {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
}

func TestDraftTemplateResolution(t *testing.T) {
	cases := []struct {
		draftType string
		wantErr   bool
	}{
		{"blog", false},
		{"email", false},
		{"unknown", false}, // falls back to raw content
	}

	for _, tc := range cases {
		t.Run(tc.draftType, func(t *testing.T) {
			provider := &mockProvider{response: "output"}
			handler := NewHandler(provider, testConfig())

			input := envelope.New("input", "test")
			input.Content = "test content"
			input.ContentType = "text"

			result := handler(input, map[string]string{"type": tc.draftType})
			if tc.wantErr && result.Error == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && result.Error != nil {
				t.Errorf("unexpected error: %v", result.Error)
			}
		})
	}
}
