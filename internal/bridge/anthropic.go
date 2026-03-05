package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type anthropicProvider struct {
	apiKey    string
	model     string
	baseURL   string
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
	return &anthropicProvider{
		apiKey:    key,
		model:     model,
		baseURL:   "https://api.anthropic.com",
		maxTokens: maxTokens,
		logger:    logger,
	}, nil
}

func (p *anthropicProvider) Complete(ctx context.Context, system, user string) (string, error) {
	p.logger.Info("provider called", "provider", "anthropic", "model", p.model, "streaming", false)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user, false)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	p.setHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding anthropic response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from anthropic")
	}
	model := result.Model
	if model == "" {
		model = p.model
	}
	p.lastUsage = Usage{
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		Model:        model,
		Cost:         CostFor(model, result.Usage.InputTokens, result.Usage.OutputTokens),
	}
	text := result.Content[0].Text
	p.logger.Info("provider responded", "bytes", len(text))
	return text, nil
}

func (p *anthropicProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(string)) (string, error) {
	p.logger.Info("provider called", "provider", "anthropic", "model", p.model, "streaming", true)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user, true)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	p.setHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return "", err
	}

	var full strings.Builder
	var inputTokens, outputTokens int
	streamModel := p.model
	scanner := bufio.NewScanner(resp.Body)
outer:
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			// message_start carries input usage and model
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			// message_delta carries final output token count
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "message_start":
			inputTokens = event.Message.Usage.InputTokens
			if event.Message.Model != "" {
				streamModel = event.Message.Model
			}
		case "message_delta":
			outputTokens = event.Usage.OutputTokens
		case "message_stop":
			p.lastUsage = Usage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				Model:        streamModel,
				Cost:         CostFor(streamModel, inputTokens, outputTokens),
			}
			break outer
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				full.WriteString(event.Delta.Text)
				onChunk(event.Delta.Text)
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		result := full.String()
		return result, fmt.Errorf("reading stream: %w", err)
	}

	result := full.String()
	p.logger.Info("provider responded", "bytes", len(result))
	return result, nil
}

func (p *anthropicProvider) buildRequest(system, user string, stream bool) ([]byte, error) {
	reqBody := map[string]any{
		"model":      p.model,
		"max_tokens": p.maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	if system != "" {
		reqBody["system"] = system
	}
	if stream {
		reqBody["stream"] = true
	}
	return json.Marshal(reqBody)
}

func (p *anthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
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

