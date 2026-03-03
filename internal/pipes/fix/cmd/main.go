package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/fix"
)

func main() {
	logger := pipehost.NewPipeLogger("fix")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("fix", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("fix", err.Error())
	}

	compiled := fix.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.RunWithStreaming(provider, fix.NewHandlerWith(provider, pc, compiled, logger), func(sp bridge.StreamingProvider) pipe.StreamHandler {
		return fix.NewStreamHandlerWith(sp, pc, compiled, logger)
	})
}
