package runtime

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

func newTestLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level}))
}

func TestLogObserverSilent(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelDebug)
	obs := NewLogObserver(logger, config.Silent)

	env := envelope.New("test", "run")
	obs.OnTransition("test", env, 100*time.Millisecond)

	if buf.Len() != 0 {
		t.Errorf("expected no output at silent level, got: %s", buf.String())
	}
}

func TestLogObserverSilentWithError(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelDebug)
	obs := NewLogObserver(logger, config.Silent)

	env := envelope.New("test", "run")
	env.Error = &envelope.EnvelopeError{Message: "something broke", Severity: "fatal"}
	obs.OnTransition("test", env, 100*time.Millisecond)

	if buf.Len() != 0 {
		t.Errorf("expected no output at silent level even with errors, got: %s", buf.String())
	}
}

func TestLogObserverErrorLevelSkipsSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelDebug)
	obs := NewLogObserver(logger, config.Error)

	env := envelope.New("test", "run")
	obs.OnTransition("test", env, 100*time.Millisecond)

	if buf.Len() != 0 {
		t.Errorf("expected no output for success at error level, got: %s", buf.String())
	}
}

func TestLogObserverErrorLevelLogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelDebug)
	obs := NewLogObserver(logger, config.Error)

	env := envelope.New("test", "run")
	env.Error = &envelope.EnvelopeError{Message: "something broke", Severity: "fatal"}
	obs.OnTransition("test", env, 100*time.Millisecond)

	output := buf.String()
	if !strings.Contains(output, "pipe error") {
		t.Errorf("expected 'pipe error' in output, got: %s", output)
	}
	if !strings.Contains(output, "something broke") {
		t.Errorf("expected error message in output, got: %s", output)
	}
}

func TestLogObserverInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)
	obs := NewLogObserver(logger, config.Info)

	env := envelope.New("test", "run")
	obs.OnTransition("test", env, 100*time.Millisecond)

	output := buf.String()
	if !strings.Contains(output, "pipe ok") {
		t.Errorf("expected 'pipe ok' in output, got: %s", output)
	}
}

func TestLogObserverDebugIncludesEnvelope(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelDebug)
	obs := NewLogObserver(logger, config.Debug)

	env := envelope.New("test", "run")
	env.Content = "hello"
	obs.OnTransition("test", env, 100*time.Millisecond)

	output := buf.String()
	if !strings.Contains(output, "pipe ok") {
		t.Errorf("expected 'pipe ok' in output, got: %s", output)
	}
	if !strings.Contains(output, "envelope") {
		t.Errorf("expected 'envelope' debug log in output, got: %s", output)
	}
}
