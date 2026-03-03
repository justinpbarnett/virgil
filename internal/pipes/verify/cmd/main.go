package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/verify"
)

func main() {
	logger := pipehost.NewPipeLogger("verify")
	executor := &verify.OSExecutor{}

	// Provider is optional — only needed for plan-check
	var provider bridge.Provider
	if os.Getenv(pipehost.EnvProvider) != "" {
		p, err := pipehost.BuildProviderFromEnvWithLogger(logger)
		if err != nil {
			logger.Warn("provider unavailable, plan-check disabled", "error", err)
		} else {
			provider = p
		}
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("verify", err.Error())
	}

	pipehost.Run(verify.NewHandler(executor, provider, pc, logger), nil)
}
