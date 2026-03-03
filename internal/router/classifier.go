package router

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

const classifySystemPrefix = `You are a signal router. Given a user's signal and a list of available pipes, respond with ONLY the pipe name that best matches the user's intent. If no pipe is a good match, respond with "chat".

Available pipes:
`

type Classifier struct {
	provider  bridge.Provider
	system    string
	pipeNames map[string]bool
	logger    *slog.Logger
}

func NewClassifier(provider bridge.Provider, defs []pipe.Definition, logger *slog.Logger) *Classifier {
	if logger == nil {
		logger = slog.Default()
	}

	var lines []string
	pipeNames := make(map[string]bool)

	for _, def := range defs {
		if def.Name == "chat" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", def.Name, def.Description))
		pipeNames[def.Name] = true
	}

	return &Classifier{
		provider:  provider,
		system:    classifySystemPrefix + strings.Join(lines, "\n"),
		pipeNames: pipeNames,
		logger:    logger,
	}
}

func (c *Classifier) Classify(ctx context.Context, signal string) (string, float64) {
	response, err := c.provider.Complete(ctx, c.system, signal)
	if err != nil {
		c.logger.Warn("classifier error", "error", err)
		return "chat", 0.0
	}

	choice := strings.ToLower(strings.TrimSpace(response))

	if c.pipeNames[choice] {
		return choice, 0.7
	}

	return "chat", 0.0
}
