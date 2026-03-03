package pipe

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing script: %v", err)
	}
	return path
}

func testConfig(name, exe, dir string, timeout time.Duration) SubprocessConfig {
	return SubprocessConfig{
		Name:       name,
		Executable: exe,
		WorkDir:    dir,
		Timeout:    timeout,
		Env:        os.Environ(),
	}
}

func testInput() envelope.Envelope {
	return envelope.Envelope{
		Pipe:        "test",
		Action:      "input",
		Content:     "hello world",
		ContentType: envelope.ContentText,
	}
}

func TestSubprocessHandler_HappyPath(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
cat <<'EOF'
{"pipe":"echo","action":"respond","args":{},"timestamp":"2024-01-01T00:00:00Z","content":"it works","content_type":"text","error":null}
EOF
`)

	h := SubprocessHandler(testConfig("echo", exe, dir, 5*time.Second))
	result := h(testInput(), map[string]string{"key": "val"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "echo" {
		t.Errorf("expected pipe=echo, got %s", result.Pipe)
	}
	s, ok := result.Content.(string)
	if !ok || s != "it works" {
		t.Errorf("expected content='it works', got %v", result.Content)
	}
}

func TestSubprocessHandler_Timeout(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
sleep 10
`)

	h := SubprocessHandler(testConfig("slow", exe, dir, 200*time.Millisecond))
	result := h(testInput(), nil)

	if result.Error == nil {
		t.Fatal("expected error for timeout")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable error for timeout")
	}
	if !strings.Contains(result.Error.Message, "timeout") {
		t.Errorf("expected timeout in message, got: %s", result.Error.Message)
	}
}

func TestSubprocessHandler_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
echo "not json at all"
`)

	h := SubprocessHandler(testConfig("bad", exe, dir, 5*time.Second))
	result := h(testInput(), nil)

	if result.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
}

func TestSubprocessHandler_NonZeroExitWithStderr(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
echo "something broke" >&2
exit 1
`)

	h := SubprocessHandler(testConfig("fail", exe, dir, 5*time.Second))
	result := h(testInput(), nil)

	if result.Error == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "something broke") {
		t.Errorf("expected stderr message, got: %s", result.Error.Message)
	}
}

func TestSubprocessHandler_NonZeroExitWithValidEnvelope(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
cat <<'EOF'
{"pipe":"fail","action":"error","args":{},"timestamp":"2024-01-01T00:00:00Z","content":null,"content_type":"","error":{"message":"handled error","severity":"error","retryable":true}}
EOF
exit 1
`)

	h := SubprocessHandler(testConfig("fail", exe, dir, 5*time.Second))
	result := h(testInput(), nil)

	if result.Error == nil {
		t.Fatal("expected error envelope")
	}
	if result.Error.Message != "handled error" {
		t.Errorf("expected 'handled error', got: %s", result.Error.Message)
	}
	if !result.Error.Retryable {
		t.Error("expected retryable from envelope")
	}
}

func TestSubprocessStreamHandler_HappyPath(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
echo '{"chunk":"First "}'
echo '{"chunk":"Second "}'
echo '{"envelope":{"pipe":"stream","action":"done","args":{},"timestamp":"2024-01-01T00:00:00Z","content":"First Second ","content_type":"text","error":null}}'
`)

	h := SubprocessStreamHandler(testConfig("stream", exe, dir, 5*time.Second))

	var chunks []string
	result := h(context.Background(), testInput(), nil, func(chunk string) {
		chunks = append(chunks, chunk)
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0] != "First " || chunks[1] != "Second " {
		t.Errorf("unexpected chunks: %v", chunks)
	}
	if result.Pipe != "stream" {
		t.Errorf("expected pipe=stream, got %s", result.Pipe)
	}
}

func TestSubprocessStreamHandler_Timeout(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "run", `#!/bin/sh
sleep 10
`)

	h := SubprocessStreamHandler(testConfig("slow", exe, dir, 200*time.Millisecond))
	result := h(context.Background(), testInput(), nil, func(string) {})

	if result.Error == nil {
		t.Fatal("expected error for timeout")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable error for timeout")
	}
}

func TestSubprocessHandler_ReadsStdin(t *testing.T) {
	dir := t.TempDir()
	// Script that echoes the pipe name from stdin back
	exe := writeScript(t, dir, "run", `#!/bin/sh
# Read stdin, extract the envelope pipe field, echo it back
INPUT=$(cat)
PIPE=$(echo "$INPUT" | grep -o '"pipe":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "{\"pipe\":\"echo\",\"action\":\"respond\",\"args\":{},\"timestamp\":\"2024-01-01T00:00:00Z\",\"content\":\"got: $PIPE\",\"content_type\":\"text\",\"error\":null}"
`)

	h := SubprocessHandler(testConfig("echo", exe, dir, 5*time.Second))
	result := h(testInput(), nil)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	s, ok := result.Content.(string)
	if !ok || s != "got: test" {
		t.Errorf("expected content='got: test', got %v", result.Content)
	}
}

func TestForwardLogs_ParsesSlogJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// slog JSONHandler produces uppercase level names
	stderr := []byte(`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"stored","pipe":"memory","count":3}
{"time":"2025-01-01T00:00:00Z","level":"DEBUG","msg":"query details","pipe":"memory"}
{"time":"2025-01-01T00:00:00Z","level":"ERROR","msg":"search failed","pipe":"memory","error":"db locked"}
`)

	plain := forwardLogs(logger, stderr, "memory")

	if plain != "" {
		t.Errorf("expected no plain text, got: %q", plain)
	}

	output := buf.String()
	if !strings.Contains(output, "stored") {
		t.Errorf("expected 'stored' in forwarded logs, got: %s", output)
	}
	if !strings.Contains(output, "query details") {
		t.Errorf("expected 'query details' in forwarded logs, got: %s", output)
	}
	if !strings.Contains(output, "search failed") {
		t.Errorf("expected 'search failed' in forwarded logs, got: %s", output)
	}
}

func TestForwardLogs_ForwardsAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	stderr := []byte(`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"fetched","count":5,"calendar":"primary"}
`)

	forwardLogs(logger, stderr, "calendar")

	output := buf.String()
	if !strings.Contains(output, "count") {
		t.Errorf("expected 'count' attr in forwarded logs, got: %s", output)
	}
	if !strings.Contains(output, "calendar") {
		t.Errorf("expected 'calendar' attr in forwarded logs, got: %s", output)
	}
}

func TestForwardLogs_SeparatesPlainStderr(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	stderr := []byte(`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"stored"}
plain error text
{"time":"2025-01-01T00:00:00Z","level":"ERROR","msg":"failed"}
another plain line
`)

	plain := forwardLogs(logger, stderr, "test")

	if !strings.Contains(plain, "plain error text") {
		t.Errorf("expected 'plain error text' in plain output, got: %q", plain)
	}
	if !strings.Contains(plain, "another plain line") {
		t.Errorf("expected 'another plain line' in plain output, got: %q", plain)
	}
	if strings.Contains(plain, "stored") {
		t.Errorf("expected 'stored' to be forwarded, not in plain output")
	}

	output := buf.String()
	if !strings.Contains(output, "stored") {
		t.Errorf("expected 'stored' in forwarded logs, got: %s", output)
	}
	if !strings.Contains(output, "failed") {
		t.Errorf("expected 'failed' in forwarded logs, got: %s", output)
	}
}

func TestForwardLogs_NilLogger(t *testing.T) {
	stderr := []byte("some error output\n")
	plain := forwardLogs(nil, stderr, "test")
	if plain != "some error output\n" {
		t.Errorf("expected raw stderr with nil logger, got: %q", plain)
	}
}

func TestForwardLogs_EmptyStderr(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	plain := forwardLogs(logger, nil, "test")
	if plain != "" {
		t.Errorf("expected empty plain for nil stderr, got: %q", plain)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no forwarded logs for nil stderr, got: %s", buf.String())
	}
}

func TestForwardLogs_LevelMapping(t *testing.T) {
	cases := []struct {
		level    string
		contains string
	}{
		{"DEBUG", "level=DEBUG"},
		{"INFO", "level=INFO"},
		{"WARN", "level=WARN"},
		{"ERROR", "level=ERROR"},
	}

	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			stderr := []byte(`{"time":"2025-01-01T00:00:00Z","level":"` + tc.level + `","msg":"test msg"}` + "\n")
			forwardLogs(logger, stderr, "test")

			output := buf.String()
			if !strings.Contains(output, tc.contains) {
				t.Errorf("expected %q in output for level %s, got: %s", tc.contains, tc.level, output)
			}
		})
	}
}
