package internal

import (
	"context"
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
