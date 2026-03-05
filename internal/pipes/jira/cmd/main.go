package main

import (
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/jira"
	"gopkg.in/yaml.v3"
)

func main() {
	logger := pipehost.NewPipeLogger("jira")

	userDir := os.Getenv(pipehost.EnvUserDir)
	if userDir == "" {
		pipehost.Fatal("jira", "VIRGIL_USER_DIR not set")
	}

	cfgData, err := os.ReadFile(filepath.Join(userDir, "jira.yaml"))
	if err != nil {
		pipehost.Fatal("jira", "cannot read jira.yaml from "+userDir+": "+err.Error()+
			"\n\nCreate jira.yaml with:\n  base_url: https://yourco.atlassian.net\n  email: you@example.com\n  token: your-pat")
	}

	var cfg jira.JiraConfig
	if err := yaml.Unmarshal(cfgData, &cfg); err != nil {
		pipehost.Fatal("jira", "invalid jira.yaml: "+err.Error())
	}

	if cfg.BaseURL == "" || cfg.Email == "" || cfg.Token == "" {
		pipehost.Fatal("jira", "jira.yaml is incomplete — base_url, email, and token are all required")
	}

	client := jira.NewClient(cfg)
	logger.Info("initialized", "base_url", cfg.BaseURL)
	pipehost.Run(jira.NewHandler(client, logger), nil)
}
