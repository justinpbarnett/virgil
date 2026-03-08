// Package pipehost provides the subprocess harness for pipe binaries.
//
// Pipe executables call Run() in their main function to handle the
// JSON stdin/stdout protocol with the parent Virgil server process.
package pipehost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	pkgenv "github.com/justinpbarnett/virgil/pkg/envelope"
	pkgpipe "github.com/justinpbarnett/virgil/pkg/pipe"
	"github.com/justinpbarnett/virgil/pkg/protocol"
)

// Environment variable names shared between the main binary (writer) and
// pipe subprocesses (reader).
const (
	EnvDBPath         = "VIRGIL_DB_PATH"
	EnvConfigDir      = "VIRGIL_CONFIG_DIR"
	EnvUserDir        = "VIRGIL_USER_DIR"
	EnvProvider       = "VIRGIL_PROVIDER"
	EnvModel          = "VIRGIL_MODEL"
	EnvProviderBinary = "VIRGIL_PROVIDER_BINARY"
	EnvLogLevel       = "VIRGIL_LOG_LEVEL"
	EnvMaxTurns       = "VIRGIL_MAX_TURNS"
	EnvMaxTokens      = "VIRGIL_MAX_TOKENS"
	EnvIdentity       = "VIRGIL_IDENTITY"
	EnvWorkDir        = "VIRGIL_WORK_DIR"
	EnvPersistent     = "VIRGIL_PERSISTENT"
)

// NewPipeLogger creates an slog.Logger for a pipe subprocess.
// It reads VIRGIL_LOG_LEVEL from the environment and writes JSON to stderr
// so the parent process can parse structured log messages.
func NewPipeLogger(pipeName string) *slog.Logger {
	level := parseLogLevel(os.Getenv(EnvLogLevel))
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})).With("pipe", pipeName)
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "silent":
		return slog.LevelError + 1
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug", "verbose":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// Run reads a SubprocessRequest from stdin, calls the appropriate handler,
// and writes the response to stdout. If req.Stream is true and streamHandler
// is non-nil, uses the streaming protocol (chunk lines + final envelope).
//
// When VIRGIL_PERSISTENT=1 is set, Run processes requests in a loop until
// stdin is closed, keeping the process alive for reuse by PersistentProcess.
func Run(handler pkgpipe.Handler, streamHandler pkgpipe.StreamHandler) {
	if os.Getenv(EnvPersistent) == "1" {
		runLoop(handler, streamHandler)
		return
	}
	var req protocol.SubprocessRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode request: %v\n", err)
		os.Exit(1)
	}

	if req.Stream && streamHandler != nil {
		runStream(streamHandler, req)
	} else {
		runSync(handler, req)
	}
}

func runLoop(handler pkgpipe.Handler, streamHandler pkgpipe.StreamHandler) {
	dec := json.NewDecoder(os.Stdin)
	for {
		var req protocol.SubprocessRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "failed to decode request: %v\n", err)
			return
		}
		if req.Stream && streamHandler != nil {
			runStream(streamHandler, req)
		} else {
			runSync(handler, req)
		}
	}
}

func runSync(handler pkgpipe.Handler, req protocol.SubprocessRequest) {
	result := handler(req.Envelope, req.Flags)
	enc := json.NewEncoder(os.Stdout)
	var err error
	if req.Stream {
		err = enc.Encode(protocol.SubprocessChunk{Envelope: &result})
	} else {
		err = enc.Encode(result)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
		os.Exit(1)
	}
}

func runStream(handler pkgpipe.StreamHandler, req protocol.SubprocessRequest) {
	enc := json.NewEncoder(os.Stdout)
	sink := func(chunk string) {
		if err := enc.Encode(protocol.SubprocessChunk{Chunk: chunk}); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode chunk: %v\n", err)
		}
	}
	ctx := context.Background()
	result := handler(ctx, req.Envelope, req.Flags, sink)
	if err := enc.Encode(protocol.SubprocessChunk{Envelope: &result}); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
		os.Exit(1)
	}
}

// Fatal writes a fatal error envelope to stdout and exits with code 1.
// Used by pipe main.go for startup failures (e.g., can't open DB).
func Fatal(pipeName, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(pkgenv.NewFatalError(pipeName, message))
	os.Exit(1)
}
