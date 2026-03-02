package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/memory"
	"github.com/justinpbarnett/virgil/internal/store"
)

func main() {
	logger := pipehost.NewPipeLogger("memory")

	s, err := store.Open(os.Getenv(pipehost.EnvDBPath))
	if err != nil {
		pipehost.Fatal("memory", err.Error())
	}
	defer s.Close()

	logger.Info("initialized")
	pipehost.Run(memory.NewHandler(s, logger), nil)
}
