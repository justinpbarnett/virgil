package main

import (
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/slack"
	"gopkg.in/yaml.v3"
)

func main() {
	logger := pipehost.NewPipeLogger("slack")

	userDir := os.Getenv(pipehost.EnvUserDir)
	if userDir == "" {
		pipehost.Fatal("slack", "VIRGIL_USER_DIR not set")
	}

	cfgData, err := os.ReadFile(filepath.Join(userDir, "slack.yaml"))
	if err != nil {
		pipehost.Fatal("slack", "cannot read slack.yaml from "+userDir+": "+err.Error()+
			"\n\nCreate slack.yaml with:\n  token: xoxp-...\n  user_id: U...\n  channels:\n    - C...")
	}

	var cfg slack.SlackConfig
	if err := yaml.Unmarshal(cfgData, &cfg); err != nil {
		pipehost.Fatal("slack", "invalid slack.yaml: "+err.Error())
	}

	if cfg.Token == "" || cfg.UserID == "" || len(cfg.Channels) == 0 {
		pipehost.Fatal("slack", "slack.yaml is incomplete — token, user_id, and channels are all required")
	}

	client := slack.NewClient(cfg)
	logger.Info("initialized", "channels", len(cfg.Channels))
	pipehost.Run(slack.NewHandler(client, logger), nil)
}
