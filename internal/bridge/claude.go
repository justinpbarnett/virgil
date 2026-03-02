package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type ClaudeProvider struct {
	model        string
	binary       string
	resolvedPath string
}

func NewClaudeProvider(model, binary string) *ClaudeProvider {
	if model == "" {
		model = "sonnet"
	}
	if binary == "" {
		binary = "claude"
	}
	resolved, _ := exec.LookPath(binary)
	return &ClaudeProvider{model: model, binary: binary, resolvedPath: resolved}
}

func (c *ClaudeProvider) Available() bool {
	return c.resolvedPath != ""
}

func (c *ClaudeProvider) Complete(ctx context.Context, system, user string) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("claude CLI not found on PATH — install it or run: npm install -g @anthropic-ai/claude-code")
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--model", c.model,
		"--no-session-persistence",
		"--max-turns", "1",
	}

	if system != "" {
		args = append(args, "--system-prompt", system)
	}

	cmd := exec.CommandContext(ctx, c.resolvedPath, args...)
	cmd.Stdin = strings.NewReader(user)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if ctx.Err() != nil {
			return "", fmt.Errorf("provider timeout: %w", ctx.Err())
		}
		if strings.Contains(stderrStr, "auth") || strings.Contains(stderrStr, "login") {
			return "", fmt.Errorf("authentication required — run: claude auth login")
		}
		return "", fmt.Errorf("claude CLI error: %s (stderr: %s)", err, stderrStr)
	}

	return parseClaudeResponse(stdout.Bytes())
}

func parseClaudeResponse(data []byte) (string, error) {
	// Try parsing as JSON with result field
	var response struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err == nil && response.Result != "" {
		return response.Result, nil
	}

	// Try parsing as JSON array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &blocks); err == nil && len(blocks) > 0 {
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n"), nil
		}
	}

	// Fall back to raw string
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}
	return s, nil
}
