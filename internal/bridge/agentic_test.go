package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func echoTool() bridge.Tool {
	return bridge.Tool{
		Name:        "echo",
		Description: "Returns the input text unchanged.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Execute: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(input, &args)
			return args.Text, nil
		},
	}
}

func TestRunAgenticLoopImmediateText(t *testing.T) {
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{FinalText: "done immediately"},
		},
	}

	result, err := bridge.RunAgenticLoop(context.Background(), provider, "sys", "hello", nil, 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done immediately" {
		t.Errorf("expected 'done immediately', got %q", result)
	}
}

func TestRunAgenticLoopOneTool(t *testing.T) {
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{testutil.MakeToolCall("c1", "echo", map[string]any{"text": "hello"})}},
			{FinalText: "tool executed, done"},
		},
	}

	result, err := bridge.RunAgenticLoop(context.Background(), provider, "sys", "use echo", []bridge.Tool{echoTool()}, 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "tool executed, done" {
		t.Errorf("expected 'tool executed, done', got %q", result)
	}
}

func TestRunAgenticLoopToolError(t *testing.T) {
	// Tool that always errors; loop should continue and return final text.
	errorTool := bridge.Tool{
		Name:        "fail",
		Description: "Always fails.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", errors.New("tool failure")
		},
	}
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{{ID: "c1", Name: "fail", Input: json.RawMessage(`{}`)}}},
			{FinalText: "recovered after error"},
		},
	}

	result, err := bridge.RunAgenticLoop(context.Background(), provider, "sys", "try", []bridge.Tool{errorTool}, 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered after error" {
		t.Errorf("got %q", result)
	}
}

func TestRunAgenticLoopMaxTurns(t *testing.T) {
	// Provider always returns a tool call — never a final text.
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{testutil.MakeToolCall("c1", "echo", map[string]any{"text": "x"})}},
		},
	}

	_, err := bridge.RunAgenticLoop(context.Background(), provider, "sys", "spin", []bridge.Tool{echoTool()}, 3, nil)
	if err == nil {
		t.Fatal("expected error for exhausted turns")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("expected 'exhausted' in error, got: %v", err)
	}
}

func TestRunAgenticLoopContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{{FinalText: "should not reach"}},
	}
	// Provider returns context error on cancelled context.
	provider.Err = context.Canceled

	_, err := bridge.RunAgenticLoop(ctx, provider, "sys", "hello", nil, 5, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
