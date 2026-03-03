package runtime

import (
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func TestCompileFormats(t *testing.T) {
	raw := map[string]map[string]string{
		"calendar": {
			"list": `{{.Count}} events`,
		},
		"memory": {
			"list": `{{.Count}} entries`,
		},
	}

	result, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 pipes, got %d", len(result))
	}
	if _, ok := result["calendar"]["list"]; !ok {
		t.Error("expected calendar/list template")
	}
	if _, ok := result["memory"]["list"]; !ok {
		t.Error("expected memory/list template")
	}
}

func TestCompileFormatsInvalid(t *testing.T) {
	raw := map[string]map[string]string{
		"broken": {
			"list": `{{.Unclosed`,
		},
	}

	_, err := compileFormats(raw)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("expected error to mention pipe name, got: %s", err.Error())
	}
}

func TestFormatTerminalList(t *testing.T) {
	raw := map[string]map[string]string{
		"calendar": {
			"list": `You have {{.Count}} event{{if gt .Count 1}}s{{end}}:{{range .Items}}
- {{.title}} at {{.start}}{{end}}`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("calendar", "list")
	env.ContentType = envelope.ContentList
	env.Content = []map[string]any{
		{"title": "Standup", "start": "10:00 AM"},
		{"title": "Lunch", "start": "12:00 PM"},
	}

	result := formatTerminal(env, "calendar", formats)

	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(s, "2 events") {
		t.Errorf("expected '2 events' in output, got: %s", s)
	}
	if !strings.Contains(s, "Standup at 10:00 AM") {
		t.Errorf("expected 'Standup at 10:00 AM' in output, got: %s", s)
	}
}

func TestFormatTerminalText(t *testing.T) {
	raw := map[string]map[string]string{
		"chat": {
			"list": `should not be used`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("chat", "respond")
	env.ContentType = envelope.ContentText
	env.Content = "hello world"

	result := formatTerminal(env, "chat", formats)

	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Content != "hello world" {
		t.Errorf("expected content unchanged, got %v", result.Content)
	}
}

func TestFormatTerminalNoTemplate(t *testing.T) {
	raw := map[string]map[string]string{
		"calendar": {
			"list": `{{.Count}} events`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("calendar", "detail")
	env.ContentType = envelope.ContentStructured
	env.Content = map[string]any{"title": "Meeting"}

	result := formatTerminal(env, "calendar", formats)

	// No template for "structured" → unchanged
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestFormatTerminalEmptyList(t *testing.T) {
	raw := map[string]map[string]string{
		"calendar": {
			"list": `{{if eq .Count 0}}Your calendar is clear.{{else}}Events:{{range .Items}}
- {{.title}}{{end}}{{end}}`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("calendar", "list")
	env.ContentType = envelope.ContentList
	env.Content = []any{}

	result := formatTerminal(env, "calendar", formats)

	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(s, "calendar is clear") {
		t.Errorf("expected 'calendar is clear' in output, got: %s", s)
	}
}

func TestFormatTerminalUnknownPipe(t *testing.T) {
	raw := map[string]map[string]string{
		"calendar": {
			"list": `{{.Count}} events`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("unknown", "list")
	env.ContentType = envelope.ContentList
	env.Content = []any{}

	result := formatTerminal(env, "unknown", formats)

	// Unknown pipe → unchanged
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list (unchanged), got %s", result.ContentType)
	}
}

func TestFormatTerminalWithStructContent(t *testing.T) {
	// Test that in-process Go structs (not JSON-decoded) also work
	type Event struct {
		Title string `json:"title"`
		Start string `json:"start"`
	}

	raw := map[string]map[string]string{
		"calendar": {
			"list": `{{.Count}} event{{if gt .Count 1}}s{{end}}:{{range .Items}}
- {{.title}}{{end}}`,
		},
	}
	formats, err := compileFormats(raw)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	env := envelope.New("calendar", "list")
	env.ContentType = envelope.ContentList
	env.Content = []Event{
		{Title: "Standup", Start: "10:00"},
		{Title: "Review", Start: "14:00"},
	}

	result := formatTerminal(env, "calendar", formats)

	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	s := result.Content.(string)
	if !strings.Contains(s, "2 events") {
		t.Errorf("expected '2 events', got: %s", s)
	}
	if !strings.Contains(s, "Standup") {
		t.Errorf("expected 'Standup', got: %s", s)
	}
}

func TestFormatTerminalNilFormats(t *testing.T) {
	env := envelope.New("calendar", "list")
	env.ContentType = envelope.ContentList
	env.Content = []any{}

	result := formatTerminal(env, "calendar", nil)

	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list (unchanged), got %s", result.ContentType)
	}
}
