package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

func testDefs() []pipe.Definition {
	return []pipe.Definition{
		{
			Name:     "calendar",
			Category: "time",
			Triggers: pipe.Triggers{
				Exact:    []string{"check my calendar", "what's on my schedule", "what's on my calendar"},
				Keywords: []string{"calendar", "schedule", "meeting", "event"},
			},
		},
		{
			Name:     "memory",
			Category: "memory",
			Triggers: pipe.Triggers{
				Exact:    []string{"remember this", "what do I know"},
				Keywords: []string{"remember", "recall", "memory", "notes", "know"},
			},
		},
		{
			Name:     "draft",
			Category: "comms",
			Triggers: pipe.Triggers{
				Exact:    []string{"write something", "draft this"},
				Keywords: []string{"draft", "compose", "write", "author"},
			},
		},
		{
			Name:     "chat",
			Category: "general",
			Triggers: pipe.Triggers{
				Exact:    []string{"hey", "hi", "hello", "hey virgil", "hi virgil", "hello virgil"},
				Keywords: []string{"chat", "talk", "hello", "hi", "hey"},
			},
		},
	}
}

func TestLayer1ExactMatch(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	result := r.Route(context.Background(), "check my calendar", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", result.Confidence)
	}
	if result.Layer != LayerExact {
		t.Errorf("expected layer %d, got %d", LayerExact, result.Layer)
	}
}

func TestLayer2KeywordScoring(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	// Signal hits 3 of 4 calendar keywords: calendar, schedule, meeting → 75% > 60% threshold
	result := r.Route(context.Background(), "show my calendar schedule for the meeting", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Layer != LayerKeyword {
		t.Errorf("expected layer %d, got %d", LayerKeyword, result.Layer)
	}
}

func TestLayer3CategoryNarrowing(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Action: "retrieve",
		Source: "memory",
		Topic:  "OAuth",
	}
	result := r.Route(context.Background(), "recall my OAuth notes", parsed)

	if result.Pipe != "memory" {
		t.Errorf("expected memory, got %s", result.Pipe)
	}
}

func TestLayer4StubFallback(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	result := r.Route(context.Background(), "xyzzy foobar", parser.ParsedSignal{})

	if result.Pipe != "chat" {
		t.Errorf("expected chat, got %s", result.Pipe)
	}
	if result.Confidence != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", result.Confidence)
	}
	if result.Layer != LayerFallback {
		t.Errorf("expected layer %d, got %d", LayerFallback, result.Layer)
	}
}

func TestLayer4MissMetadataPopulated(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	// "calendar" is a keyword match; "xyzzy" and "foobar" are not
	result := r.Route(context.Background(), "xyzzy calendar foobar", parser.ParsedSignal{})

	// Should be a keyword match for calendar, not a fallback
	// Try with completely unknown words to get Layer 4
	result = r.Route(context.Background(), "xyzzy foobar", parser.ParsedSignal{})
	if result.Layer != LayerFallback {
		t.Fatalf("expected layer 4 fallback, got layer %d", result.Layer)
	}
	// KeywordsNotFound should contain the unknown words
	if len(result.KeywordsNotFound) == 0 {
		t.Error("expected KeywordsNotFound to be populated at Layer 4")
	}
}

func TestMissLogStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "misses.jsonl")
	missLog, err := NewMissLog(path)
	if err != nil {
		t.Fatalf("failed to create miss log: %v", err)
	}
	defer missLog.Close()

	// Miss log is now written by the server; test the MissLog.Log API directly
	_ = missLog.Log(MissEntry{
		Signal:           "completely unknown input",
		KeywordsFound:    []string{},
		KeywordsNotFound: []string{"completely", "unknown", "input"},
		FallbackPipe:     "chat",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read miss log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"signal"`) {
		t.Error("expected signal field in JSONL")
	}
	if !strings.Contains(content, `"keywords_found"`) {
		t.Error("expected keywords_found field in JSONL")
	}
	if !strings.Contains(content, `"fallback_pipe"`) {
		t.Error("expected fallback_pipe field in JSONL")
	}
	if !strings.Contains(content, `"timestamp"`) {
		t.Error("expected timestamp field in JSONL")
	}
}

func TestWhQuestionFallsToChat(t *testing.T) {
	defs := append(testDefs(), pipe.Definition{
		Name:     "visualize",
		Category: "comms",
		Triggers: pipe.Triggers{
			Keywords: []string{"visualize", "animate", "animation", "illustrate", "diagram", "visualization", "manim"},
		},
	})
	r := NewRouter(defs, nil)

	// "visualize" appears in the signal as a topic of a question, not as a command
	parsed := parser.ParsedSignal{Verb: "visualize", IsQuestion: true}
	result := r.Route(context.Background(), "what's a complicated workflow that would be cool to visualize?", parsed)

	if result.Pipe != "chat" {
		t.Errorf("expected chat for wh-question, got %s", result.Pipe)
	}
	if result.Layer != LayerFallback {
		t.Errorf("expected layer %d, got %d", LayerFallback, result.Layer)
	}
}

// TestWhQuestionWithSourceRoutesToPipe verifies that a wh-question whose
// parsed source matches a pipe name routes to that pipe instead of falling
// through to chat. Regression test: "what's on my calendar today?" was
// falling to chat → study(source=calendar) → "unsupported source: calendar".
func TestWhQuestionWithSourceRoutesToPipe(t *testing.T) {
	r := NewRouter(testDefs(), nil)

	parsed := parser.ParsedSignal{
		Source:     "calendar",
		Modifier:   "today",
		IsQuestion: true,
	}
	result := r.Route(context.Background(), "what's on my calendar today?", parsed)

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar for question with source=calendar, got %s", result.Pipe)
	}
	if result.Layer != LayerCategory {
		t.Errorf("expected layer %d, got %d", LayerCategory, result.Layer)
	}
}

func TestGreetingExactMatch(t *testing.T) {
	r := NewRouter(testDefs(), nil)

	tests := []struct {
		signal string
	}{
		{"hey"},
		{"hi"},
		{"hello"},
		{"hey virgil"},
		{"Hi Virgil"},
	}

	for _, tt := range tests {
		result := r.Route(context.Background(), tt.signal, parser.ParsedSignal{})
		if result.Pipe != "chat" {
			t.Errorf("signal %q: expected chat, got %s", tt.signal, result.Pipe)
		}
		if result.Layer != LayerExact {
			t.Errorf("signal %q: expected layer %d, got %d", tt.signal, LayerExact, result.Layer)
		}
	}
}

func TestShortKeywordSignalMatchesLayer2(t *testing.T) {
	r := NewRouter(testDefs(), nil)

	// Single keyword should match even though the pipe has multiple keywords.
	// Signal coverage: 1/1 = 100% ≥ 60% threshold.
	result := r.Route(context.Background(), "meeting", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Layer != LayerKeyword {
		t.Errorf("expected layer %d, got %d", LayerKeyword, result.Layer)
	}
}

func TestExactMatchCaseInsensitive(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	result := r.Route(context.Background(), "Check My Calendar", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Layer != LayerExact {
		t.Errorf("expected layer %d, got %d", LayerExact, result.Layer)
	}
}

// TestLayer4FallbackToChat verifies that unrecognized signals default to chat.
func TestLayer4FallbackToChat(t *testing.T) {
	r := NewRouter(testDefs(), nil)

	result := r.Route(context.Background(), "xyzzy foobar totally unmatched", parser.ParsedSignal{})

	if result.Pipe != "chat" {
		t.Errorf("expected chat fallback, got %s", result.Pipe)
	}
	if result.Layer != LayerFallback {
		t.Errorf("expected layer %d, got %d", LayerFallback, result.Layer)
	}
}
