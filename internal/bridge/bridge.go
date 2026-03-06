package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Role constants for AgenticMessage.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
}

// Tool defines a capability the model can invoke during an agentic loop.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema object describing the input
	Execute     func(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolCall is a single tool invocation returned by the model.
type ToolCall struct {
	ID    string // provider-assigned call ID, echoed in the result
	Name  string
	Input json.RawMessage
}

// AgenticResponse is the result of one turn in an agentic loop.
// Either Text is set (model is done) or ToolCalls is set (model wants tools executed).
type AgenticResponse struct {
	Text      string
	ToolCalls []ToolCall
}

// AgenticMessage is a single entry in the conversation history.
type AgenticMessage struct {
	Role        string // "user", "assistant"
	Content     string
	ToolCalls   []ToolCall   // populated for assistant turns that requested tools
	ToolResults []ToolResult // populated for user turns responding to tool calls
}

// ToolResult carries the output of an executed tool call.
type ToolResult struct {
	CallID  string
	Name    string // tool name, needed by Gemini's functionResponse
	Content string
	IsError bool
}

// AgenticProvider extends Provider with tool use support.
type AgenticProvider interface {
	Provider
	// CompleteWithTools sends one turn with the current message history and
	// available tools. Returns either a final text response or tool call
	// requests. The caller manages the loop.
	CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error)
}

type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error)
}

// UsageReporter is an optional interface for providers that track token usage.
// After each Complete or CompleteStream call, LastUsage returns the token counts
// and computed cost for that call. Providers that do not implement this interface
// contribute zero cost.
type UsageReporter interface {
	LastUsage() Usage
}

type ProviderConfig struct {
	Name      string       `yaml:"name" json:"name"`
	Model     string       `yaml:"model" json:"model"`
	Binary    string       `yaml:"binary" json:"binary"`
	MaxTurns  int          `yaml:"max_turns" json:"max_turns"`
	MaxTokens int          `yaml:"-" json:"-"`
	Verbose   bool         `yaml:"-" json:"-"`
	Logger    *slog.Logger `yaml:"-" json:"-"`
	BaseURL   string       `yaml:"-" json:"-"` // optional override, used in tests
	NoRetry   bool         `yaml:"-" json:"-"` // disable SDK-level retries, used in tests
}

// httpClient is shared by all HTTP-based providers with a reasonable timeout.
var httpClient = &http.Client{Timeout: 120 * time.Second}

// checkStatus returns an error for non-2xx HTTP responses, capping the error
// body read to 4 KB to prevent unbounded memory allocation.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 429 {
		retryAfter := resp.Header.Get("retry-after")
		if retryAfter != "" {
			return fmt.Errorf("rate limited (429): retry after %s", retryAfter)
		}
		return fmt.Errorf("rate limited (429): %s", strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// resolveDefaults returns shared defaults for HTTP-based provider constructors.
func resolveDefaults(cfg ProviderConfig, defaultModel string) (model string, maxTokens int, logger *slog.Logger) {
	model = cfg.Model
	if model == "" {
		model = defaultModel
	}
	maxTokens = cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	logger = cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return
}

// buildAgenticResponse assembles an AgenticResponse from parsed tool calls and
// text, shared by all provider CompleteWithTools implementations.
func buildAgenticResponse(toolCalls []ToolCall, text, providerName string, logger *slog.Logger) (AgenticResponse, error) {
	if len(toolCalls) > 0 {
		return AgenticResponse{ToolCalls: toolCalls}, nil
	}
	if text == "" {
		return AgenticResponse{}, fmt.Errorf("empty response from %s", providerName)
	}
	logger.Info("provider responded", "bytes", len(text))
	return AgenticResponse{Text: text}, nil
}

func CreateProvider(config ProviderConfig) (Provider, error) {
	switch config.Name {
	case "claude":
		return ClaudeProvider(config), nil
	case "anthropic":
		return AnthropicProvider(config)
	case "openai":
		return OpenAIProvider(config, "https://api.openai.com/v1", "OPENAI_API_KEY")
	case "xai":
		return OpenAIProvider(config, "https://api.x.ai/v1", "XAI_API_KEY")
	case "zen":
		return OpenAIProvider(config, "https://opencode.ai/zen/v1", "ZEN_API_KEY")
	case "gemini":
		return GeminiProvider(config)
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Name)
	}
}
