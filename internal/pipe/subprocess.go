package pipe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

// maxOutputBytes is the maximum bytes captured from subprocess stdout/stderr.
const maxOutputBytes = 10 * 1024 * 1024 // 10 MB

// limitedBuffer wraps a bytes.Buffer with a max size limit.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	remaining := lb.max - lb.buf.Len()
	if remaining <= 0 {
		return len(p), nil // silently discard
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) Bytes() []byte  { return lb.buf.Bytes() }
func (lb *limitedBuffer) String() string { return lb.buf.String() }

// SubprocessRequest is the JSON payload sent to a pipe subprocess on stdin.
type SubprocessRequest struct {
	Envelope envelope.Envelope `json:"envelope"`
	Flags    map[string]string `json:"flags"`
	Stream   bool              `json:"stream"`
}

// SubprocessChunk is a single line of streaming output from a pipe subprocess.
type SubprocessChunk struct {
	Chunk    string             `json:"chunk,omitempty"`
	Envelope *envelope.Envelope `json:"envelope,omitempty"`
}

// SubprocessConfig holds the configuration for subprocess pipe handlers.
type SubprocessConfig struct {
	Name       string
	Executable string
	WorkDir    string
	Timeout    time.Duration
	Env        []string
	Logger     *slog.Logger
}

// buildCmd creates a configured exec.Cmd for a subprocess invocation.
func (sc SubprocessConfig) buildCmd(ctx context.Context, reqBytes []byte) *exec.Cmd {
	cmd := exec.CommandContext(ctx, sc.Executable)
	cmd.Dir = sc.WorkDir
	cmd.Stdin = bytes.NewReader(reqBytes)
	cmd.Env = sc.Env
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd
}

// marshalRequest serializes a SubprocessRequest, returning an error envelope on failure.
func (sc SubprocessConfig) marshalRequest(input envelope.Envelope, flags map[string]string, stream bool) ([]byte, *envelope.Envelope) {
	req := SubprocessRequest{
		Envelope: input,
		Flags:    flags,
		Stream:   stream,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		out := envelope.NewFatalError(sc.Name, fmt.Sprintf("marshaling request: %v", err))
		return nil, &out
	}
	return reqBytes, nil
}

// forwardLogs parses stderr lines for structured log messages and forwards them
// to the logger. Non-JSON lines are collected and returned as plain stderr text.
func forwardLogs(logger *slog.Logger, stderr []byte, pipeName string) string {
	if logger == nil || len(stderr) == 0 {
		return string(stderr)
	}

	var plainLines []byte
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			plainLines = append(plainLines, line...)
			plainLines = append(plainLines, '\n')
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err == nil {
			msg, _ := raw["msg"].(string)
			if msg == "" {
				plainLines = append(plainLines, line...)
				plainLines = append(plainLines, '\n')
				continue
			}
			var lvl slog.Level
			if levelStr, _ := raw["level"].(string); levelStr != "" {
				if err := lvl.UnmarshalText([]byte(levelStr)); err != nil {
					lvl = slog.LevelInfo
				}
			}
			attrs := []any{"pipe", pipeName}
			for k, v := range raw {
				if k == "time" || k == "level" || k == "msg" {
					continue
				}
				attrs = append(attrs, k, v)
			}
			logger.Log(context.Background(), lvl, msg, attrs...)
		} else {
			plainLines = append(plainLines, line...)
			plainLines = append(plainLines, '\n')
		}
	}
	return string(plainLines)
}

// SubprocessHandler returns a Handler that invokes the given executable as a
// subprocess, sending a SubprocessRequest on stdin and reading a single
// envelope from stdout.
func SubprocessHandler(cfg SubprocessConfig) Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		reqBytes, errEnv := cfg.marshalRequest(input, flags, false)
		if errEnv != nil {
			return *errEnv
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		cmd := cfg.buildCmd(ctx, reqBytes)

		stdout := &limitedBuffer{max: maxOutputBytes}
		stderr := &limitedBuffer{max: maxOutputBytes}
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		runErr := cmd.Run()

		// Forward structured log messages from stderr
		plainStderr := forwardLogs(cfg.Logger, stderr.Bytes(), cfg.Name)

		// Timeout → retryable error
		if ctx.Err() == context.DeadlineExceeded {
			return envelope.NewRetryableError(cfg.Name, fmt.Sprintf("timeout after %s", cfg.Timeout))
		}

		// Try to parse stdout as envelope
		var out envelope.Envelope
		jsonErr := json.Unmarshal(stdout.Bytes(), &out)

		if runErr != nil {
			// Non-zero exit with valid JSON → use the envelope
			if jsonErr == nil {
				return out
			}
			// Non-zero exit, no valid JSON → fatal from stderr
			msg := plainStderr
			if msg == "" {
				msg = runErr.Error()
			}
			return envelope.NewFatalError(cfg.Name, msg)
		}

		// Exit 0 + invalid JSON → fatal
		if jsonErr != nil {
			return envelope.NewFatalError(cfg.Name, fmt.Sprintf("invalid JSON from subprocess: %v", jsonErr))
		}

		return out
	}
}

// SubprocessStreamHandler returns a StreamHandler that invokes the given
// executable as a subprocess with stream: true, reading chunk lines and a
// final envelope line from stdout.
func SubprocessStreamHandler(cfg SubprocessConfig) StreamHandler {
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		reqBytes, errEnv := cfg.marshalRequest(input, flags, true)
		if errEnv != nil {
			return *errEnv
		}

		ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()

		cmd := cfg.buildCmd(ctx, reqBytes)

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return envelope.NewFatalError(cfg.Name, fmt.Sprintf("creating stdout pipe: %v", err))
		}

		stderr := &limitedBuffer{max: maxOutputBytes}
		cmd.Stderr = stderr

		if err := cmd.Start(); err != nil {
			return envelope.NewFatalError(cfg.Name, fmt.Sprintf("starting subprocess: %v", err))
		}

		var result *envelope.Envelope
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 4096), 1024*1024) // 1 MB max line for large envelopes
		for scanner.Scan() {
			line := scanner.Bytes()
			var chunk SubprocessChunk
			if err := json.Unmarshal(line, &chunk); err != nil {
				continue
			}
			if chunk.Envelope != nil {
				result = chunk.Envelope
			} else if chunk.Chunk != "" {
				sink(chunk.Chunk)
			}
		}

		waitErr := cmd.Wait()

		// Forward structured log messages from stderr
		plainStderr := forwardLogs(cfg.Logger, stderr.Bytes(), cfg.Name)

		if ctx.Err() == context.DeadlineExceeded {
			return envelope.NewRetryableError(cfg.Name, fmt.Sprintf("timeout after %s", cfg.Timeout))
		}

		if result != nil {
			return *result
		}

		if waitErr != nil {
			msg := plainStderr
			if msg == "" {
				msg = waitErr.Error()
			}
			return envelope.NewFatalError(cfg.Name, msg)
		}

		return envelope.NewFatalError(cfg.Name, "subprocess produced no envelope")
	}
}

