package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Guide    GuideConfig    `yaml:"guide"`
	AI       AIConfig       `yaml:"ai"`
	Channels ChannelsConfig `yaml:"channels"`
	Skills   SkillsConfig   `yaml:"skills"`
	Trust    TrustConfig    `yaml:"trust"`
	Server   ServerConfig   `yaml:"server"`
}

type GuideConfig struct {
	Name    string `yaml:"name"`
	DataDir string `yaml:"data_dir"`
}

type AIConfig struct {
	Default     string                    `yaml:"default"`
	Providers   map[string]ProviderConfig `yaml:"providers"`
	Interactive ModelSelection            `yaml:"interactive"`
	MCP         ModelSelection            `yaml:"mcp"`
}

type ProviderConfig struct {
	APIKeyEnv      string            `yaml:"api_key_env"`
	BaseURL        string            `yaml:"base_url"`
	Models         map[string]string `yaml:"models"`
	EmbeddingModel string            `yaml:"embedding_model"`
}

type ModelSelection struct {
	Model    string   `yaml:"model"`
	Fallback []string `yaml:"fallback"`
}

type ChannelsConfig struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Email    EmailConfig    `yaml:"email"`
	Calendar CalendarConfig `yaml:"calendar"`
	Slack    SlackConfig    `yaml:"slack"`
	JIRA     JIRAConfig     `yaml:"jira"`
	Drive    DriveConfig    `yaml:"drive"`
	Omi      OmiConfig      `yaml:"omi"`
}

type TelegramConfig struct {
	BotTokenEnv string `yaml:"bot_token_env"`
	ChatIDEnv   string `yaml:"chat_id_env"`
}

type EmailConfig struct {
	Accounts map[string]GoogleAccountConfig `yaml:"accounts"`
}

type GoogleAccountConfig struct {
	Provider        string `yaml:"provider"`
	Address         string `yaml:"address"`
	CredentialsPath string `yaml:"credentials_path"`
	Default         bool   `yaml:"default"`
	Bridge          string `yaml:"bridge"`
}

type CalendarConfig struct {
	Accounts map[string]GoogleAccountConfig `yaml:"accounts"`
}

type SlackConfig struct {
	Workspaces map[string]SlackWorkspaceConfig `yaml:"workspaces"`
}

type SlackWorkspaceConfig struct {
	TokenEnv      string   `yaml:"token_env"`
	TokenPath     string   `yaml:"token_path"`
	UserID        string   `yaml:"user_id"`
	WatchChannels []string `yaml:"watch_channels"`
	Bridge        string   `yaml:"bridge"`
}

type JIRAConfig struct {
	Instances map[string]JIRAInstanceConfig `yaml:"instances"`
}

type JIRAInstanceConfig struct {
	BaseURL     string `yaml:"base_url"`
	Email       string `yaml:"email"`
	APITokenEnv string `yaml:"api_token_env"`
	Bridge      string `yaml:"bridge"`
}

type DriveConfig struct {
	Accounts []string `yaml:"accounts"`
}

type OmiConfig struct {
	APIKeyEnv string `yaml:"api_key_env"`
}

type SkillsConfig struct {
	Dir string `yaml:"dir"`
}

type TrustConfig struct {
	AutoApproveThreshold int    `yaml:"auto_approve_threshold"`
	DefaultAction        string `yaml:"default_action"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Load reads a virgil.yaml from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.ExpandDataDir(); err != nil {
		return nil, err
	}

	cfg.loadEnv()
	return cfg, nil
}

// loadEnv loads <data_dir>/.env into the process environment.
// Existing env vars are not overridden, so this is safe to call even when
// env vars are already set by the shell.
func (c *Config) loadEnv() {
	envPath := filepath.Join(c.Guide.DataDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to read .env file", "path", envPath, "err", err)
		}
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

// Validate checks that required config fields are present and consistent.
func (c *Config) Validate() error {
	if c.Guide.DataDir == "" {
		return fmt.Errorf("guide.data_dir is required")
	}
	for name, ws := range c.Channels.Slack.Workspaces {
		if ws.TokenEnv == "" && ws.TokenPath == "" {
			return fmt.Errorf("slack workspace %q has neither token_env nor token_path", name)
		}
	}
	for _, acctName := range c.Channels.Drive.Accounts {
		if _, ok := c.Channels.Email.Accounts[acctName]; !ok {
			return fmt.Errorf("drive account %q not found in email accounts", acctName)
		}
	}
	return nil
}

// DBPath returns the full path to the SQLite database.
func (c *Config) DBPath() string {
	return filepath.Join(c.Guide.DataDir, "data.db")
}

// ConfigPath returns the path to the config file within the data dir.
func (c *Config) ConfigPath() string {
	return filepath.Join(c.Guide.DataDir, "virgil.yaml")
}

func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	return filepath.Join(home, path[2:]), nil
}

func (c *Config) ExpandDataDir() error {
	if c.Guide.DataDir == "" {
		c.Guide.DataDir = "~/.virgil"
	}
	var err error
	if c.Guide.DataDir, err = expandTilde(c.Guide.DataDir); err != nil {
		return err
	}

	// Expand ~ in all credential paths
	for name, acct := range c.Channels.Email.Accounts {
		if acct.CredentialsPath, err = expandTilde(acct.CredentialsPath); err != nil {
			return err
		}
		c.Channels.Email.Accounts[name] = acct
	}
	for name, acct := range c.Channels.Calendar.Accounts {
		if acct.CredentialsPath, err = expandTilde(acct.CredentialsPath); err != nil {
			return err
		}
		c.Channels.Calendar.Accounts[name] = acct
	}
	for name, ws := range c.Channels.Slack.Workspaces {
		if ws.TokenPath, err = expandTilde(ws.TokenPath); err != nil {
			return err
		}
		c.Channels.Slack.Workspaces[name] = ws
	}
	return nil
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Guide: GuideConfig{
			Name:    "Virgil",
			DataDir: "~/.virgil",
		},
		AI: AIConfig{
			Default: "anthropic",
			Providers: map[string]ProviderConfig{
				"anthropic": {
					APIKeyEnv: "ANTHROPIC_API_KEY",
					Models: map[string]string{
						"sonnet": "claude-sonnet-4-6",
						"haiku":  "claude-haiku-4-5-20251001",
						"opus":   "claude-opus-4-6",
					},
				},
				"openai": {
					APIKeyEnv:      "OPENAI_API_KEY",
					EmbeddingModel: "text-embedding-3-small",
					Models: map[string]string{
						"gpt4o":      "gpt-4o",
						"gpt4o-mini": "gpt-4o-mini",
					},
				},
			},
			Interactive: ModelSelection{
				Model:    "anthropic/sonnet",
				Fallback: []string{"openai/gpt4o"},
			},
			MCP: ModelSelection{
				Model:    "anthropic/haiku",
				Fallback: []string{"anthropic/sonnet"},
			},
		},
		Skills: SkillsConfig{
			Dir: "skills/",
		},
		Trust: TrustConfig{
			AutoApproveThreshold: 15,
			DefaultAction:        "ask",
		},
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
	}
}

// ModelFor returns the model selection for a given channel.
func (c *Config) ModelFor(channel string) ModelSelection {
	if channel == "mcp" && c.AI.MCP.Model != "" {
		return c.AI.MCP
	}
	return c.AI.Interactive
}

// ModelFromSkill returns the model selection for a skill, falling back to interactive defaults.
func (c *Config) ModelFromSkill(model string, fallback []string) ModelSelection {
	ms := ModelSelection{}
	if model != "" {
		ms.Model = model
	} else {
		ms.Model = c.AI.Interactive.Model
	}
	if len(fallback) > 0 {
		ms.Fallback = fallback
	} else if model == "" {
		ms.Fallback = c.AI.Interactive.Fallback
	}
	return ms
}

// WriteDefault writes the default config to the given path.
func WriteDefault(path string) error {
	cfg := Default()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
