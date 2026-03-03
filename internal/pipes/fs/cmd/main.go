package main

import (
	"os"
	"strings"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/fs"
)

func main() {
	logger := pipehost.NewPipeLogger("fs")

	projectRoot := os.Getenv(pipehost.EnvConfigDir)
	if projectRoot == "" {
		pipehost.Fatal("fs", "VIRGIL_CONFIG_DIR not set")
	}

	var allowedPaths []string
	if extra := os.Getenv("VIRGIL_FS_ALLOWED_PATHS"); extra != "" {
		allowedPaths = strings.Split(extra, ":")
	}

	logger.Info("initialized", "project_root", projectRoot, "extra_paths", len(allowedPaths))
	pipehost.Run(fs.NewHandler(projectRoot, allowedPaths, logger), nil)
}
