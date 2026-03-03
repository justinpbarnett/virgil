package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/review"
)

func main() {
	logger := pipehost.NewPipeLogger("review")

	provider, err := pipehost.BuildProviderFromEnvWithLogger(logger)
	if err != nil {
		pipehost.Fatal("review", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("review", err.Error())
	}

	compiled := review.CompileTemplates(pc)

	logger.Info("initialized")
	pipehost.Run(review.NewHandlerWith(provider, pc, compiled, &review.GHDiffFetcher{}, logger), nil)
}
