package envelope

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	e := New("memory", "retrieve")

	if e.Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", e.Pipe)
	}
	if e.Action != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", e.Action)
	}
	if e.Args == nil {
		t.Error("expected args to be initialized")
	}
	if e.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
	if e.Error != nil {
		t.Error("expected error to be nil on new envelope")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	e := New("draft", "generate")
	e.Content = "hello world"
	e.ContentType = "text"
	e.Duration = 150 * time.Millisecond
	e.Args["type"] = "blog"

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Pipe != "draft" {
		t.Errorf("expected pipe=draft, got %s", decoded.Pipe)
	}
	if decoded.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", decoded.ContentType)
	}
	if decoded.Args["type"] != "blog" {
		t.Errorf("expected args[type]=blog, got %s", decoded.Args["type"])
	}
}

func TestJSONRoundTripWithError(t *testing.T) {
	e := New("calendar", "list")
	e.Error = &EnvelopeError{
		Message:   "API timeout",
		Severity:  "error",
		Retryable: true,
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Error == nil {
		t.Fatal("expected error to be populated")
	}
	if decoded.Error.Message != "API timeout" {
		t.Errorf("expected message=API timeout, got %s", decoded.Error.Message)
	}
	if decoded.Error.Severity != "error" {
		t.Errorf("expected severity=error, got %s", decoded.Error.Severity)
	}
	if !decoded.Error.Retryable {
		t.Error("expected retryable=true")
	}
}

func TestContentToText_Text(t *testing.T) {
	result := ContentToText("hello world", "text")
	if result != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", result)
	}
}

func TestContentToText_ListOfStrings(t *testing.T) {
	items := []any{"first", "second", "third"}
	result := ContentToText(items, "list")
	expected := "1. first\n2. second\n3. third"
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

type testEntry struct {
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"created_at"`
}

func TestContentToText_ListOfStructs(t *testing.T) {
	items := []any{
		testEntry{Content: "OAuth notes", Tags: "auth", CreatedAt: "2026-01-01"},
		testEntry{Content: "JWT info", Tags: "auth", CreatedAt: "2026-01-02"},
	}
	result := ContentToText(items, "list")
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "content: OAuth notes") {
		t.Errorf("expected content field, got:\n%s", result)
	}
}

func TestContentToText_Structured(t *testing.T) {
	data := map[string]any{
		"title": "Meeting",
		"time":  "10:00",
	}
	result := ContentToText(data, "structured")
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "title: Meeting") {
		t.Errorf("expected 'title: Meeting' in result, got:\n%s", result)
	}
}

func TestMemoryEntryJSONRoundTrip(t *testing.T) {
	e := New("draft", "generate")
	e.Memory = []MemoryEntry{
		{Type: "topic_history", Content: "previous session about Go"},
		{Type: "working_state", Content: "current project: virgil"},
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Memory) != 2 {
		t.Fatalf("expected 2 memory entries, got %d", len(decoded.Memory))
	}
	if decoded.Memory[0].Type != "topic_history" {
		t.Errorf("expected type=topic_history, got %s", decoded.Memory[0].Type)
	}
	if decoded.Memory[1].Content != "current project: virgil" {
		t.Errorf("expected content match, got %s", decoded.Memory[1].Content)
	}
}

func TestMemoryOmittedWhenEmpty(t *testing.T) {
	e := New("chat", "respond")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(data), "memory") {
		t.Errorf("expected memory field to be omitted when empty, got: %s", string(data))
	}
}

func TestContentToText_Nil(t *testing.T) {
	result := ContentToText(nil, "text")
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestContentToText_Binary(t *testing.T) {
	result := ContentToText([]byte{0x00, 0x01}, "binary")
	if result != "[binary content]" {
		t.Errorf("expected '[binary content]', got '%s'", result)
	}
}

func TestContentToText_Unknown(t *testing.T) {
	result := ContentToText("something", "unknown")
	if result != "" {
		t.Errorf("expected empty string for unknown type, got '%s'", result)
	}
}

