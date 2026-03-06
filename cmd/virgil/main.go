package main

import (
	"context"
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
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/study"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/server"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/tui"
	"github.com/justinpbarnett/virgil/internal/voice"
)

const defaultPipesDir = "internal/pipes"

func main() {
	configDir := flag.String("config", "", "config directory path")
	serverMode := flag.Bool("server", false, "run in server-only mode")
	voiceMode := flag.Bool("voice", false, "run voice daemon")
	flag.Parse()

	// Resolve config directory
	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.UserDir()
		if _, err := os.Stat(filepath.Join(cfgDir, "virgil.yaml")); os.IsNotExist(err) {
			cfgDir = "config"
		}
	}

	// Load credentials file before any provider is initialized.
	// System env vars take precedence; this is a fallback only.
	if err := config.LoadCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: credentials.yaml: %v\n", err)
	}

	// Set up logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *serverMode {
		if err := runServer(cfgDir); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if *voiceMode {
		if err := runVoiceDaemon(cfgDir, logger); err != nil {
			logger.Error("voice daemon failed", "error", err)
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
	binary, err := os.Executable()
	if err != nil {
		logger.Warn("could not determine executable path", "error", err)
	}
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

func runVoiceDaemon(cfgDir string, logger *slog.Logger) error {
	voiceCfg, err := config.LoadVoiceConfig(config.UserDir())
	if err != nil {
		return fmt.Errorf("loading voice config: %w", err)
	}
	if voiceCfg == nil {
		return fmt.Errorf("voice.json not found in %s — see docs/setup.md for setup instructions", config.UserDir())
	}
	if err := voiceCfg.Validate(); err != nil {
		return err
	}

	cfg, err := config.Load(cfgDir, defaultPipesDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	serverAddr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))

	binary, err := os.Executable()
	if err != nil {
		logger.Warn("could not determine executable path", "error", err)
	}
	if err := tui.EnsureServer(binary, serverAddr); err != nil {
		logger.Warn("auto-start failed", "error", err)
	}

	daemon, err := voice.NewDaemon(voiceCfg, serverAddr)
	if err != nil {
		return fmt.Errorf("initializing voice daemon: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return daemon.Run(ctx)
}

func runServer(cfgDir string) error {
	// 1. Load configuration
	cfg, err := config.Load(cfgDir, defaultPipesDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set log level from config
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: config.ToSlogLevel(cfg.LogLevel)}))

	logger.Info("server started", "log_level", cfg.LogLevel, "config_dir", cfgDir, "port", cfg.Server.Port)

	if cfg.DatabasePath == "" {
		logger.Warn("database_path not set in virgil.yaml — pipes requiring storage will fail")
	}

	// 2. Build vocabulary, parser, router
	vocab := parser.LoadVocabulary(cfg.Vocabulary)
	p := parser.New(vocab)

	// Capture server's working directory for passing to subprocesses
	workDir, _ := os.Getwd()

	// 3. Register all pipes as persistent subprocesses
	reg := pipe.NewRegistry()
	defer reg.Shutdown()
	baseEnv := pipeEnv(cfg, cfgDir, workDir)
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
			pipehost.EnvProvider+"="+pc.EffectiveProvider(cfg.Provider.Name),
			pipehost.EnvModel+"="+pc.EffectiveModel(cfg.Provider.Model),
			pipehost.EnvMaxTurns+"="+strconv.Itoa(pc.EffectiveMaxTurns()),
			pipehost.EnvMaxTokens+"="+strconv.Itoa(pc.EffectiveMaxTokens(cfg.Provider.MaxTokens)),
		)
		sc := pipe.SubprocessConfig{
			Name:       name,
			Executable: handlerPath,
			WorkDir:    pc.Dir,
			Timeout:    pc.TimeoutDuration(),
			Env:        env,
			Logger:     logger,
		}
		if err := reg.RegisterPersistent(pc.ToDefinition(), sc, pc.Streaming); err != nil {
			logger.Warn("failed to start persistent pipe, falling back to spawn-per-call", "pipe", name, "error", err)
			reg.Register(pc.ToDefinition(), pipe.SubprocessHandler(sc))
			if pc.Streaming {
				reg.RegisterStream(name, pipe.SubprocessStreamHandler(sc))
			}
		}
		logger.Info("registered pipe", "pipe", name, "handler", handlerPath)
	}

	// Build router from registered definitions
	missLogPath := config.DailyPath(config.LogDir(), "misses", ".jsonl")
	missLog, err := router.NewMissLog(missLogPath)
	if err != nil {
		logger.Warn("miss log not available", "error", err)
	}
	if missLog != nil {
		defer missLog.Close()
	}

	rt := router.NewRouter(reg.Definitions(), logger)
	defer rt.Close()

	// Build AI planner for Layer 4 routing
	var aiPlanner *planner.AIPlanner
	plannerProvider, plannerErr := bridge.CreateProvider(bridge.ProviderConfig{
		Name:      cfg.Planner.Provider,
		Model:     cfg.Planner.Model,
		MaxTokens: cfg.Planner.MaxTokens,
		Logger:    logger,
	})
	if plannerErr != nil {
		logger.Warn("AI planner unavailable", "provider", cfg.Planner.Provider, "error", plannerErr)
	} else {
		aiPlanner = planner.NewAIPlanner(plannerProvider, reg.Definitions(), logger)
	}

	// Build planner
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources, logger)

	// Build runtime with format templates
	observer := runtime.NewLogObserver(logger, cfg.LogLevel)
	run, err := runtime.NewWithFormats(reg, observer, logger, cfg.LogLevel, cfg.RawFormats())
	if err != nil {
		return fmt.Errorf("building runtime: %w", err)
	}

	// Wire memory infrastructure if database is configured
	var st *store.Store
	if cfg.DatabasePath != "" {
		var stErr error
		st, stErr = store.Open(cfg.DatabasePath)
		if stErr != nil {
			logger.Warn("memory infrastructure unavailable", "error", stErr)
		} else {
			defer st.Close()
			injector := runtime.NewStoreMemoryInjector(st)
			if workDir != "" {
				capturedWorkDir := workDir
				injector.WithCodebaseSearch(func(ctx context.Context, query string, budget int) ([]envelope.MemoryEntry, error) {
					return study.SearchCodebase(ctx, query, capturedWorkDir, budget)
				})
			}
			memConfigs := make(map[string]config.MemoryConfig)
			for name, pc := range cfg.Pipes {
				memConfigs[name] = pc.Memory
			}
			run.WithMemory(
				injector,
				runtime.NewStoreMemorySaver(st),
				memConfigs,
			)
			logger.Info("memory infrastructure enabled")
		}
	}

	// 4. Start HTTP server
	srv := server.New(server.Deps{
		Config:    cfg,
		Router:    rt,
		Parser:    p,
		Planner:   pl,
		Runtime:   run,
		Registry:  reg,
		AIPlanner: aiPlanner,
		MissLog:   missLog,
		Store:     st,
		Logger:    logger,
	})
	return srv.Start()
}

// pipeEnv builds the environment variable list passed to pipe subprocesses.
func pipeEnv(cfg *config.Config, cfgDir, workDir string) []string {
	env := os.Environ()
	env = append(env,
		pipehost.EnvDBPath+"="+cfg.DatabasePath,
		pipehost.EnvConfigDir+"="+cfgDir,
		pipehost.EnvUserDir+"="+config.UserDir(),
		pipehost.EnvProvider+"="+cfg.Provider.Name,
		pipehost.EnvProviderBinary+"="+cfg.Provider.Binary,
		pipehost.EnvIdentity+"="+cfg.Identity,
		pipehost.EnvWorkDir+"="+workDir,
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
