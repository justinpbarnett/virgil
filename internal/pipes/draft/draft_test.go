package draft

import (
	"context"
	"fmt"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "draft",
		Prompts: config.PromptsConfig{
			System: "You are a professional writer.",
			Templates: map[string]string{
				"blog":  "Write a blog post.\n\nSource material:\n{{.Content}}\n\n{{if .Topic}}Focus on: {{.Topic}}{{end}}",
				"email": "Draft an email.\n\nContext:\n{{.Content}}",
			},
		},
	}
}

func TestDraftWithType(t *testing.T) {
	provider := &testutil.MockProvider{Response: "A great blog post about OAuth..."}
	handler := NewHandler(provider, testConfig(), nil)

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
	provider := &testutil.MockProvider{Response: "Generated content"}
	handler := NewHandler(provider, testConfig(), nil)

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
	provider := &testutil.MockProvider{Response: "something"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestDraftProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: fmt.Errorf("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(input, map[string]string{"type": "blog"})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandler(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "Full streamed blog post"},
		Chunks:       []string{"Full ", "streamed ", "blog post"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("memory", "retrieve")
	input.Content = "OAuth uses short-lived tokens"
	input.ContentType = "text"

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"type": "blog"}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "Full streamed blog post" {
		t.Errorf("unexpected content: %v", result.Content)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestStreamHandlerProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: fmt.Errorf("stream timeout")},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.Content = "content"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"type": "blog"}, func(string) {})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandlerEmptyContent(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "something"},
	}
	handler := NewStreamHandler(provider, testConfig(), nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected error for empty content")
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
			provider := &testutil.MockProvider{Response: "output"}
			handler := NewHandler(provider, testConfig(), nil)

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
