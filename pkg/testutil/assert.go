// Package testutil provides test helpers for pipe implementations.
package testutil

import (
	"testing"

	"github.com/justinpbarnett/virgil/pkg/envelope"
)

// AssertEnvelope checks that the universal envelope fields (Pipe, Action,
// Timestamp, Duration) are set correctly.
func AssertEnvelope(t *testing.T, env envelope.Envelope, pipe, action string) {
	t.Helper()
	if env.Pipe != pipe {
		t.Errorf("expected pipe=%s, got %s", pipe, env.Pipe)
	}
	if env.Action != action {
		t.Errorf("expected action=%s, got %s", action, env.Action)
	}
	if env.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if env.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

// AssertFatalError checks that the envelope carries a fatal error.
func AssertFatalError(t *testing.T, env envelope.Envelope) {
	t.Helper()
	if env.Error == nil {
		t.Fatal("expected error")
	}
	if env.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", env.Error.Severity)
	}
}
