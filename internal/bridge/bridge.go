package bridge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
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
	case "gemini":
		return GeminiProvider(config)
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Name)
	}
}
