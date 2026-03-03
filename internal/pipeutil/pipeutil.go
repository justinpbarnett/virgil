package pipeutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"text/template"

	"github.com/justinpbarnett/virgil/internal/config"
)

// Executor abstracts shell command execution for testability.
type Executor interface {
	Execute(ctx context.Context, cmd string, cwd string) (stdout string, stderr string, exitCode int, err error)
}

// OSExecutor implements Executor using os/exec.
type OSExecutor struct{}

func (e *OSExecutor) Execute(ctx context.Context, cmd string, cwd string) (string, string, int, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	if cwd != "" {
		c.Dir = cwd
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	c.Stdout = &stdoutBuf
	c.Stderr = &stderrBuf

	err := c.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdoutBuf.String(), stderrBuf.String(), exitErr.ExitCode(), nil
		}
		return stdoutBuf.String(), stderrBuf.String(), -1, err
	}

	return stdoutBuf.String(), stderrBuf.String(), 0, nil
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
func CompileTemplates(pipeConfig config.PipeConfig) map[string]*template.Template {
	compiled := make(map[string]*template.Template)
	for name, tmplStr := range pipeConfig.Prompts.Templates {
		t, err := template.New(name).Parse(tmplStr)
		if err == nil {
			compiled[name] = t
		}
	}
	return compiled
}

// ExecuteTemplate looks up a compiled template by key, executes it with data,
// and returns the rendered string.
func ExecuteTemplate(compiled map[string]*template.Template, key string, data any) (string, error) {
	tmpl, ok := compiled[key]
	if !ok {
		return "", fmt.Errorf("no template for key: %s", key)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// StripMarkdownFences removes ``` fences from AI model output, including any
// language specifier on the opening fence line (e.g. ```json, ```python).
func StripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Strip opening fence line (includes language tag like ```python or ```json)
	newline := strings.Index(s, "\n")
	if newline != -1 {
		s = s[newline+1:]
	} else {
		// Single-line fence with no content (e.g. "```json") — nothing to keep.
		return ""
	}
	// Strip closing fence
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// FlagOrDefault returns the flag value if present, otherwise the default.
func FlagOrDefault(flags map[string]string, key, defaultVal string) string {
	if v, ok := flags[key]; ok && v != "" {
		return v
	}
	return defaultVal
}
