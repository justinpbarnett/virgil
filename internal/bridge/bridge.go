package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go"
)

type Bridge interface {
	Complete(ctx context.Context, cfg ModelConfig, messages []Message, tools []*internal.Tool) (*Response, error)
	Embed(ctx context.Context, text string) ([]float32, error)
}

type ModelConfig struct {
	Provider string
	Model    string
}

type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

type Response struct {
	Text       string     `json:"text"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason string     `json:"stop_reason"`
	ModelUsed  string     `json:"model_used"`
	TokensIn   int        `json:"tokens_in"`
	TokensOut  int        `json:"tokens_out"`
}

func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

func ParseModelRef(ref string) (provider, model string) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return ref, ""
}

func ResolveModel(cfg *config.Config, provider, friendlyName string) string {
	if p, ok := cfg.AI.Providers[provider]; ok {
		if m, ok := p.Models[friendlyName]; ok {
			return m
		}
	}
	return friendlyName
}

func NewModelConfig(cfg *config.Config, ref string) ModelConfig {
	provider, model := ParseModelRef(ref)
	return ModelConfig{
		Provider: provider,
		Model:    ResolveModel(cfg, provider, model),
	}
}

func InputToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func InputToJSON(input map[string]any) string {
	if input == nil {
		return "{}"
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode == http.StatusTooManyRequests ||
			anthropicErr.StatusCode == http.StatusRequestTimeout ||
			anthropicErr.StatusCode >= http.StatusInternalServerError
	}

	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return openaiErr.StatusCode == http.StatusTooManyRequests ||
			openaiErr.StatusCode == http.StatusRequestTimeout ||
			openaiErr.StatusCode >= http.StatusInternalServerError
	}

	return false
}

var ErrNoProviders = fmt.Errorf("no AI providers configured")
