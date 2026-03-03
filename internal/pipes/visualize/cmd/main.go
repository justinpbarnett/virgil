package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
	"github.com/justinpbarnett/virgil/internal/pipes/visualize"
)

func main() {
	logger := pipehost.NewPipeLogger("visualize")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("visualize", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("visualize", err.Error())
	}

	compiled := visualize.CompileTemplates(pc)

	renderer := &visualize.ManimRenderer{
		Executor:  &pipeutil.OSExecutor{},
		OutputDir: os.TempDir(),
	}

	logger.Info("initialized")
	pipehost.Run(
		visualize.NewHandler(provider, pc, compiled, renderer, logger),
		nil,
	)
}
