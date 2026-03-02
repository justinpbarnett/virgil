package bridge

import (
	"context"
	"fmt"
)

type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
}

type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error)
}

type ProviderConfig struct {
	Name   string `yaml:"name" json:"name"`
	Model  string `yaml:"model" json:"model"`
	Binary string `yaml:"binary" json:"binary"`
}

func NewProvider(config ProviderConfig) (Provider, error) {
	switch config.Name {
	case "claude":
		binary := config.Binary
		if binary == "" {
			binary = "claude"
		}
		return NewClaudeProvider(config.Model, binary), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Name)
	}
}
