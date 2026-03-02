package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/draft"
)

func main() {
	logger := pipehost.NewPipeLogger("draft")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("draft", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("draft", err.Error())
	}

	compiled := draft.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.RunWithStreaming(provider, draft.NewHandlerWith(provider, pc, compiled, logger), func(sp bridge.StreamingProvider) pipe.StreamHandler {
		return draft.NewStreamHandlerWith(sp, pc, compiled, logger)
	})
}
