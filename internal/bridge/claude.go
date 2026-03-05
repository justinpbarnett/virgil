package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

type claudeProvider struct {
	model        string
	binary       string
	resolvedPath string
	maxTurns     int
	logger       *slog.Logger
	verbose      bool
	lastUsage    Usage
}

func (c *claudeProvider) LastUsage() Usage {
	return c.lastUsage
}

func ClaudeProvider(cfg ProviderConfig) *claudeProvider {
	model := cfg.Model
	if model == "" {
		model = "sonnet"
	}
	binary := cfg.Binary
	if binary == "" {
		binary = "claude"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	resolved, _ := exec.LookPath(binary)
	return &claudeProvider{model: model, binary: binary, resolvedPath: resolved, maxTurns: cfg.MaxTurns, logger: logger, verbose: cfg.Verbose}
}

func (c *claudeProvider) Available() bool {
	return c.resolvedPath != ""
}

// checkAvailable returns an error if the Claude CLI is not found.
func (c *claudeProvider) checkAvailable() error {
	if !c.Available() {
		return fmt.Errorf("claude CLI not found on PATH — install it or run: npm install -g @anthropic-ai/claude-code")
	}
	return nil
}

// buildArgs constructs the common argument list for the Claude CLI.
func (c *claudeProvider) buildArgs(system string, extra ...string) []string {
	args := []string{
		"-p",
		"--model", c.model,
		"--no-session-persistence",
		"--max-turns", strconv.Itoa(c.maxTurns),
	}
	args = append(args, extra...)
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	return args
}

func (c *claudeProvider) logRequest(streaming bool, system, user string) {
	c.logger.Info("provider called", "model", c.model, "streaming", streaming)
	c.logger.Debug("provider request", "system_len", len(system), "user_len", len(user))
	if c.verbose {
		c.logger.Debug("provider prompt", "system", system, "user", user)
	}
}

func (c *claudeProvider) logResponse(result string) {
	c.logger.Info("provider responded", "bytes", len(result))
}

func (c *claudeProvider) logError(err error, stderr string) {
	c.logger.Error("provider failed", "error", err, "stderr", stderr)
}

// classifyCLIError converts a CLI execution error into a user-friendly error.
func classifyCLIError(ctx context.Context, runErr error, stderr string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("provider timeout: %w", ctx.Err())
	}
	if strings.Contains(stderr, "auth") || strings.Contains(stderr, "login") {
		return fmt.Errorf("authentication required — run: claude auth login")
	}
	return fmt.Errorf("claude CLI error: %s (stderr: %s)", runErr, stderr)
}

func (c *claudeProvider) Complete(ctx context.Context, system, user string) (string, error) {
	if err := c.checkAvailable(); err != nil {
		return "", err
	}

	c.logRequest(false, system, user)

	args := c.buildArgs(system, "--output-format", "json")
	cmd := exec.CommandContext(ctx, c.resolvedPath, args...)
	cmd.Stdin = strings.NewReader(user)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.logError(err, stderr.String())
		return "", classifyCLIError(ctx, err, stderr.String())
	}

	result, usage := parseClaudeResponseWithUsage(stdout.Bytes(), c.model)
	if result == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}
	c.lastUsage = usage
	c.logResponse(result)
	return result, nil
}

func (c *claudeProvider) CompleteStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error) {
	if err := c.checkAvailable(); err != nil {
		return "", err
	}

	c.logRequest(true, system, user)

	args := c.buildArgs(system)
	cmd := exec.CommandContext(ctx, c.resolvedPath, args...)
	cmd.Stdin = strings.NewReader(user)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting claude CLI: %w", err)
	}

	var full strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			full.WriteString(chunk)
			onChunk(chunk)
		}
		if readErr != nil {
			break // io.EOF is expected; cmd.Wait surfaces other errors
		}
	}

	if err := cmd.Wait(); err != nil {
		c.logError(err, stderr.String())
		return "", classifyCLIError(ctx, err, stderr.String())
	}

	result := strings.TrimSpace(full.String())
	c.logResponse(result)
	return result, nil
}

func parseClaudeResponse(data []byte) (string, error) {
	text, _ := parseClaudeResponseWithUsage(data, "")
	if text == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}
	return text, nil
}

func parseClaudeResponseWithUsage(data []byte, model string) (string, Usage) {
	// Try parsing as JSON with result field (may include usage)
	var response struct {
		Result string `json:"result"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &response); err == nil && response.Result != "" {
		usage := Usage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
			Model:        model,
			Cost:         CostFor(model, response.Usage.InputTokens, response.Usage.OutputTokens),
		}
		return response.Result, usage
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
			return strings.Join(texts, "\n"), Usage{}
		}
	}

	// Fall back to raw string
	s := strings.TrimSpace(string(data))
	return s, Usage{}
}
