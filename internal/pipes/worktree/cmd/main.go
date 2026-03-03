package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/worktree"
)

func main() {
	logger := pipehost.NewPipeLogger("worktree")
	executor := &worktree.OSGitExecutor{}
	pipehost.Run(worktree.NewHandler(executor, logger), nil)
}
