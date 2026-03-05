package testutil

import (
	"context"
	"encoding/json"

	"github.com/justinpbarnett/virgil/internal/bridge"
)

// MockProvider implements bridge.Provider for tests.
type MockProvider struct {
	Response string
	Err      error
}

func (m *MockProvider) Complete(_ context.Context, _, user string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	if m.Response != "" {
		return m.Response, nil
	}
	return "Mock response for: " + user, nil
}

// MockStreamProvider implements bridge.StreamingProvider for tests.
type MockStreamProvider struct {
	MockProvider
	Chunks []string
}

func (m *MockStreamProvider) CompleteStream(_ context.Context, _, _ string, onChunk func(string)) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	for _, c := range m.Chunks {
		onChunk(c)
	}
	return m.Response, nil
}

// AgenticTurn describes one exchange in a MockAgenticProvider script.
type AgenticTurn struct {
	// If ToolCalls is non-empty the provider returns tool calls on this turn.
	// Otherwise it returns FinalText.
	ToolCalls []bridge.ToolCall
	FinalText string
}

// MockAgenticProvider implements bridge.AgenticProvider for tests.
// Turns are consumed in order; once exhausted it returns FinalText from the
// last turn indefinitely.
type MockAgenticProvider struct {
	Turns []AgenticTurn
	Err   error
	turn  int
}

func (m *MockAgenticProvider) Complete(_ context.Context, _, user string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return "Mock response for: " + user, nil
}

func (m *MockAgenticProvider) CompleteWithTools(_ context.Context, _ string, _ []bridge.AgenticMessage, _ []bridge.Tool) (bridge.AgenticResponse, error) {
	if m.Err != nil {
		return bridge.AgenticResponse{}, m.Err
	}
	idx := m.turn
	if idx >= len(m.Turns) {
		idx = len(m.Turns) - 1
	}
	m.turn++
	t := m.Turns[idx]
	if len(t.ToolCalls) > 0 {
		return bridge.AgenticResponse{ToolCalls: t.ToolCalls}, nil
	}
	return bridge.AgenticResponse{Text: t.FinalText}, nil
}

// MakeToolCall builds a bridge.ToolCall with JSON-marshalled args for test use.
func MakeToolCall(id, name string, args map[string]any) bridge.ToolCall {
	input, _ := json.Marshal(args)
	return bridge.ToolCall{ID: id, Name: name, Input: input}
}
