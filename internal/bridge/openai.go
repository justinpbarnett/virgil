package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type openaiProvider struct {
	client       *openai.Client
	model        string
	maxTokens    int
	providerName string
	logger       *slog.Logger
	lastUsage    Usage
}

func (p *openaiProvider) LastUsage() Usage {
	return p.lastUsage
}

func OpenAIProvider(cfg ProviderConfig, baseURL string, keyEnvVar string) (*openaiProvider, error) {
	key := os.Getenv(keyEnvVar)
	if key == "" {
		return nil, fmt.Errorf("%s not set", keyEnvVar)
	}
	model, maxTokens, logger := resolveDefaults(cfg, "gpt-4o")

	// cfg.BaseURL takes precedence (used in tests), then the baseURL argument.
	effectiveBaseURL := cfg.BaseURL
	if effectiveBaseURL == "" {
		effectiveBaseURL = baseURL
	}

	client := openai.NewClient(
		option.WithAPIKey(key),
		option.WithBaseURL(effectiveBaseURL),
	)

	return &openaiProvider{
		client:       &client,
		model:        model,
		maxTokens:    maxTokens,
		providerName: cfg.Name,
		logger:       logger,
	}, nil
}

func (p *openaiProvider) buildMessages(system, user string) []openai.ChatCompletionMessageParamUnion {
	messages := []openai.ChatCompletionMessageParamUnion{}
	if system != "" {
		messages = append(messages, openai.SystemMessage(system))
	}
	messages = append(messages, openai.UserMessage(user))
	return messages
}

func (p *openaiProvider) Complete(ctx context.Context, system, user string) (string, error) {
	p.logger.Info("provider called", "provider", p.providerName, "model", p.model, "streaming", false)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	completion, err := p.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:     openai.ChatModel(p.model),
		MaxTokens: openai.Int(int64(p.maxTokens)),
		Messages:  p.buildMessages(system, user),
	})
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("empty response from openai")
	}

	model := completion.Model
	if model == "" {
		model = p.model
	}
	p.lastUsage = Usage{
		InputTokens:  int(completion.Usage.PromptTokens),
		OutputTokens: int(completion.Usage.CompletionTokens),
		Model:        model,
		Cost:         CostFor(model, int(completion.Usage.PromptTokens), int(completion.Usage.CompletionTokens)),
	}

	text := completion.Choices[0].Message.Content
	p.logger.Info("provider responded", "bytes", len(text))
	return text, nil
}

func (p *openaiProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(string)) (string, error) {
	p.logger.Info("provider called", "provider", p.providerName, "model", p.model, "streaming", true)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	stream := p.client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:         openai.ChatModel(p.model),
		MaxTokens:     openai.Int(int64(p.maxTokens)),
		Messages:      p.buildMessages(system, user),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	})
	defer stream.Close()

	var full strings.Builder
	acc := openai.ChatCompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta != "" {
				full.WriteString(delta)
				onChunk(delta)
			}
		}
	}
	if err := stream.Err(); err != nil {
		result := full.String()
		return result, fmt.Errorf("reading stream: %w", err)
	}

	model := acc.ChatCompletion.Model
	if model == "" {
		model = p.model
	}
	p.lastUsage = Usage{
		InputTokens:  int(acc.Usage.PromptTokens),
		OutputTokens: int(acc.Usage.CompletionTokens),
		Model:        model,
		Cost:         CostFor(model, int(acc.Usage.PromptTokens), int(acc.Usage.CompletionTokens)),
	}

	result := full.String()
	p.logger.Info("provider responded", "bytes", len(result))
	return result, nil
}

func (p *openaiProvider) CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error) {
	p.logger.Info("provider called", "provider", p.providerName, "model", p.model, "agentic", true, "tools", len(tools))

	// Build tool params.
	openaiTools := make([]openai.ChatCompletionToolParam, len(tools))
	for i, t := range tools {
		var params shared.FunctionParameters
		_ = json.Unmarshal(t.InputSchema, &params)
		openaiTools[i] = openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  params,
			},
		}
	}

	completion, err := p.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:     openai.ChatModel(p.model),
		MaxTokens: openai.Int(int64(p.maxTokens)),
		Messages:  buildOpenAIMessages(system, messages),
		Tools:     openaiTools,
	})
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("openai request: %w", err)
	}
	if len(completion.Choices) == 0 {
		return AgenticResponse{}, fmt.Errorf("empty response from openai")
	}

	choice := completion.Choices[0]
	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}
	return buildAgenticResponse(toolCalls, choice.Message.Content, p.providerName, p.logger)
}

func buildOpenAIMessages(system string, messages []AgenticMessage) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion
	if system != "" {
		result = append(result, openai.SystemMessage(system))
	}
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			if len(m.ToolResults) > 0 {
				for _, tr := range m.ToolResults {
					content := tr.Content
					if tr.IsError {
						content = "Error: " + content
					}
					result = append(result, openai.ToolMessage(content, tr.CallID))
				}
			} else {
				result = append(result, openai.UserMessage(m.Content))
			}
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var assistant openai.ChatCompletionAssistantMessageParam
				assistant.ToolCalls = make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					assistant.ToolCalls[i] = openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(tc.Input),
						},
					}
				}
				result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
			} else {
				result = append(result, openai.AssistantMessage(m.Content))
			}
		}
	}
	return result
}
