package router

import (
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
				Keywords: []string{"chat", "talk", "hello"},
			},
		},
	}
}

func TestLayer1ExactMatch(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	result := r.Route("check my calendar", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", result.Confidence)
	}
	if result.Layer != 1 {
		t.Errorf("expected layer 1, got %d", result.Layer)
	}
}

func TestLayer2KeywordScoring(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	// Signal hits 3 of 4 calendar keywords: calendar, schedule, meeting → 75% > 60% threshold
	result := r.Route("show my calendar schedule for the meeting", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Layer != 2 {
		t.Errorf("expected layer 2, got %d", result.Layer)
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
	result := r.Route("recall my OAuth notes", parsed)

	if result.Pipe != "memory" {
		t.Errorf("expected memory, got %s", result.Pipe)
	}
}

func TestLayer4StubFallback(t *testing.T) {
	dir := t.TempDir()
	missLog, _ := NewMissLog(filepath.Join(dir, "misses.jsonl"))
	defer missLog.Close()

	r := NewRouter(testDefs(), missLog)
	result := r.Route("xyzzy foobar", parser.ParsedSignal{})

	if result.Pipe != "chat" {
		t.Errorf("expected chat, got %s", result.Pipe)
	}
	if result.Confidence != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", result.Confidence)
	}
	if result.Layer != 4 {
		t.Errorf("expected layer 4, got %d", result.Layer)
	}

	// Verify miss was logged
	data, err := os.ReadFile(filepath.Join(dir, "misses.jsonl"))
	if err != nil {
		t.Fatalf("failed to read miss log: %v", err)
	}
	if !strings.Contains(string(data), "xyzzy foobar") {
		t.Error("expected miss log to contain the signal")
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

	r := NewRouter(testDefs(), missLog)
	r.Route("completely unknown input", parser.ParsedSignal{})

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

func TestExactMatchCaseInsensitive(t *testing.T) {
	r := NewRouter(testDefs(), nil)
	result := r.Route("Check My Calendar", parser.ParsedSignal{})

	if result.Pipe != "calendar" {
		t.Errorf("expected calendar, got %s", result.Pipe)
	}
	if result.Layer != 1 {
		t.Errorf("expected layer 1, got %d", result.Layer)
	}
}
