package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/decompose"
)

func main() {
	logger := pipehost.NewPipeLogger("decompose")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("decompose", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("decompose", err.Error())
	}

	compiled := decompose.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.Run(
		decompose.NewHandlerWith(provider, pc, compiled, logger),
		nil, // no streaming — decompose is synchronous
	)
}
