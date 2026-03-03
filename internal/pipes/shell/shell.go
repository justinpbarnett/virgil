package shell

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// Executor is an alias for pipeutil.Executor.
type Executor = pipeutil.Executor

// OSExecutor is an alias for pipeutil.OSExecutor.
type OSExecutor = pipeutil.OSExecutor

// NewHandler returns a pipe.Handler that executes shell commands with sandboxing.
func NewHandler(executor Executor, allowlist []string, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	allowed := make(map[string]bool, len(allowlist))
	for _, cmd := range allowlist {
		allowed[cmd] = true
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("shell", "exec")
		out.Args = flags

		cmd := flags["cmd"]
		if cmd == "" {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError("missing required flag: cmd")
			return out
		}

		basename := filepath.Base(strings.Fields(cmd)[0])
		if !allowed[basename] {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("command not allowed: %s", basename))
			return out
		}

		timeoutStr := flags["timeout"]
		timeout := 30 * time.Second
		if timeoutStr != "" {
			if d, err := time.ParseDuration(timeoutStr); err == nil && d > 0 {
				timeout = d
			}
		}

		cwd := flags["cwd"]
		if cwd != "" {
			info, err := os.Stat(cwd)
			if err != nil || !info.IsDir() {
				out.Duration = time.Since(out.Timestamp)
				out.Error = envelope.FatalError(fmt.Sprintf("invalid working directory: %s", cwd))
				return out
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		logger.Debug("executing", "cmd", cmd, "cwd", cwd, "timeout", timeout)
		stdout, stderr, exitCode, err := executor.Execute(ctx, cmd, cwd)

		content := map[string]any{
			"stdout":    stdout,
			"stderr":    stderr,
			"exit_code": exitCode,
		}
		out.Content = content
		out.ContentType = envelope.ContentStructured

		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = context.DeadlineExceeded
			}
			out.Error = envelope.ClassifyError("shell", err)
		} else if exitCode != 0 {
			out.Error = &envelope.EnvelopeError{
				Message:  fmt.Sprintf("command exited with code %d", exitCode),
				Severity: envelope.SeverityWarn,
			}
		}

		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
