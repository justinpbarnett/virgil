package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	calendarPipe "github.com/justinpbarnett/virgil/internal/pipes/calendar"
	chatPipe "github.com/justinpbarnett/virgil/internal/pipes/chat"
	draftPipe "github.com/justinpbarnett/virgil/internal/pipes/draft"
	memoryPipe "github.com/justinpbarnett/virgil/internal/pipes/memory"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/server"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/tui"
)

func main() {
	configDir := flag.String("config", "", "config directory path")
	serverMode := flag.Bool("server", false, "run in server-only mode")
	flag.Parse()

	// Resolve config directory
	cfgDir := *configDir
	if cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".config", "virgil")
		if _, err := os.Stat(cfgDir); os.IsNotExist(err) {
			cfgDir = "config"
		}
	}

	// Set up logging
	logLevel := slog.LevelInfo
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	if *serverMode {
		if err := runServer(cfgDir, logger); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Client mode
	args := flag.Args()

	// Load config to get server address
	cfg, err := config.Load(cfgDir)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	serverAddr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))

	// Ensure server is running
	binary, _ := os.Executable()
	if err := tui.EnsureServer(binary, serverAddr); err != nil {
		logger.Warn("auto-start failed, attempting inline", "error", err)
	}

	if len(args) > 0 {
		// One-shot mode
		signal := strings.Join(args, " ")
		if err := tui.RunOneShot(signal, serverAddr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Session mode
		if err := tui.RunSession(serverAddr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func runServer(cfgDir string, logger *slog.Logger) error {
	// 1. Load configuration
	cfg, err := config.Load(cfgDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set log level from config
	switch cfg.LogLevel {
	case "debug":
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	logger.Info("virgil v0.1.0 starting",
		"config_dir", cfgDir,
		"port", cfg.Server.Port,
	)

	// 2. Initialize SQLite store
	memStore, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer memStore.Close()
	logger.Info("memory store opened", "path", cfg.DatabasePath)

	// 3. Build vocabulary, parser, router
	vocab := parser.LoadVocabulary(cfg.Vocabulary)
	p := parser.New(vocab)

	// 4. Initialize AI bridge
	providerCfg := bridge.ProviderConfig{
		Name:  cfg.Provider.Name,
		Model: cfg.Provider.Model,
		Options: map[string]string{
			"binary": cfg.Provider.Binary,
		},
	}
	provider, err := bridge.NewProvider(providerCfg)
	if err != nil {
		logger.Warn("AI provider not available", "error", err)
	} else {
		if cp, ok := provider.(*bridge.ClaudeProvider); ok {
			if !cp.Available() {
				logger.Warn("claude CLI not found on PATH — non-deterministic pipes will return errors")
			} else {
				logger.Info("AI provider ready", "provider", cfg.Provider.Name, "model", cfg.Provider.Model)
			}
		}
	}

	// 5. Register all pipe handlers
	reg := pipe.NewRegistry()

	// Memory pipe
	if memCfg, ok := cfg.Pipes["memory"]; ok {
		reg.Register(memCfg.ToDefinition(), memoryPipe.NewHandler(memStore))
	}

	// Calendar pipe
	if calCfg, ok := cfg.Pipes["calendar"]; ok {
		var calClient calendarPipe.CalendarClient
		home, _ := os.UserHomeDir()
		calConfigDir := filepath.Join(home, ".config", "virgil")
		gc, err := calendarPipe.NewGoogleClient(calConfigDir)
		if err != nil {
			logger.Warn("calendar not configured", "error", err)
		} else {
			calClient = gc
		}
		reg.Register(calCfg.ToDefinition(), calendarPipe.NewHandler(calClient))
	}

	// Draft pipe
	if draftCfg, ok := cfg.Pipes["draft"]; ok {
		if provider != nil {
			reg.Register(draftCfg.ToDefinition(), draftPipe.NewHandler(provider, draftCfg))
		} else {
			reg.Register(draftCfg.ToDefinition(), errorHandler("draft", "AI provider not configured"))
		}
	}

	// Chat pipe
	if chatCfg, ok := cfg.Pipes["chat"]; ok {
		if provider != nil {
			reg.Register(chatCfg.ToDefinition(), chatPipe.NewHandler(provider))
		} else {
			reg.Register(chatCfg.ToDefinition(), errorHandler("chat", "AI provider not configured"))
		}
	}

	// Build router from registered definitions
	home, _ := os.UserHomeDir()
	missLogPath := filepath.Join(home, ".local", "share", "virgil", "misses.jsonl")
	missLog, err := router.NewMissLog(missLogPath)
	if err != nil {
		logger.Warn("miss log not available", "error", err)
	}
	if missLog != nil {
		defer missLog.Close()
	}

	rt := router.NewRouter(reg.Definitions(), missLog)

	// Build planner
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources)

	// Build runtime
	observer := runtime.NewLogObserver(logger, cfg.LogLevel)
	run := runtime.New(reg, observer)

	// 6. Start HTTP server
	srv := server.New(cfg, rt, p, pl, run, reg, logger)
	return srv.Start()
}

func errorHandler(name, msg string) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New(name, "error")
		out.Error = &envelope.EnvelopeError{
			Message:  msg,
			Severity: "fatal",
		}
		return out
	}
}
