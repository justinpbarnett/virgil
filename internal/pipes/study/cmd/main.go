package main

import (
	"fmt"
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/study"
	"github.com/justinpbarnett/virgil/internal/store"
)

func main() {
	logger := pipehost.NewPipeLogger("study")

	// Provider is optional — Tiers 0-2 work without it
	provider, providerErr := pipehost.BuildProviderFromEnvWithLogger(logger)
	if providerErr != nil {
		logger.Warn("AI provider unavailable, Tier 3 compression disabled", "error", providerErr)
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("study", err.Error())
	}

	// Open memory store (optional — only needed for memory source)
	var s *store.Store
	dbPath := os.Getenv(pipehost.EnvDBPath)
	if dbPath != "" {
		s, err = store.Open(dbPath)
		if err != nil {
			logger.Warn("memory store unavailable", "error", err)
		} else {
			defer s.Close()
		}
	}

	// Determine workspace directory: prefer server's CWD over subprocess CWD
	workDir := os.Getenv(pipehost.EnvWorkDir)
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			pipehost.Fatal("study", fmt.Sprintf("cannot determine working directory: %v", err))
		}
	}

	handler := study.NewHandler(study.Config{
		Provider:   provider,
		Store:      s,
		WorkDir:    workDir,
		PipeConfig: pc,
		Logger:     logger,
	})

	logger.Info("initialized")
	pipehost.Run(handler, nil) // no streaming — study returns a complete result
}
