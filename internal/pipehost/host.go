package pipehost

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
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
)

// NewPipeLogger creates an slog.Logger for a pipe subprocess.
// It reads VIRGIL_LOG_LEVEL from the environment and writes JSON to stderr
// so the parent process can parse structured log messages.
func NewPipeLogger(pipeName string) *slog.Logger {
	levelStr := os.Getenv(EnvLogLevel)
	level := config.ParseLogLevel(levelStr)
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: config.ToSlogLevel(level),
	})).With("pipe", pipeName)
}

// Run reads a SubprocessRequest from stdin, calls the appropriate handler,
// and writes the response to stdout. If req.Stream is true and streamHandler
// is non-nil, uses the streaming protocol (chunk lines + final envelope).
func Run(handler pipe.Handler, streamHandler pipe.StreamHandler) {
	var req pipe.SubprocessRequest
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

func runSync(handler pipe.Handler, req pipe.SubprocessRequest) {
	result := handler(req.Envelope, req.Flags)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
		os.Exit(1)
	}
}

func runStream(handler pipe.StreamHandler, req pipe.SubprocessRequest) {
	enc := json.NewEncoder(os.Stdout)

	sink := func(chunk string) {
		if err := enc.Encode(pipe.SubprocessChunk{Chunk: chunk}); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode chunk: %v\n", err)
		}
	}

	ctx := context.Background()
	result := handler(ctx, req.Envelope, req.Flags, sink)
	if err := enc.Encode(pipe.SubprocessChunk{Envelope: &result}); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
		os.Exit(1)
	}
}

// RunWithStreaming is a convenience wrapper that handles the StreamingProvider
// type assertion. If the provider implements StreamingProvider and makeStream
// is non-nil, the stream handler is used; otherwise only the sync handler runs.
func RunWithStreaming(provider bridge.Provider, handler pipe.Handler, makeStream func(bridge.StreamingProvider) pipe.StreamHandler) {
	if makeStream != nil {
		if sp, ok := provider.(bridge.StreamingProvider); ok {
			Run(handler, makeStream(sp))
			return
		}
	}
	Run(handler, nil)
}

// Fatal writes a fatal error envelope to stdout and exits with code 1.
// Used by pipe main.go for startup failures (e.g., can't open DB).
func Fatal(pipeName, message string) {
	json.NewEncoder(os.Stdout).Encode(envelope.NewFatalError(pipeName, message))
	os.Exit(1)
}

// BuildProviderFromEnv creates a bridge.Provider from VIRGIL_PROVIDER,
// VIRGIL_MODEL, and VIRGIL_PROVIDER_BINARY environment variables.
func BuildProviderFromEnv() (bridge.Provider, error) {
	return BuildProviderFromEnvWithLogger(nil)
}

// BuildProviderFromEnvWithLogger creates a bridge.Provider with the given logger.
// If log level is verbose, enables verbose prompt logging on the provider.
func BuildProviderFromEnvWithLogger(logger *slog.Logger) (bridge.Provider, error) {
	name := os.Getenv(EnvProvider)
	if name == "" {
		name = "claude"
	}
	maxTurns := 1
	if s := os.Getenv(EnvMaxTurns); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			maxTurns = v
		}
	}
	cfg := bridge.ProviderConfig{
		Name:     name,
		Model:    os.Getenv(EnvModel),
		Binary:   os.Getenv(EnvProviderBinary),
		MaxTurns: maxTurns,
		Verbose:  config.ParseLogLevel(os.Getenv(EnvLogLevel)) == config.Verbose,
		Logger:   logger,
	}
	return bridge.NewProvider(cfg)
}

// LoadPipeConfig loads and returns the PipeConfig from pipe.yaml in the
// current working directory.
func LoadPipeConfig() (config.PipeConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.PipeConfig{}, fmt.Errorf("getting working directory: %w", err)
	}
	return LoadPipeConfigFrom(filepath.Join(cwd, "pipe.yaml"))
}

// LoadPipeConfigFrom loads a PipeConfig from the given YAML path.
func LoadPipeConfigFrom(path string) (config.PipeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.PipeConfig{}, fmt.Errorf("reading pipe.yaml: %w", err)
	}
	var pc config.PipeConfig
	if err := config.UnmarshalPipeConfig(data, &pc); err != nil {
		return config.PipeConfig{}, fmt.Errorf("parsing pipe.yaml: %w", err)
	}
	return pc, nil
}
