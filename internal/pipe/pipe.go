package pipe

import (
	"github.com/justinpbarnett/virgil/internal/envelope"
)

type Handler func(input envelope.Envelope, flags map[string]string) envelope.Envelope

type Definition struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description"`
	Category    string         `yaml:"category" json:"category"`
	Triggers    Triggers       `yaml:"triggers" json:"triggers"`
	Flags       map[string]Flag `yaml:"flags" json:"flags"`
	Provider    *ProviderConfig `yaml:"provider,omitempty" json:"provider,omitempty"`
}

type Triggers struct {
	Exact    []string `yaml:"exact" json:"exact"`
	Keywords []string `yaml:"keywords" json:"keywords"`
	Patterns []string `yaml:"patterns" json:"patterns"`
}

type Flag struct {
	Description string   `yaml:"description" json:"description"`
	Values      []string `yaml:"values" json:"values"`
	Default     string   `yaml:"default" json:"default"`
	Required    bool     `yaml:"required" json:"required"`
}

type ProviderConfig struct {
	Name    string            `yaml:"name" json:"name"`
	Model   string            `yaml:"model" json:"model"`
	Options map[string]string `yaml:"options" json:"options"`
}
