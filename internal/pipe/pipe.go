package pipe

import (
	"context"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

type Handler func(input envelope.Envelope, flags map[string]string) envelope.Envelope

type StreamHandler func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope

type Definition struct {
	Name        string               `yaml:"name" json:"name"`
	Description string               `yaml:"description" json:"description"`
	Category    string               `yaml:"category" json:"category"`
	Triggers    Triggers             `yaml:"triggers" json:"triggers"`
	Flags       map[string]Flag      `yaml:"flags" json:"flags"`
	Vocabulary  DefinitionVocabulary `yaml:"-" json:"-"`
}

type DefinitionVocabulary struct {
	Verbs     map[string]string
	Sources   map[string]string
	Types     map[string]string
	Modifiers map[string]string
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

