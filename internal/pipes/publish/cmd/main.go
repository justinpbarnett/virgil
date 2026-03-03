package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/publish"
)

func main() {
	logger := pipehost.NewPipeLogger("publish")
	executor := &publish.OSExecutor{}
	pipehost.Run(publish.NewHandler(executor, logger), nil)
}
