package main

import (
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/shell"
)

func main() {
	logger := pipehost.NewPipeLogger("shell")

	allowlist := []string{
		"go", "git", "make", "just",
		"grep", "find", "ls", "cat", "head", "tail", "wc",
		"diff", "patch",
		"echo", "printf", "true", "false", "test",
		"mkdir", "cp", "mv", "touch",
	}

	executor := &shell.OSExecutor{}
	logger.Info("initialized", "allowed_commands", allowlist)
	pipehost.Run(shell.NewHandler(executor, allowlist, logger), nil)
}
