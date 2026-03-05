package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client    anthropic.Client
	model     string
	maxTokens int
	logger    *slog.Logger
	lastUsage Usage
}

func (p *anthropicProvider) LastUsage() Usage {
	return p.lastUsage
}

func AnthropicProvider(cfg ProviderConfig) (*anthropicProvider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	model, maxTokens, logger := resolveDefaults(cfg, "")
	model = resolveAnthropicModel(model)

	opts := []option.RequestOption{option.WithAPIKey(key)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.NoRetry {
		opts = append(opts, option.WithMaxRetries(0))
	}

	return &anthropicProvider{
		client:    anthropic.NewClient(opts...),
		model:     model,
		maxTokens: maxTokens,
		logger:    logger,
	}, nil
}

func (p *anthropicProvider) buildParams(system, user string) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(p.maxTokens),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	return params
}

func (p *anthropicProvider) Complete(ctx context.Context, system, user string) (string, error) {
	p.logger.Info("provider called", "provider", "anthropic", "model", p.model, "streaming", false)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	msg, err := p.client.Messages.New(ctx, p.buildParams(system, user))
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}

	model := string(msg.Model)
	if model == "" {
		model = p.model
	}
	p.lastUsage = Usage{
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
		Model:        model,
		Cost:         CostFor(model, int(msg.Usage.InputTokens), int(msg.Usage.OutputTokens)),
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	text := sb.String()
	if text == "" {
		return "", fmt.Errorf("empty response from anthropic")
	}
	p.logger.Info("provider responded", "bytes", len(text))
	return text, nil
}

func (p *anthropicProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(string)) (string, error) {
	p.logger.Info("provider called", "provider", "anthropic", "model", p.model, "streaming", true)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	stream := p.client.Messages.NewStreaming(ctx, p.buildParams(system, user))
	defer stream.Close()

	var full strings.Builder
	var inputTokens, outputTokens int
	streamModel := p.model

	for stream.Next() {
		event := stream.Current()
		switch e := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			inputTokens = int(e.Message.Usage.InputTokens)
			if string(e.Message.Model) != "" {
				streamModel = string(e.Message.Model)
			}
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
				full.WriteString(delta.Text)
				onChunk(delta.Text)
			}
		case anthropic.MessageDeltaEvent:
			outputTokens = int(e.Usage.OutputTokens)
			p.lastUsage = Usage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				Model:        streamModel,
				Cost:         CostFor(streamModel, inputTokens, outputTokens),
			}
		}
	}
	if err := stream.Err(); err != nil {
		result := full.String()
		return result, fmt.Errorf("reading stream: %w", err)
	}

	result := full.String()
	p.logger.Info("provider responded", "bytes", len(result))
	return result, nil
}

// resolveAnthropicModel maps CLI shorthands to full Anthropic model IDs.
func resolveAnthropicModel(model string) string {
	switch model {
	case "", "sonnet":
		return "claude-sonnet-4-20250514"
	case "haiku":
		return "claude-haiku-4-5-20251001"
	case "opus":
		return "claude-opus-4-6"
	default:
		return model
	}
}
