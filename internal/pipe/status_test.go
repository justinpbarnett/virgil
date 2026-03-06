package pipe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// runReadStderr is a helper that feeds bytes into readStderr synchronously and
// returns the collected plain text and any status events forwarded to the sink.
func runReadStderr(t *testing.T, input []byte, logger *slog.Logger, pipeName string) (plain string, events []StatusEvent) {
	t.Helper()
	var captured []StatusEvent
	sink := func(e StatusEvent) { captured = append(captured, e) }
	done := make(chan stderrResult, 1)
	readStderr(bytes.NewReader(input), logger, pipeName, sink, done)
	res := <-done
	return res.plain, captured
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

// TestReadStderrStatusEvent verifies that a line with a "status" key is
// parsed as a StatusEvent and forwarded to the sink, not to plain text.
func TestReadStderrStatusEvent(t *testing.T) {
	input := []byte(`{"status":"progress","pipe":"build","message":"writing tests","ts":1700000000}` + "\n")
	plain, events := runReadStderr(t, input, silentLogger(), "build")

	if plain != "" {
		t.Errorf("expected no plain text, got: %q", plain)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 status event, got %d", len(events))
	}
	e := events[0]
	if e.Status != StatusProgress {
		t.Errorf("expected status=%q, got %q", StatusProgress, e.Status)
	}
	if e.Pipe != "build" {
		t.Errorf("expected pipe=%q, got %q", "build", e.Pipe)
	}
	if e.Message != "writing tests" {
		t.Errorf("expected message=%q, got %q", "writing tests", e.Message)
	}
	if e.TS != 1700000000 {
		t.Errorf("expected ts=1700000000, got %d", e.TS)
	}
}

// TestReadStderrSlogMessage verifies that a line with a "msg" key but no
// "status" key is forwarded to the logger, not the status sink.
func TestReadStderrSlogMessage(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	input := []byte(`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"stored","pipe":"memory","count":3}` + "\n")
	plain, events := runReadStderr(t, input, logger, "memory")

	if plain != "" {
		t.Errorf("expected no plain text, got: %q", plain)
	}
	if len(events) != 0 {
		t.Errorf("expected no status events, got %d", len(events))
	}
	if !strings.Contains(logBuf.String(), "stored") {
		t.Errorf("expected 'stored' in forwarded log output, got: %s", logBuf.String())
	}
}

// TestReadStderrPlainText verifies that non-JSON lines are collected as plain
// text and not forwarded to either the logger or the status sink.
func TestReadStderrPlainText(t *testing.T) {
	input := []byte("something went wrong\nanother plain line\n")
	plain, events := runReadStderr(t, input, silentLogger(), "test")

	if !strings.Contains(plain, "something went wrong") {
		t.Errorf("expected plain text to contain 'something went wrong', got: %q", plain)
	}
	if !strings.Contains(plain, "another plain line") {
		t.Errorf("expected plain text to contain 'another plain line', got: %q", plain)
	}
	if len(events) != 0 {
		t.Errorf("expected no status events, got %d", len(events))
	}
}

// TestReadStderrMixed verifies that all three line types in a single input are
// correctly discriminated.
func TestReadStderrMixed(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	input := []byte(
		`{"status":"waiting","pipe":"build","message":"waiting for anthropic-api","ts":1700000001}` + "\n" +
			`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"tool called","pipe":"build"}` + "\n" +
			"plain debug output\n" +
			`{"status":"progress","pipe":"build","message":"writing file","ts":1700000002}` + "\n",
	)

	plain, events := runReadStderr(t, input, logger, "build")

	if len(events) != 2 {
		t.Fatalf("expected 2 status events, got %d", len(events))
	}
	if events[0].Status != StatusWaiting {
		t.Errorf("expected first event status=%q, got %q", StatusWaiting, events[0].Status)
	}
	if events[1].Status != StatusProgress {
		t.Errorf("expected second event status=%q, got %q", StatusProgress, events[1].Status)
	}
	if !strings.Contains(plain, "plain debug output") {
		t.Errorf("expected plain text to contain 'plain debug output', got: %q", plain)
	}
	if !strings.Contains(logBuf.String(), "tool called") {
		t.Errorf("expected 'tool called' in forwarded logs, got: %s", logBuf.String())
	}
}

// TestReadStderrMalformedJSON verifies that a line starting with '{' but
// containing invalid JSON is treated as plain text, not as a status event.
func TestReadStderrMalformedJSON(t *testing.T) {
	input := []byte("{this is not valid json\n")
	plain, events := runReadStderr(t, input, silentLogger(), "test")

	if !strings.Contains(plain, "{this is not valid json") {
		t.Errorf("expected malformed JSON in plain text, got: %q", plain)
	}
	if len(events) != 0 {
		t.Errorf("expected no status events for malformed JSON, got %d", len(events))
	}
}

// TestReadStderrNilSink verifies that status events are silently discarded
// when the sink is nil — no panic, no plain text leakage.
func TestReadStderrNilSink(t *testing.T) {
	input := []byte(`{"status":"progress","pipe":"build","message":"step 1","ts":1700000000}` + "\n")
	done := make(chan stderrResult, 1)
	readStderr(bytes.NewReader(input), silentLogger(), "build", nil, done)
	res := <-done
	if res.plain != "" {
		t.Errorf("expected no plain text with nil sink, got: %q", res.plain)
	}
}

// TestReadStderrEmpty verifies that empty input produces no events and empty
// plain text.
func TestReadStderrEmpty(t *testing.T) {
	plain, events := runReadStderr(t, []byte{}, silentLogger(), "test")
	if plain != "" {
		t.Errorf("expected empty plain text for empty input, got: %q", plain)
	}
	if len(events) != 0 {
		t.Errorf("expected no events for empty input, got %d", len(events))
	}
}

// TestReadStderrStatusEventWithDetail verifies that the optional "detail"
// field is parsed correctly.
func TestReadStderrStatusEventWithDetail(t *testing.T) {
	input := []byte(`{"status":"error","pipe":"build","message":"rate limited","ts":1700000000,"detail":{"error":"429 Too Many Requests"}}` + "\n")
	_, events := runReadStderr(t, input, silentLogger(), "build")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Status != StatusError {
		t.Errorf("expected status=%q, got %q", StatusError, e.Status)
	}
	if e.Detail == nil {
		t.Fatal("expected non-nil detail")
	}
	errMsg, _ := e.Detail["error"].(string)
	if !strings.Contains(errMsg, "429") {
		t.Errorf("expected detail.error to contain '429', got: %q", errMsg)
	}
}

// TestReadStderrTaskID verifies that the task_id field is preserved in parsed
// events.
func TestReadStderrTaskID(t *testing.T) {
	input := []byte(`{"status":"progress","pipe":"build","task_id":"task-42","message":"step","ts":1700000000}` + "\n")
	_, events := runReadStderr(t, input, silentLogger(), "build")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].TaskID != "task-42" {
		t.Errorf("expected task_id=%q, got %q", "task-42", events[0].TaskID)
	}
}
