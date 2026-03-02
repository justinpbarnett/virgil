package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/justinpbarnett/virgil/internal/pipe"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig          `yaml:"server"`
	Provider     ProviderConfig        `yaml:"provider"`
	LogLevel     string                `yaml:"log_level"`
	DatabasePath string                `yaml:"database_path"`
	ConfigDir    string                `yaml:"-"`
	Pipes        map[string]PipeConfig `yaml:"-"`
	Vocabulary   VocabularyConfig      `yaml:"-"`
	Templates    TemplatesConfig       `yaml:"-"`
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
	Name        string                `yaml:"name"`
	Description string                `yaml:"description"`
	Category    string                `yaml:"category"`
	Streaming   bool                  `yaml:"streaming"`
	Timeout     string                `yaml:"timeout"`
	Triggers    pipe.Triggers        `yaml:"triggers"`
	Flags       map[string]pipe.Flag `yaml:"flags"`
	Prompts     PromptsConfig         `yaml:"prompts"`
	Vocabulary  VocabularyConfig      `yaml:"vocabulary"`
	Templates   TemplateContrib       `yaml:"templates"`
	Dir         string                `yaml:"-"`
}

type PromptsConfig struct {
	System    string            `yaml:"system"`
	Templates map[string]string `yaml:"templates"`
}

type TemplateContrib struct {
	Priority int             `yaml:"priority"`
	Entries  []TemplateEntry `yaml:"entries"`
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
	Requires []string   `yaml:"requires"`
	Plan     []PlanStep `yaml:"plan"`
}

type PlanStep struct {
	Pipe  string            `yaml:"pipe"`
	Flags map[string]string `yaml:"flags"`
}

func Load(configDir string, pipesDir string) (*Config, error) {
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

	// Resolve pipesDir to absolute once (avoids os.Getwd per pipe in filepath.Abs)
	absPipesDir, err := filepath.Abs(pipesDir)
	if err != nil {
		return nil, fmt.Errorf("resolving pipes directory: %w", err)
	}

	// Load pipe definitions from pipesDir/*/pipe.yaml
	entries, err := os.ReadDir(absPipesDir)
	if err != nil {
		return nil, fmt.Errorf("reading pipes directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pipeDir := filepath.Join(absPipesDir, entry.Name())
		pipeYAML := filepath.Join(pipeDir, "pipe.yaml")
		var pc PipeConfig
		if err := loadYAML(pipeYAML, &pc); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("loading pipe config %s: %w", entry.Name(), err)
		}
		pc.Dir = pipeDir
		cfg.Pipes[pc.Name] = pc
	}

	// Merge vocabulary from all pipes
	if err := mergeVocabulary(cfg); err != nil {
		return nil, err
	}

	// Merge templates from all pipes
	mergeTemplates(cfg)

	return cfg, nil
}

func mergeVocabulary(cfg *Config) error {
	cfg.Vocabulary = VocabularyConfig{
		Verbs:     make(map[string]string),
		Types:     make(map[string]string),
		Sources:   make(map[string]string),
		Modifiers: make(map[string]string),
	}

	merge := func(category string, target map[string]string, source map[string]string) error {
		for word, mapping := range source {
			if existing, ok := target[word]; ok {
				if existing != mapping {
					return fmt.Errorf("vocabulary conflict in %s: word %q mapped to %q and %q by different pipes", category, word, existing, mapping)
				}
				// Same mapping is fine (idempotent)
				continue
			}
			target[word] = mapping
		}
		return nil
	}

	for _, pc := range cfg.Pipes {
		categories := []struct {
			name   string
			target map[string]string
			source map[string]string
		}{
			{"verbs", cfg.Vocabulary.Verbs, pc.Vocabulary.Verbs},
			{"types", cfg.Vocabulary.Types, pc.Vocabulary.Types},
			{"sources", cfg.Vocabulary.Sources, pc.Vocabulary.Sources},
			{"modifiers", cfg.Vocabulary.Modifiers, pc.Vocabulary.Modifiers},
		}
		for _, c := range categories {
			if err := merge(c.name, c.target, c.source); err != nil {
				return err
			}
		}
	}

	return nil
}

func mergeTemplates(cfg *Config) {
	type prioritizedEntry struct {
		priority int
		pipeName string
		entry    TemplateEntry
	}

	var all []prioritizedEntry
	for _, pc := range cfg.Pipes {
		if len(pc.Templates.Entries) == 0 {
			continue
		}
		priority := pc.Templates.Priority
		if priority == 0 {
			priority = 50
		}
		for _, e := range pc.Templates.Entries {
			all = append(all, prioritizedEntry{
				priority: priority,
				pipeName: pc.Name,
				entry:    e,
			})
		}
	}

	// Sort: priority ascending → specificity descending (more requires first) → pipe name alphabetical
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].priority != all[j].priority {
			return all[i].priority < all[j].priority
		}
		if len(all[i].entry.Requires) != len(all[j].entry.Requires) {
			return len(all[i].entry.Requires) > len(all[j].entry.Requires)
		}
		return all[i].pipeName < all[j].pipeName
	})

	cfg.Templates.Templates = make([]TemplateEntry, len(all))
	for i, pe := range all {
		cfg.Templates.Templates[i] = pe.entry
	}
}

func loadYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

// UnmarshalPipeConfig parses YAML data into a PipeConfig.
func UnmarshalPipeConfig(data []byte, pc *PipeConfig) error {
	return yaml.Unmarshal(data, pc)
}

func (pc PipeConfig) ToDefinition() pipe.Definition {
	return pipe.Definition{
		Name:        pc.Name,
		Description: pc.Description,
		Category:    pc.Category,
		Triggers:    pc.Triggers,
		Flags:       pc.Flags,
	}
}

// HandlerPath returns the path to the pipe's executable binary.
func (pc PipeConfig) HandlerPath() string {
	return filepath.Join(pc.Dir, "run")
}

// TimeoutDuration parses the Timeout string as a time.Duration.
// Returns 30s if Timeout is empty or unparseable.
func (pc PipeConfig) TimeoutDuration() time.Duration {
	if pc.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(pc.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// UserDir returns the path to the user's virgil config directory (~/.config/virgil).
// This is where user-specific files like credentials and tokens are stored,
// independent of which config directory the server resolves for pipe definitions.
func UserDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "virgil")
	}
	return filepath.Join(home, ".config", "virgil")
}

// DataDir returns the path to the shared virgil data directory (~/.local/share/virgil).
func DataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".local", "share", "virgil")
	}
	return filepath.Join(home, ".local", "share", "virgil")
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
