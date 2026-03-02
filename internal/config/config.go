package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/justinpbarnett/virgil/internal/pipe"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig              `yaml:"server"`
	Provider     ProviderConfig            `yaml:"provider"`
	LogLevel     string                    `yaml:"log_level"`
	DatabasePath string                    `yaml:"database_path"`
	ConfigDir    string                    `yaml:"-"`
	Pipes        map[string]PipeConfig     `yaml:"-"`
	Vocabulary   VocabularyConfig          `yaml:"-"`
	Templates    TemplatesConfig           `yaml:"-"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type ProviderConfig struct {
	Name   string `yaml:"name"`
	Model  string `yaml:"model"`
	Binary string `yaml:"binary"`
}

type PipeConfig struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Category    string              `yaml:"category"`
	Triggers    TriggersConfig      `yaml:"triggers"`
	Flags       map[string]FlagConfig `yaml:"flags"`
	Prompts     PromptsConfig       `yaml:"prompts"`
}

type TriggersConfig struct {
	Exact    []string `yaml:"exact"`
	Keywords []string `yaml:"keywords"`
	Patterns []string `yaml:"patterns"`
}

type FlagConfig struct {
	Description string   `yaml:"description"`
	Values      []string `yaml:"values"`
	Default     string   `yaml:"default"`
	Required    bool     `yaml:"required"`
}

type PromptsConfig struct {
	System    string            `yaml:"system"`
	Templates map[string]string `yaml:"templates"`
}

type VocabularyConfig struct {
	Verbs     map[string]string `yaml:"verbs"`
	Types     map[string]string `yaml:"types"`
	Sources   map[string]string `yaml:"sources"`
	Modifiers map[string]string `yaml:"modifiers"`
}

type TemplatesConfig struct {
	Templates []TemplateEntry `yaml:"templates"`
}

type TemplateEntry struct {
	Requires []string       `yaml:"requires"`
	Plan     []PlanStep     `yaml:"plan"`
}

type PlanStep struct {
	Pipe  string            `yaml:"pipe"`
	Flags map[string]string `yaml:"flags"`
}

func Load(configDir string) (*Config, error) {
	cfg := &Config{
		ConfigDir: configDir,
		Server:    ServerConfig{Host: "localhost", Port: 7890},
		Provider:  ProviderConfig{Name: "claude", Model: "sonnet", Binary: "claude"},
		LogLevel:  "info",
		Pipes:     make(map[string]PipeConfig),
	}

	// Load virgil.yaml
	if err := loadYAML(filepath.Join(configDir, "virgil.yaml"), cfg); err != nil {
		return nil, fmt.Errorf("loading virgil.yaml: %w", err)
	}

	// Expand ~ in database path
	if cfg.DatabasePath != "" {
		cfg.DatabasePath = expandHome(cfg.DatabasePath)
	}

	// Load vocabulary.yaml
	vocabPath := filepath.Join(configDir, "vocabulary.yaml")
	if err := loadYAML(vocabPath, &cfg.Vocabulary); err != nil {
		return nil, fmt.Errorf("loading vocabulary.yaml: %w", err)
	}

	// Load templates.yaml
	tmplPath := filepath.Join(configDir, "templates.yaml")
	if err := loadYAML(tmplPath, &cfg.Templates); err != nil {
		return nil, fmt.Errorf("loading templates.yaml: %w", err)
	}

	// Load pipe definitions from pipes/ subdirectory
	pipesDir := filepath.Join(configDir, "pipes")
	entries, err := os.ReadDir(pipesDir)
	if err != nil {
		return nil, fmt.Errorf("reading pipes directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		var pc PipeConfig
		if err := loadYAML(filepath.Join(pipesDir, entry.Name()), &pc); err != nil {
			return nil, fmt.Errorf("loading pipe config %s: %w", entry.Name(), err)
		}
		cfg.Pipes[pc.Name] = pc
	}

	return cfg, nil
}

func loadYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

func (pc PipeConfig) ToDefinition() pipe.Definition {
	return pipe.Definition{
		Name:        pc.Name,
		Description: pc.Description,
		Category:    pc.Category,
		Triggers: pipe.Triggers{
			Exact:    pc.Triggers.Exact,
			Keywords: pc.Triggers.Keywords,
			Patterns: pc.Triggers.Patterns,
		},
	}
}

func expandHome(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
