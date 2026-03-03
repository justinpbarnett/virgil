package shell

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

type mockExecutor struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
	lastCtx  context.Context
}

func (m *mockExecutor) Execute(ctx context.Context, cmd string, cwd string) (string, string, int, error) {
	m.lastCtx = ctx
	return m.stdout, m.stderr, m.exitCode, m.err
}

var defaultAllowlist = []string{"go", "git", "make", "ls", "echo"}

func TestShellHappyPath(t *testing.T) {
	executor := &mockExecutor{
		stdout:   "go version go1.25 linux/amd64",
		exitCode: 0,
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go version"})

	testutil.AssertEnvelope(t, result, "shell", "exec")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	content, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result.Content)
	}
	if content["stdout"] != "go version go1.25 linux/amd64" {
		t.Errorf("unexpected stdout: %v", content["stdout"])
	}
	if content["exit_code"] != 0 {
		t.Errorf("unexpected exit_code: %v", content["exit_code"])
	}
}

func TestShellNonZeroExit(t *testing.T) {
	executor := &mockExecutor{
		stdout:   "some output",
		stderr:   "error details",
		exitCode: 1,
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go test"})

	if result.Error == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
	if result.Error.Retryable {
		t.Error("expected retryable=false")
	}

	content := result.Content.(map[string]any)
	if content["stdout"] != "some output" {
		t.Errorf("expected stdout preserved, got %v", content["stdout"])
	}
	if content["stderr"] != "error details" {
		t.Errorf("expected stderr preserved, got %v", content["stderr"])
	}
	if content["exit_code"] != 1 {
		t.Errorf("expected exit_code=1, got %v", content["exit_code"])
	}
}

func TestShellMissingCmd(t *testing.T) {
	executor := &mockExecutor{}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
	if result.Error.Message != "missing required flag: cmd" {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}
}

func TestShellCommandNotAllowed(t *testing.T) {
	executor := &mockExecutor{}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "rm -rf /"})

	testutil.AssertFatalError(t, result)
	if result.Error.Message != "command not allowed: rm" {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}
}

func TestShellTimeout(t *testing.T) {
	executor := &mockExecutor{
		err: context.DeadlineExceeded,
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go test", "timeout": "1ms"})

	if result.Error == nil {
		t.Fatal("expected error for timeout")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true for timeout")
	}
}

func TestShellExecutionError(t *testing.T) {
	executor := &mockExecutor{
		err: fmt.Errorf("command not found"),
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go build"})

	testutil.AssertFatalError(t, result)
}

func TestShellInvalidCwd(t *testing.T) {
	executor := &mockExecutor{}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go version", "cwd": "/nonexistent"})

	testutil.AssertFatalError(t, result)
	if result.Error.Message != "invalid working directory: /nonexistent" {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}
}

func TestShellCustomTimeout(t *testing.T) {
	executor := &mockExecutor{exitCode: 0}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go version", "timeout": "5s"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	deadline, ok := executor.lastCtx.Deadline()
	if !ok {
		t.Fatal("expected context to have deadline")
	}
	if deadline.IsZero() {
		t.Error("expected non-zero deadline")
	}
}

func TestShellPathPrefixStripping(t *testing.T) {
	executor := &mockExecutor{
		stdout:   "go version go1.25",
		exitCode: 0,
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "/usr/bin/go version"})

	if result.Error != nil {
		t.Fatalf("expected allowed, got error: %v", result.Error)
	}
}

func TestShellEnvelopeCompliance(t *testing.T) {
	executor := &mockExecutor{
		stdout:   "ok",
		exitCode: 0,
	}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "echo hello"})

	testutil.AssertEnvelope(t, result, "shell", "exec")
	if result.Args == nil {
		t.Error("expected args to be non-nil")
	}
	if result.Content == nil {
		t.Error("expected content to be non-nil")
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}

func TestShellCwdIsFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "shell-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	executor := &mockExecutor{}
	handler := NewHandler(executor, defaultAllowlist, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cmd": "go version", "cwd": tmp.Name()})

	testutil.AssertFatalError(t, result)
}
