package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/justinpbarnett/virgil/internal"
)

type frontmatter struct {
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
}

type skillConfig struct {
	Schedule string   `yaml:"schedule"`
	Model    string   `yaml:"model"`
	Fallback []string `yaml:"fallback"`
	Enabled  *bool    `yaml:"enabled"`
}

// LoadAll discovers and loads all skills from the given directory.
func LoadAll(dir string) ([]*internal.Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills dir %q: %w", dir, err)
	}

	var result []*internal.Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		s, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("load skill %q: %w", entry.Name(), err)
		}
		if s != nil {
			result = append(result, s)
		}
	}
	return result, nil
}

// Load reads a single skill from a folder.
func Load(path string) (*internal.Skill, error) {
	data, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	fm, body, err := parseFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md: %w", err)
	}

	s := &internal.Skill{
		Name:        filepath.Base(path),
		Description: fm.Description,
		Path:        path,
		Prompt:      body,
		Tools:       fm.Tools,
		Enabled:     true,
	}

	if cfgData, err := os.ReadFile(filepath.Join(path, "config.yaml")); err == nil {
		var cfg skillConfig
		if err := yaml.Unmarshal(cfgData, &cfg); err != nil {
			return nil, fmt.Errorf("parse config.yaml: %w", err)
		}
		s.Schedule = cfg.Schedule
		s.Model = cfg.Model
		s.Fallback = cfg.Fallback
		if cfg.Enabled != nil {
			s.Enabled = *cfg.Enabled
		}
	}

	if gotchas, err := os.ReadFile(filepath.Join(path, "gotchas.md")); err == nil {
		s.Gotchas = string(gotchas)
	}

	templatesDir := filepath.Join(path, "templates")
	if entries, err := os.ReadDir(templatesDir); err == nil {
		s.Templates = make(map[string]string)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			if content, err := os.ReadFile(filepath.Join(templatesDir, entry.Name())); err == nil {
				s.Templates[entry.Name()] = string(content)
			}
		}
	}

	return s, nil
}

func parseFrontmatter(content string) (frontmatter, string, error) {
	var fm frontmatter
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return fm, content, nil
	}

	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, content, nil
	}

	yamlStr := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:])

	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		return fm, "", fmt.Errorf("unmarshal frontmatter: %w", err)
	}

	return fm, body, nil
}
