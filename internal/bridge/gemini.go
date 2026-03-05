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
	model, maxTokens, logger := resolveDefaults(cfg, "gemini-3-flash-preview")
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

func (p *geminiProvider) CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error) {
	p.logger.Info("provider called", "provider", "gemini", "model", p.model, "agentic", true, "tools", len(tools))

	// Build function declarations.
	funcDecls := make([]map[string]any, len(tools))
	for i, t := range tools {
		var schema map[string]any
		_ = json.Unmarshal(t.InputSchema, &schema)
		funcDecls[i] = map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  schema,
		}
	}

	// Build contents from message history.
	contents := buildGeminiContents(messages)

	reqBody := map[string]any{
		"contents": contents,
		"tools": []map[string]any{
			{"functionDeclarations": funcDecls},
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

	body, err := json.Marshal(reqBody)
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("building request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.baseURL, p.model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return AgenticResponse{}, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return AgenticResponse{}, fmt.Errorf("reading gemini response: %w", err)
	}

	return parseGeminiAgenticResponse(data, p.model, p.logger)
}

func buildGeminiContents(messages []AgenticMessage) []map[string]any {
	contents := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			if len(m.ToolResults) > 0 {
				parts := make([]map[string]any, len(m.ToolResults))
				for i, tr := range m.ToolResults {
					response := map[string]any{"result": tr.Content}
					if tr.IsError {
						response = map[string]any{"error": tr.Content}
					}
					parts[i] = map[string]any{
						"functionResponse": map[string]any{
							"name":     tr.Name,
							"response": response,
						},
					}
				}
				contents = append(contents, map[string]any{"role": "user", "parts": parts})
			} else {
				contents = append(contents, map[string]any{
					"role":  "user",
					"parts": []map[string]any{{"text": m.Content}},
				})
			}
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				parts := make([]map[string]any, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					var args map[string]any
					_ = json.Unmarshal(tc.Input, &args)
					parts[i] = map[string]any{
						"functionCall": map[string]any{
							"name": tc.Name,
							"args": args,
						},
					}
				}
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			} else {
				contents = append(contents, map[string]any{
					"role":  "model",
					"parts": []map[string]any{{"text": m.Content}},
				})
			}
		}
	}
	return contents
}

func parseGeminiAgenticResponse(data []byte, model string, logger *slog.Logger) (AgenticResponse, error) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return AgenticResponse{}, fmt.Errorf("parsing gemini response: %w", err)
	}
	if len(result.Candidates) == 0 {
		return AgenticResponse{}, fmt.Errorf("empty response from gemini")
	}

	var toolCalls []ToolCall
	var textBuilder strings.Builder
	for i, part := range result.Candidates[0].Content.Parts {
		if part.FunctionCall != nil {
			input, _ := json.Marshal(part.FunctionCall.Args)
			// Gemini does not provide call IDs; generate a unique ID per call so
			// that multiple calls to the same function in one turn are distinguishable.
			toolCalls = append(toolCalls, ToolCall{
				ID:    fmt.Sprintf("%s_%d", part.FunctionCall.Name, i),
				Name:  part.FunctionCall.Name,
				Input: input,
			})
		} else if part.Text != "" {
			textBuilder.WriteString(part.Text)
		}
	}

	return buildAgenticResponse(toolCalls, textBuilder.String(), "gemini", logger)
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
