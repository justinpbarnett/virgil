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

type openaiProvider struct {
	apiKey       string
	model        string
	baseURL      string
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
	return &openaiProvider{
		apiKey:       key,
		model:        model,
		baseURL:      baseURL,
		maxTokens:    maxTokens,
		providerName: cfg.Name,
		logger:       logger,
	}, nil
}

func (p *openaiProvider) Complete(ctx context.Context, system, user string) (string, error) {
	p.logger.Info("provider called", "provider", p.providerName, "model", p.model, "streaming", false)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user, false)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	p.setHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return "", err
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding openai response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from openai")
	}
	model := result.Model
	if model == "" {
		model = p.model
	}
	p.lastUsage = Usage{
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
		Model:        model,
		Cost:         CostFor(model, result.Usage.PromptTokens, result.Usage.CompletionTokens),
	}
	text := result.Choices[0].Message.Content
	p.logger.Info("provider responded", "bytes", len(text))
	return text, nil
}

func (p *openaiProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(string)) (string, error) {
	p.logger.Info("provider called", "provider", p.providerName, "model", p.model, "streaming", true)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user, true)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	p.setHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return "", err
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
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
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			// usage appears in the final chunk when stream_options.include_usage is true
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if event.Usage != nil {
			model := event.Model
			if model == "" {
				model = p.model
			}
			p.lastUsage = Usage{
				InputTokens:  event.Usage.PromptTokens,
				OutputTokens: event.Usage.CompletionTokens,
				Model:        model,
				Cost:         CostFor(model, event.Usage.PromptTokens, event.Usage.CompletionTokens),
			}
		}
		if len(event.Choices) > 0 {
			chunk := event.Choices[0].Delta.Content
			if chunk != "" {
				full.WriteString(chunk)
				onChunk(chunk)
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

func (p *openaiProvider) buildRequest(system, user string, stream bool) ([]byte, error) {
	messages := []map[string]string{}
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": user})

	reqBody := map[string]any{
		"model":      p.model,
		"max_tokens": p.maxTokens,
		"messages":   messages,
	}
	if stream {
		reqBody["stream"] = true
		reqBody["stream_options"] = map[string]bool{"include_usage": true}
	}
	return json.Marshal(reqBody)
}

func (p *openaiProvider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("content-type", "application/json")
}
