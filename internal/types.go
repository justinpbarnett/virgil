package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Signal is every input from any channel.
type Signal struct {
	ID        string            `json:"id"`
	Channel   string            `json:"channel"`
	Account   string            `json:"account"`
	Bridge    string            `json:"bridge"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp time.Time         `json:"timestamp"`
}

// RandomHex returns a random hex string. n is the number of random bytes;
// the returned string is 2n characters long (e.g., RandomHex(8) returns 16 characters).
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// NewSignal creates a Signal with the given channel and content.
// ID and Timestamp are set automatically.
func NewSignal(channel, content string) Signal {
	return Signal{
		ID:        RandomHex(8),
		Channel:   channel,
		Content:   content,
		Timestamp: time.Now(),
	}
}

// Tool is a capability the model can invoke.
type Tool struct {
	Name        string                                                                `json:"name"`
	Description string                                                                `json:"description"`
	Parameters  map[string]any                                                        `json:"parameters"`
	Execute     func(ctx context.Context, params map[string]any) (*ToolResult, error) `json:"-"`
}

// ToolResult is what a tool returns.
type ToolResult struct {
	Data  any    `json:"data,omitempty"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// Format serializes a ToolResult to a string for logging and agent responses.
func (r *ToolResult) Format() string {
	if r.Error != "" {
		return "error: " + r.Error
	}
	if r.Text != "" {
		return r.Text
	}
	if r.Data != nil {
		data, err := json.Marshal(r.Data)
		if err != nil {
			slog.Error("failed to marshal tool result", "err", err)
			return fmt.Sprintf("error: failed to serialize result: %v", err)
		}
		return string(data)
	}
	return "ok"
}

// Skill is a capability package loaded from a skill folder.
type Skill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Path        string            `json:"path"`
	Prompt      string            `json:"-"`
	Tools       []string          `json:"tools"`
	Model       string            `json:"model"`
	Fallback    []string          `json:"fallback"`
	Schedule    string            `json:"schedule"`
	Enabled     bool              `json:"enabled"`
	Gotchas     string            `json:"-"`
	Templates   map[string]string `json:"-"`
}

// Validate checks that required Skill fields are set.
func (s *Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("skill name is required")
	}
	if s.Prompt == "" {
		return fmt.Errorf("skill %q has no prompt (empty SKILL.md body)", s.Name)
	}
	return nil
}

// Event is a structured log entry for debugging and audit.
type Event struct {
	ID         int64     `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Component  string    `json:"component"`
	Action     string    `json:"action"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
	TraceID    string    `json:"trace_id,omitempty"`
	SpanID     string    `json:"span_id,omitempty"`
	ParentSpan string    `json:"parent_span,omitempty"`
	Model      string    `json:"model,omitempty"`
	TokensIn   int       `json:"tokens_in,omitempty"`
	TokensOut  int       `json:"tokens_out,omitempty"`
}
