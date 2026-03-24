package bridge

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/justinpbarnett/virgil/internal"
)

type AnthropicProvider struct {
	client *anthropic.Client
}

func NewAnthropicProvider(apiKeyEnv string) (*AnthropicProvider, error) {
	if apiKeyEnv == "" {
		apiKeyEnv = "ANTHROPIC_API_KEY"
	}
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("env var %s is not set", apiKeyEnv)
	}
	client := anthropic.NewClient(anthropicopt.WithAPIKey(key))
	return &AnthropicProvider{client: &client}, nil
}

func (p *AnthropicProvider) Complete(ctx context.Context, cfg ModelConfig, messages []Message, tools []*internal.Tool) (*Response, error) {
	params := anthropic.MessageNewParams{
		Model:     cfg.Model,
		MaxTokens: 4096,
		Messages:  convertMessagesAnthropic(messages),
	}

	for _, m := range messages {
		if m.Role == "system" {
			params.System = []anthropic.TextBlockParam{{Text: m.Content}}
			break
		}
	}

	if len(tools) > 0 {
		params.Tools = convertToolsAnthropic(tools)
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic complete: %w", err)
	}

	return parseAnthropicResponse(resp, cfg.Model), nil
}

func (p *AnthropicProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("anthropic does not support embeddings, use OpenAI provider")
}

func convertMessagesAnthropic(messages []Message) []anthropic.MessageParam {
	var out []anthropic.MessageParam
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}

		if m.Role == "user" && len(m.ToolResults) > 0 {
			var blocks []anthropic.ContentBlockParamUnion
			for _, tr := range m.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
			}
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: blocks,
			})
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name))
			}
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: blocks,
			})
			continue
		}

		role := anthropic.MessageParamRoleUser
		if m.Role == "assistant" {
			role = anthropic.MessageParamRoleAssistant
		}
		out = append(out, anthropic.MessageParam{
			Role:    role,
			Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)},
		})
	}
	return out
}

func convertToolsAnthropic(tools []*internal.Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		props, _ := t.Parameters["properties"]

		var required []string
		switch r := t.Parameters["required"].(type) {
		case []string:
			required = r
		case []any:
			for _, v := range r {
				if s, ok := v.(string); ok {
					required = append(required, s)
				}
			}
		}

		schema := anthropic.ToolInputSchemaParam{
			Properties: props,
			Required:   required,
		}

		tp := anthropic.ToolUnionParamOfTool(schema, t.Name)
		tp.OfTool.Description = anthropic.String(t.Description)
		out = append(out, tp)
	}
	return out
}

func parseAnthropicResponse(resp *anthropic.Message, model string) *Response {
	r := &Response{
		StopReason: string(resp.StopReason),
		ModelUsed:  model,
		TokensIn:   int(resp.Usage.InputTokens),
		TokensOut:  int(resp.Usage.OutputTokens),
	}

	var textParts []string
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: InputToMap(block.Input),
			})
		}
	}
	r.Text = strings.Join(textParts, "")

	return r
}
