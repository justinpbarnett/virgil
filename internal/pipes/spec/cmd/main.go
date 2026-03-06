package main

import (
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/spec"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
	"github.com/justinpbarnett/virgil/internal/store"
)

func main() {
	logger := pipehost.NewPipeLogger("spec")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("spec", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("spec", err.Error())
	}

	// Open store for spec/active working_state (nil if unavailable).
	var st spec.SpecStore
	if dbPath := os.Getenv(pipehost.EnvDBPath); dbPath != "" {
		if s, err := store.Open(dbPath); err == nil {
			st = s
			defer s.Close()
		}
	}

	specsDir := filepath.Join(os.Getenv(pipehost.EnvWorkDir), "specs")
	compiled := pipeutil.CompileTemplates(pc)

	logger.Info("initialized")

	pipehost.RunWithStreaming(provider,
		spec.NewHandlerWith(provider, pc, compiled, st, specsDir, logger),
		func(sp bridge.StreamingProvider) pipe.StreamHandler {
			return spec.NewStreamHandlerWith(sp, pc, compiled, st, specsDir, logger)
		},
	)
}
