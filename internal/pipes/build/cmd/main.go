package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/build"
)

func main() {
	logger := pipehost.NewPipeLogger("build")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("build", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("build", err.Error())
	}

	compiled := build.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.RunWithStreaming(provider, build.NewHandlerWith(provider, pc, compiled, logger), func(sp bridge.StreamingProvider) pipe.StreamHandler {
		return build.NewStreamHandlerWith(sp, pc, compiled, logger)
	})
}
