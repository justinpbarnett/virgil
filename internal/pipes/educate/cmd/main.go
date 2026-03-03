package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/educate"
)

func main() {
	logger := pipehost.NewPipeLogger("educate")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("educate", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("educate", err.Error())
	}

	compiled := educate.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.RunWithStreaming(provider,
		educate.NewHandler(provider, pc, compiled, logger),
		func(sp bridge.StreamingProvider) pipe.StreamHandler {
			return educate.NewStreamHandler(sp, pc, compiled, logger)
		})
}
