// Package pipe defines the public types for pipe definitions and handler signatures.
//
// These types describe what a pipe is and how it processes signals. During the
// current single-module phase, the concrete implementations remain in internal/pipe.
// When virgil-core becomes a standalone module, external code will import these
// types to build custom pipes.
package pipe

import (
	"context"

	pkgenv "github.com/justinpbarnett/virgil/pkg/envelope"
)

// Handler is a synchronous pipe handler: receives an envelope and flags,
// returns a result envelope.
type Handler func(input pkgenv.Envelope, flags map[string]string) pkgenv.Envelope

// StreamHandler is an async pipe handler that emits text chunks via a sink
// function during processing.
type StreamHandler func(ctx context.Context, input pkgenv.Envelope, flags map[string]string, sink func(chunk string)) pkgenv.Envelope

// Definition describes a pipe's metadata for routing and discovery.
type Definition struct {
	Name        string               `yaml:"name" json:"name"`
	Description string               `yaml:"description" json:"description"`
	Category    string               `yaml:"category" json:"category"`
	Triggers    Triggers             `yaml:"triggers" json:"triggers"`
	Flags       map[string]Flag      `yaml:"flags" json:"flags"`
	Vocabulary  DefinitionVocabulary `yaml:"-" json:"-"`
}

// DefinitionVocabulary holds vocabulary terms used for routing decisions.
type DefinitionVocabulary struct {
	Verbs     map[string][]string
	Sources   map[string][]string
	Types     map[string][]string
	Modifiers map[string][]string
}

// Triggers defines how a pipe is activated by user signals.
type Triggers struct {
	Exact    []string `yaml:"exact" json:"exact"`
	Keywords []string `yaml:"keywords" json:"keywords"`
	Patterns []string `yaml:"patterns" json:"patterns"`
}

// Flag describes a pipe argument.
type Flag struct {
	Description string   `yaml:"description" json:"description"`
	Values      []string `yaml:"values" json:"values"`
	Default     string   `yaml:"default" json:"default"`
	Required    bool     `yaml:"required" json:"required"`
}
