package bridge

import (
	"context"
	"encoding/json"
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

func (p *anthropicProvider) CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error) {
	p.logger.Info("provider called", "provider", "anthropic", "model", p.model, "agentic", true, "tools", len(tools))

	// Build tool params.
	anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		_ = json.Unmarshal(t.InputSchema, &schema)
		tp := anthropic.ToolParam{
			Name: t.Name,
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: schema.Properties,
				Required:   schema.Required,
			},
		}
		if t.Description != "" {
			tp.Description = anthropic.String(t.Description)
		}
		anthropicTools[i] = anthropic.ToolUnionParam{OfTool: &tp}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(p.maxTokens),
		Tools:     anthropicTools,
		Messages:  buildAnthropicMessages(messages),
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("anthropic request: %w", err)
	}

	var toolCalls []ToolCall
	var textBuilder strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			textBuilder.WriteString(b.Text)
		case anthropic.ToolUseBlock:
			toolCalls = append(toolCalls, ToolCall{
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}

	return buildAgenticResponse(toolCalls, textBuilder.String(), "anthropic", p.logger)
}

func buildAnthropicMessages(messages []AgenticMessage) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			if len(m.ToolResults) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, len(m.ToolResults))
				for i, tr := range m.ToolResults {
					blocks[i] = anthropic.NewToolResultBlock(tr.CallID, tr.Content, tr.IsError)
				}
				result = append(result, anthropic.NewUserMessage(blocks...))
			} else {
				result = append(result, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					var input any
					_ = json.Unmarshal(tc.Input, &input)
					blocks[i] = anthropic.NewToolUseBlock(tc.ID, input, tc.Name)
				}
				result = append(result, anthropic.NewAssistantMessage(blocks...))
			} else {
				result = append(result, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
			}
		}
	}
	return result
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
