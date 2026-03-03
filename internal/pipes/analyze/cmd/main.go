package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/analyze"
)

func main() {
	logger := pipehost.NewPipeLogger("analyze")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("analyze", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("analyze", err.Error())
	}

	compiled := analyze.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.Run(analyze.NewHandlerWith(provider, pc, compiled, logger), nil)
}
