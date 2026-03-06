package main

import (
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	jirapkg "github.com/justinpbarnett/virgil/internal/pipes/jira"
	slackpkg "github.com/justinpbarnett/virgil/internal/pipes/slack"
	syncpkg "github.com/justinpbarnett/virgil/internal/pipes/sync"
	"github.com/justinpbarnett/virgil/internal/store"
	"gopkg.in/yaml.v3"
)

func main() {
	logger := pipehost.NewPipeLogger("sync")

	userDir := os.Getenv(pipehost.EnvUserDir)
	if userDir == "" {
		pipehost.Fatal("sync", "VIRGIL_USER_DIR not set")
	}

	dbPath := os.Getenv(pipehost.EnvDBPath)
	if dbPath == "" {
		pipehost.Fatal("sync", "VIRGIL_DB_PATH not set")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		pipehost.Fatal("sync", "cannot open database: "+err.Error())
	}
	defer st.Close()

	// Load JIRA credentials (required)
	jiraCfgData, err := os.ReadFile(filepath.Join(userDir, "jira.yaml"))
	if err != nil {
		pipehost.Fatal("sync", "cannot read jira.yaml: "+err.Error())
	}
	var jiraCfg jirapkg.JiraConfig
	if err := yaml.Unmarshal(jiraCfgData, &jiraCfg); err != nil {
		pipehost.Fatal("sync", "invalid jira.yaml: "+err.Error())
	}
	if jiraCfg.BaseURL == "" || jiraCfg.Token == "" {
		pipehost.Fatal("sync", "jira.yaml is incomplete — base_url and token are required")
	}
	jiraClient := jirapkg.NewClient(jiraCfg)

	// Load Slack credentials (optional)
	var slackClient *slackpkg.SlackClient
	slackCfgData, err := os.ReadFile(filepath.Join(userDir, "slack.yaml"))
	if err != nil {
		logger.Warn("slack.yaml not found, Slack sync disabled", "error", err)
	} else {
		var slackCfg slackpkg.SlackConfig
		if err := yaml.Unmarshal(slackCfgData, &slackCfg); err != nil {
			logger.Warn("invalid slack.yaml, Slack sync disabled", "error", err)
		} else {
			slackClient = slackpkg.NewClient(slackCfg)
		}
	}

	logger.Info("initialized", "jira_url", jiraCfg.BaseURL, "slack_enabled", slackClient != nil)
	pipehost.Run(syncpkg.NewHandler(jiraClient, slackClient, st, logger), nil)
}
