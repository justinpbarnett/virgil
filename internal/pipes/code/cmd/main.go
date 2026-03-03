package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/code"
)

func main() {
	logger := pipehost.NewPipeLogger("code")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("code", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("code", err.Error())
	}

	compiled := code.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.RunWithStreaming(provider, code.NewHandlerWith(provider, pc, compiled, logger), func(sp bridge.StreamingProvider) pipe.StreamHandler {
		return code.NewStreamHandlerWith(sp, pc, compiled, logger)
	})
}
