package bridge

import (
	"context"
	"fmt"
	"log/slog"
)

type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
}

type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error)
}

type ProviderConfig struct {
	Name     string       `yaml:"name" json:"name"`
	Model    string       `yaml:"model" json:"model"`
	Binary   string       `yaml:"binary" json:"binary"`
	MaxTurns int          `yaml:"max_turns" json:"max_turns"`
	Verbose  bool         `yaml:"-" json:"-"`
	Logger   *slog.Logger `yaml:"-" json:"-"`
}

func NewProvider(config ProviderConfig) (Provider, error) {
	switch config.Name {
	case "claude":
		return NewClaudeProvider(config), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Name)
	}
}
