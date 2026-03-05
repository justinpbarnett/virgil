package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/bridge"
)

func executeTool(t *testing.T, tools []bridge.Tool, name string, args map[string]any) (string, error) {
	t.Helper()
	input, _ := json.Marshal(args)
	for _, tool := range tools {
		if tool.Name == name {
			return tool.Execute(context.Background(), input)
		}
	}
	t.Fatalf("tool %q not found", name)
	return "", nil
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := executeTool(t, BuildTools(dir), "read_file", map[string]any{"path": "hello.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestReadFileOutsideWorktree(t *testing.T) {
	dir := t.TempDir()

	_, err := executeTool(t, BuildTools(dir), "read_file", map[string]any{"path": "../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("expected 'outside' in error, got: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()

	_, err := executeTool(t, BuildTools(dir), "write_file", map[string]any{"path": "sub/new.txt", "content": "content here"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sub/new.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "content here" {
		t.Errorf("expected 'content here', got %q", string(data))
	}
}

func TestWriteFileOutsideWorktree(t *testing.T) {
	dir := t.TempDir()

	_, err := executeTool(t, BuildTools(dir), "write_file", map[string]any{"path": "../../tmp/evil.txt", "content": "evil"})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("var x = 1\nvar y = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := executeTool(t, BuildTools(dir), "edit_file", map[string]any{"path": "code.go", "old_str": "var x = 1", "new_str": "var x = 42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	if !strings.Contains(string(data), "var x = 42") {
		t.Errorf("edit not applied, got: %s", data)
	}
}

func TestEditFileStringNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("var x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := executeTool(t, BuildTools(dir), "edit_file", map[string]any{"path": "code.go", "old_str": "not present", "new_str": "replacement"})
	if err == nil {
		t.Fatal("expected error for missing old_str")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestRunShell(t *testing.T) {
	dir := t.TempDir()

	result, err := executeTool(t, BuildTools(dir), "run_shell", map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", result)
	}
	if !strings.HasPrefix(result, "exit 0\n") {
		t.Errorf("expected 'exit 0' prefix, got: %q", result)
	}
}

func TestRunShellOutputCapped(t *testing.T) {
	dir := t.TempDir()

	// Generate > 64KB of output.
	result, err := executeTool(t, BuildTools(dir), "run_shell", map[string]any{
		"command": "dd if=/dev/zero bs=1024 count=128 2>/dev/null | tr '\\0' 'a'",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Account for the "exit N\n" prefix line added by the tool.
	maxExpected := maxShellOutput + len("exit 0\n")
	if len(result) > maxExpected {
		t.Errorf("output not capped: got %d bytes, expected <= %d", len(result), maxExpected)
	}
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)

	result, err := executeTool(t, BuildTools(dir), "list_dir", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.go") {
		t.Errorf("expected 'a.go' in listing, got: %q", result)
	}
	if !strings.Contains(result, "sub/") {
		t.Errorf("expected 'sub/' in listing, got: %q", result)
	}
}
