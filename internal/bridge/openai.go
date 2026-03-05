package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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
