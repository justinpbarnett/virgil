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

// StripMarkdownFences removes ```json or ``` fences from AI model output.
func StripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

// FlagOrDefault returns the flag value if present, otherwise the default.
func FlagOrDefault(flags map[string]string, key, defaultVal string) string {
	if v, ok := flags[key]; ok && v != "" {
		return v
	}
	return defaultVal
}
