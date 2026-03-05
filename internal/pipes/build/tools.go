package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/justinpbarnett/virgil/internal/bridge"
)

const maxShellOutput = 64 * 1024  // 64 KB
const maxReadFileSize = 1024 * 1024 // 1 MB

// limitWriter wraps an io.Writer and stops accepting bytes after n bytes total.
type limitWriter struct {
	w io.Writer
	n int
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n <= 0 {
		return len(p), nil // silently discard
	}
	if len(p) > lw.n {
		p = p[:lw.n]
	}
	n, err := lw.w.Write(p)
	lw.n -= n
	return n, err
}

// BuildTools returns the set of tools available to the build pipe.
// All file operations are sandboxed to worktreePath.
func BuildTools(worktreePath string) []bridge.Tool {
	// Resolve worktree path once to avoid repeated EvalSymlinks calls.
	resolved, err := filepath.EvalSymlinks(filepath.Clean(worktreePath))
	if err != nil {
		resolved = filepath.Clean(worktreePath)
	}
	return []bridge.Tool{
		readFileTool(resolved),
		writeFileTool(resolved),
		editFileTool(resolved),
		runShellTool(resolved),
		listDirTool(resolved),
	}
}

// sandboxPath resolves path relative to worktreePath and verifies it stays inside.
// worktreePath must already be resolved (no symlinks).
func sandboxPath(worktreePath, path string) (string, error) {
	abs := path
	if !filepath.IsAbs(path) {
		abs = filepath.Join(worktreePath, path)
	}
	clean, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		// File may not exist yet (write_file). Clean without symlink resolution.
		clean = filepath.Clean(abs)
	}
	if !strings.HasPrefix(clean+string(filepath.Separator), worktreePath+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the working directory", path)
	}
	return clean, nil
}

func readFileTool(worktreePath string) bridge.Tool {
	return bridge.Tool{
		Name:        "read_file",
		Description: "Read the contents of a file. Path must be relative to the working directory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the working directory"}
			},
			"required": ["path"]
		}`),
		Execute: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			safe, err := sandboxPath(worktreePath, args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(safe)
			if err != nil {
				return "", fmt.Errorf("reading file: %w", err)
			}
			if info.Size() > maxReadFileSize {
				return "", fmt.Errorf("file %s is too large (%d bytes, max %d)", args.Path, info.Size(), maxReadFileSize)
			}
			data, err := os.ReadFile(safe)
			if err != nil {
				return "", fmt.Errorf("reading file: %w", err)
			}
			return string(data), nil
		},
	}
}

func writeFileTool(worktreePath string) bridge.Tool {
	return bridge.Tool{
		Name:        "write_file",
		Description: "Create or overwrite a file. Creates parent directories as needed. Path must be relative to the working directory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path":    {"type": "string", "description": "File path relative to the working directory"},
				"content": {"type": "string", "description": "File content to write"}
			},
			"required": ["path", "content"]
		}`),
		Execute: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			safe, err := sandboxPath(worktreePath, args.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(safe), 0o755); err != nil {
				return "", fmt.Errorf("creating directories: %w", err)
			}
			if err := os.WriteFile(safe, []byte(args.Content), 0o644); err != nil {
				return "", fmt.Errorf("writing file: %w", err)
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), nil
		},
	}
}

func editFileTool(worktreePath string) bridge.Tool {
	return bridge.Tool{
		Name:        "edit_file",
		Description: "Replace the first occurrence of old_str with new_str in a file. Prefer this over write_file for targeted changes.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path":    {"type": "string", "description": "File path relative to the working directory"},
				"old_str": {"type": "string", "description": "String to replace (must exist exactly once)"},
				"new_str": {"type": "string", "description": "Replacement string"}
			},
			"required": ["path", "old_str", "new_str"]
		}`),
		Execute: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Path   string `json:"path"`
				OldStr string `json:"old_str"`
				NewStr string `json:"new_str"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			safe, err := sandboxPath(worktreePath, args.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(safe)
			if err != nil {
				return "", fmt.Errorf("reading file: %w", err)
			}
			content := string(data)
			count := strings.Count(content, args.OldStr)
			if count == 0 {
				return "", fmt.Errorf("old_str not found in %s", args.Path)
			}
			if count > 1 {
				return "", fmt.Errorf("old_str appears %d times in %s (must be unique)", count, args.Path)
			}
			updated := strings.Replace(content, args.OldStr, args.NewStr, 1)
			if err := os.WriteFile(safe, []byte(updated), 0o644); err != nil {
				return "", fmt.Errorf("writing file: %w", err)
			}
			return fmt.Sprintf("edited %s", args.Path), nil
		},
	}
}

func runShellTool(worktreePath string) bridge.Tool {
	return bridge.Tool{
		Name:        "run_shell",
		Description: "Execute a shell command in the working directory. Returns combined stdout+stderr, capped at 64 KB.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "Shell command to execute"}
			},
			"required": ["command"]
		}`),
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			cmd := exec.CommandContext(ctx, "sh", "-c", args.Command)
			cmd.Dir = worktreePath
			var buf bytes.Buffer
			lw := &limitWriter{w: &buf, n: maxShellOutput}
			cmd.Stdout = lw
			cmd.Stderr = lw
			err := cmd.Run()
			exitCode := 0
			if err != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			return fmt.Sprintf("exit %d\n%s", exitCode, buf.String()), nil
		},
	}
}

func listDirTool(worktreePath string) bridge.Tool {
	return bridge.Tool{
		Name:        "list_dir",
		Description: "List the contents of a directory. Path must be relative to the working directory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path relative to the working directory (use '.' for root)"}
			},
			"required": ["path"]
		}`),
		Execute: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			safe, err := sandboxPath(worktreePath, args.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(safe)
			if err != nil {
				return "", fmt.Errorf("reading directory: %w", err)
			}
			var sb strings.Builder
			for _, e := range entries {
				if e.IsDir() {
					sb.WriteString(e.Name() + "/\n")
				} else {
					sb.WriteString(e.Name() + "\n")
				}
			}
			return sb.String(), nil
		},
	}
}
