package pipehost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	EnvMaxTokens      = "VIRGIL_MAX_TOKENS"
	EnvIdentity       = "VIRGIL_IDENTITY"
	EnvWorkDir        = "VIRGIL_WORK_DIR"
	// EnvPersistent enables persistent (loop) mode: when set to "1", Run
	// processes requests in a loop until stdin is closed rather than exiting
	// after a single request.
	EnvPersistent = "VIRGIL_PERSISTENT"
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
//
// When VIRGIL_PERSISTENT=1 is set, Run processes requests in a loop until
// stdin is closed, keeping the process alive for reuse by PersistentProcess.
func Run(handler pipe.Handler, streamHandler pipe.StreamHandler) {
	if os.Getenv(EnvPersistent) == "1" {
		runLoop(handler, streamHandler)
		return
	}
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

// runLoop processes requests continuously until stdin is closed (EOF).
func runLoop(handler pipe.Handler, streamHandler pipe.StreamHandler) {
	dec := json.NewDecoder(os.Stdin)
	for {
		var req pipe.SubprocessRequest
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
// Both sync and stream handlers are wrapped to inject token usage into the
// result envelope when the provider implements UsageReporter.
func RunWithStreaming(provider bridge.Provider, handler pipe.Handler, makeStream func(bridge.StreamingProvider) pipe.StreamHandler) {
	wrappedHandler := wrapHandlerWithUsage(provider, handler)
	if makeStream != nil {
		if sp, ok := provider.(bridge.StreamingProvider); ok {
			wrappedStream := wrapStreamHandlerWithUsage(provider, makeStream(sp))
			Run(wrappedHandler, wrappedStream)
			return
		}
	}
	Run(wrappedHandler, nil)
}

// wrapHandlerWithUsage wraps a Handler to inject provider usage into the result
// envelope after the handler returns.
func wrapHandlerWithUsage(provider bridge.Provider, h pipe.Handler) pipe.Handler {
	ur, ok := provider.(bridge.UsageReporter)
	if !ok {
		return h
	}
	return func(env envelope.Envelope, flags map[string]string) envelope.Envelope {
		result := h(env, flags)
		injectProviderUsage(&result, ur)
		return result
	}
}

// wrapStreamHandlerWithUsage wraps a StreamHandler to inject provider usage into
// the result envelope after the handler returns.
func wrapStreamHandlerWithUsage(provider bridge.Provider, sh pipe.StreamHandler) pipe.StreamHandler {
	ur, ok := provider.(bridge.UsageReporter)
	if !ok {
		return sh
	}
	return func(ctx context.Context, env envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		result := sh(ctx, env, flags, sink)
		injectProviderUsage(&result, ur)
		return result
	}
}

// injectProviderUsage reads the last usage from the provider and attaches it
// to the envelope. Only injects when there were actual tokens consumed.
func injectProviderUsage(result *envelope.Envelope, ur bridge.UsageReporter) {
	u := ur.LastUsage()
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	result.Usage = &u
}

// Fatal writes a fatal error envelope to stdout and exits with code 1.
// Used by pipe main.go for startup failures (e.g., can't open DB).
func Fatal(pipeName, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(envelope.NewFatalError(pipeName, message))
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
	maxTokens := 8192
	if s := os.Getenv(EnvMaxTokens); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			maxTokens = v
		}
	}
	cfg := bridge.ProviderConfig{
		Name:      name,
		Model:     os.Getenv(EnvModel),
		Binary:    os.Getenv(EnvProviderBinary),
		MaxTurns:  maxTurns,
		MaxTokens: maxTokens,
		Verbose:   config.ParseLogLevel(os.Getenv(EnvLogLevel)) == config.Verbose,
		Logger:    logger,
	}
	return bridge.CreateProvider(cfg)
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
	injectIdentity(&pc)
	return pc, nil
}

func injectIdentity(pc *config.PipeConfig) {
	identity := strings.TrimSpace(os.Getenv(EnvIdentity))
	if identity == "" {
		return
	}
	if pc.Prompts.System != "" {
		pc.Prompts.System = identity + "\n\n" + pc.Prompts.System
	}
	for k, v := range pc.Prompts.Templates {
		pc.Prompts.Templates[k] = identity + "\n\n" + v
	}
}
