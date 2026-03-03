package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/jira"
)

func main() {
	logger := pipehost.NewPipeLogger("jira")

	userDir := os.Getenv(pipehost.EnvUserDir)
	if userDir == "" {
		pipehost.Fatal("jira", "VIRGIL_USER_DIR not set")
	}

	cfgData, err := os.ReadFile(filepath.Join(userDir, "jira.json"))
	if err != nil {
		pipehost.Fatal("jira", "cannot read jira.json from "+userDir+": "+err.Error()+
			"\n\nCreate jira.json with: {\"base_url\": \"https://yourco.atlassian.net\", \"email\": \"you@example.com\", \"token\": \"your-pat\"}")
	}

	var cfg jira.JiraConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		pipehost.Fatal("jira", "invalid jira.json: "+err.Error())
	}

	if cfg.BaseURL == "" || cfg.Email == "" || cfg.Token == "" {
		pipehost.Fatal("jira", "jira.json is incomplete — base_url, email, and token are all required")
	}

	client := jira.NewClient(cfg)
	logger.Info("initialized", "base_url", cfg.BaseURL)
	pipehost.Run(jira.NewHandler(client, logger), nil)
}
