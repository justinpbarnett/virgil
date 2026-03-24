package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"github.com/justinpbarnett/virgil/internal"
)

type OpenAIProvider struct {
	client         *openai.Client
	embeddingModel string
}

func NewOpenAIProvider(apiKeyEnv, baseURL, embeddingModel string) (*OpenAIProvider, error) {
	if apiKeyEnv == "" {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("env var %s is not set", apiKeyEnv)
	}

	opts := []openaiopt.RequestOption{openaiopt.WithAPIKey(key)}
	if baseURL != "" {
		opts = append(opts, openaiopt.WithBaseURL(baseURL))
	}
	client := openai.NewClient(opts...)

	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}
	return &OpenAIProvider{client: &client, embeddingModel: embeddingModel}, nil
}

func (p *OpenAIProvider) Complete(ctx context.Context, cfg ModelConfig, messages []Message, tools []*internal.Tool) (*Response, error) {
	params := openai.ChatCompletionNewParams{
		Model:    cfg.Model,
		Messages: convertMessagesOpenAI(messages),
	}

	if len(tools) > 0 {
		params.Tools = convertToolsOpenAI(tools)
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai complete: %w", err)
	}

	return parseOpenAIResponse(resp, cfg.Model)
}

func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: p.embeddingModel,
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: param.NewOpt(text),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openai embed: no embeddings returned")
	}

	// OpenAI returns float64, convert to float32
	f64 := resp.Data[0].Embedding
	result := make([]float32, len(f64))
	for i, v := range f64 {
		result[i] = float32(v)
	}
	return result, nil
}

func convertMessagesOpenAI(messages []Message) []openai.ChatCompletionMessageParamUnion {
	var out []openai.ChatCompletionMessageParamUnion
	for _, m := range messages {
		switch m.Role {
		case "system":
			out = append(out, openai.SystemMessage(m.Content))

		case "user":
			if len(m.ToolResults) > 0 {
				for _, tr := range m.ToolResults {
					out = append(out, openai.ToolMessage(tr.Content, tr.ToolCallID))
				}
				continue
			}
			out = append(out, openai.UserMessage(m.Content))

		case "assistant":
			if len(m.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallParam
				for _, tc := range m.ToolCalls {
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: InputToJSON(tc.Input),
						},
					})
				}
				msg := openai.ChatCompletionAssistantMessageParam{
					ToolCalls: toolCalls,
				}
				if m.Content != "" {
					msg.Content.OfString = openai.String(m.Content)
				}
				out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &msg})
				continue
			}
			out = append(out, openai.AssistantMessage(m.Content))
		}
	}
	return out
}

func convertToolsOpenAI(tools []*internal.Tool) []openai.ChatCompletionToolParam {
	out := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		out = append(out, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(t.Parameters),
			},
		})
	}
	return out
}

func parseOpenAIResponse(resp *openai.ChatCompletion, model string) (*Response, error) {
	r := &Response{
		ModelUsed: model,
		TokensIn:  int(resp.Usage.PromptTokens),
		TokensOut: int(resp.Usage.CompletionTokens),
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: response contained no choices")
	}

	choice := resp.Choices[0]
	r.StopReason = choice.FinishReason
	r.Text = choice.Message.Content

	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			input = map[string]any{"_raw": tc.Function.Arguments}
		}
		r.ToolCalls = append(r.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	if len(r.ToolCalls) > 0 && r.StopReason == "" {
		r.StopReason = "tool_use"
	}

	return r, nil
}
