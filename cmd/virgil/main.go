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

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/server"
	"github.com/justinpbarnett/virgil/internal/tui"
)

const defaultPipesDir = "internal/pipes"

func main() {
	configDir := flag.String("config", "", "config directory path")
	serverMode := flag.Bool("server", false, "run in server-only mode")
	flag.Parse()

	// Resolve config directory
	cfgDir := *configDir
	if cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".config", "virgil")
		if _, err := os.Stat(filepath.Join(cfgDir, "virgil.yaml")); os.IsNotExist(err) {
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
	cfg, err := config.Load(cfgDir, defaultPipesDir)
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

	// Pipe mode: stdin is not a terminal
	if tui.IsPiped() {
		signal := strings.Join(args, " ")
		if err := tui.RunPipe(signal, serverAddr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
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
	cfg, err := config.Load(cfgDir, defaultPipesDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set log level from config
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: config.ToSlogLevel(cfg.LogLevel)}))

	logger.Info("server started", "log_level", cfg.LogLevel, "config_dir", cfgDir, "port", cfg.Server.Port)

	if cfg.DatabasePath == "" {
		logger.Warn("database_path not set in virgil.yaml — pipes requiring storage will fail")
	}

	// 2. Build vocabulary, parser, router
	vocab := parser.LoadVocabulary(cfg.Vocabulary)
	p := parser.New(vocab)

	// 3. Register all pipes as subprocesses
	reg := pipe.NewRegistry()
	baseEnv := pipeEnv(cfg, cfgDir)
	baseEnv = baseEnv[:len(baseEnv):len(baseEnv)] // clip capacity to prevent aliasing

	for name, pc := range cfg.Pipes {
		handlerPath := pc.HandlerPath()
		if err := validateExecutable(handlerPath); err != nil {
			logger.Warn("pipe handler not available, skipping", "pipe", name, "error", err)
			continue
		}
		pipeLogLevel := pc.EffectiveLogLevel(cfg.LogLevel)
		env := append(baseEnv,
			pipehost.EnvLogLevel+"="+pipeLogLevel.String(),
			pipehost.EnvModel+"="+pc.EffectiveModel(cfg.Provider.Model),
			pipehost.EnvMaxTurns+"="+strconv.Itoa(pc.EffectiveMaxTurns()),
		)
		sc := pipe.SubprocessConfig{
			Name:       name,
			Executable: handlerPath,
			WorkDir:    pc.Dir,
			Timeout:    pc.TimeoutDuration(),
			Env:        env,
			Logger:     logger,
		}
		reg.Register(pc.ToDefinition(), pipe.SubprocessHandler(sc))
		if pc.Streaming {
			reg.RegisterStream(name, pipe.SubprocessStreamHandler(sc))
		}
		logger.Info("registered pipe", "pipe", name, "handler", handlerPath)
	}

	// Build router from registered definitions
	missLogPath := filepath.Join(config.DataDir(), "misses.jsonl")
	missLog, err := router.NewMissLog(missLogPath)
	if err != nil {
		logger.Warn("miss log not available", "error", err)
	}
	if missLog != nil {
		defer missLog.Close()
	}

	rt := router.NewRouter(reg.Definitions(), missLog, logger)

	// Build planner
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources, logger)

	// Build runtime
	observer := runtime.NewLogObserver(logger, cfg.LogLevel)
	run := runtime.NewWithLevel(reg, observer, logger, cfg.LogLevel)

	// 4. Start HTTP server
	srv := server.New(server.Deps{
		Config:   cfg,
		Router:   rt,
		Parser:   p,
		Planner:  pl,
		Runtime:  run,
		Registry: reg,
		Logger:   logger,
	})
	return srv.Start()
}

// pipeEnv builds the environment variable list passed to pipe subprocesses.
func pipeEnv(cfg *config.Config, cfgDir string) []string {
	env := os.Environ()
	env = append(env,
		pipehost.EnvDBPath+"="+cfg.DatabasePath,
		pipehost.EnvConfigDir+"="+cfgDir,
		pipehost.EnvUserDir+"="+config.UserDir(),
		pipehost.EnvProvider+"="+cfg.Provider.Name,
		pipehost.EnvProviderBinary+"="+cfg.Provider.Binary,
	)
	return env
}

// validateExecutable checks that the file at path exists and is executable.
func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}
