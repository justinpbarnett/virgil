package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/todo"
	"github.com/justinpbarnett/virgil/internal/store"
)

func main() {
	logger := pipehost.NewPipeLogger("todo")

	s, err := store.Open(os.Getenv(pipehost.EnvDBPath))
	if err != nil {
		pipehost.Fatal("todo", err.Error())
	}
	defer s.Close()

	logger.Info("initialized")
	pipehost.Run(todo.NewHandler(s, logger), nil)
}
