// Package bridge defines the provider interface for AI model abstraction.
//
// Implement Provider to add a new AI backend. StreamingProvider adds
// token-by-token streaming. AgenticProvider adds multi-turn tool use.
package bridge

import (
	"context"
	"encoding/json"
)

// Provider is the minimal interface for an AI model backend.
type Provider interface {
	Complete(ctx context.Context, system string, user string) (string, error)
}

// StreamingProvider extends Provider with streaming support.
type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error)
}

// AgenticProvider extends Provider with multi-turn tool use.
type AgenticProvider interface {
	Provider
	CompleteWithTools(ctx context.Context, system string, messages []AgenticMessage, tools []Tool) (AgenticResponse, error)
}

// UsageReporter is an optional interface for providers that track token usage.
type UsageReporter interface {
	LastUsage() Usage
}

// Tool defines a capability the model can invoke during an agentic loop.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Execute     func(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolCall is a single tool invocation returned by the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// AgenticResponse is the result of one turn in an agentic loop.
type AgenticResponse struct {
	Text      string
	ToolCalls []ToolCall
}

// AgenticMessage is a single entry in the conversation history.
type AgenticMessage struct {
	Role        string
	Content     string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// ToolResult carries the output of an executed tool call.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// Usage carries token counts and computed cost for a provider API call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	Model        string
	Cost         float64
}
