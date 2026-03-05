package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/build"
)

func main() {
	logger := pipehost.NewPipeLogger("build")

	provider, err := pipehost.BuildAgenticProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("build", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("build", err.Error())
	}

	compiled := build.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.Run(
		build.NewHandlerWith(provider, pc, compiled, logger),
		build.NewStreamHandlerWith(provider, pc, compiled, logger),
	)
}
