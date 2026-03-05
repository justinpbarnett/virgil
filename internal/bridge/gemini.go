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

type geminiProvider struct {
	apiKey    string
	model     string
	baseURL   string
	maxTokens int
	logger    *slog.Logger
	lastUsage Usage
}

func (p *geminiProvider) LastUsage() Usage {
	return p.lastUsage
}

func GeminiProvider(cfg ProviderConfig) (*geminiProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY not set")
	}
	model, maxTokens, logger := resolveDefaults(cfg, "gemini-2.0-flash")
	return &geminiProvider{
		apiKey:    key,
		model:     model,
		baseURL:   "https://generativelanguage.googleapis.com/v1beta",
		maxTokens: maxTokens,
		logger:    logger,
	}, nil
}

func (p *geminiProvider) Complete(ctx context.Context, system, user string) (string, error) {
	p.logger.Info("provider called", "provider", "gemini", "model", p.model, "streaming", false)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.baseURL, p.model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return "", err
	}

	text, usage, err := parseGeminiResponse(resp.Body, p.model)
	if err != nil {
		return "", err
	}
	p.lastUsage = usage
	p.logger.Info("provider responded", "bytes", len(text))
	return text, nil
}

func (p *geminiProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(string)) (string, error) {
	p.logger.Info("provider called", "provider", "gemini", "model", p.model, "streaming", true)
	p.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))

	body, err := p.buildRequest(system, user)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse&key=%s", p.baseURL, p.model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
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

		chunk, usage := extractGeminiChunk([]byte(data), p.model)
		if usage.InputTokens > 0 || usage.OutputTokens > 0 {
			p.lastUsage = usage
		}
		if chunk == "" {
			continue
		}
		full.WriteString(chunk)
		onChunk(chunk)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		result := full.String()
		return result, fmt.Errorf("reading stream: %w", err)
	}

	result := full.String()
	p.logger.Info("provider responded", "bytes", len(result))
	return result, nil
}

func (p *geminiProvider) buildRequest(system, user string) ([]byte, error) {
	reqBody := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": user}}},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": p.maxTokens,
		},
	}
	if system != "" {
		reqBody["system_instruction"] = map[string]any{
			"parts": []map[string]string{{"text": system}},
		}
	}
	return json.Marshal(reqBody)
}

func parseGeminiResponse(r io.Reader, model string) (string, Usage, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", Usage{}, fmt.Errorf("reading gemini response: %w", err)
	}
	text, usage := extractGeminiChunk(data, model)
	if text == "" {
		return "", usage, fmt.Errorf("empty response from gemini")
	}
	return text, usage, nil
}

func extractGeminiChunk(data []byte, model string) (string, Usage) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", Usage{}
	}
	var text string
	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		text = result.Candidates[0].Content.Parts[0].Text
	}
	usage := Usage{
		InputTokens:  result.UsageMetadata.PromptTokenCount,
		OutputTokens: result.UsageMetadata.CandidatesTokenCount,
		Model:        model,
		Cost:         CostFor(model, result.UsageMetadata.PromptTokenCount, result.UsageMetadata.CandidatesTokenCount),
	}
	return text, usage
}
