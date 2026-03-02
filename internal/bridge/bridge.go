package bridge

import (
	"context"
	"fmt"
)

type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
}

type ProviderConfig struct {
	Name    string            `yaml:"name" json:"name"`
	Model   string            `yaml:"model" json:"model"`
	Options map[string]string `yaml:"options" json:"options"`
}

func NewProvider(config ProviderConfig) (Provider, error) {
	switch config.Name {
	case "claude":
		binary := "claude"
		if b, ok := config.Options["binary"]; ok && b != "" {
			binary = b
		}
		return NewClaudeProvider(config.Model, binary), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Name)
	}
}
