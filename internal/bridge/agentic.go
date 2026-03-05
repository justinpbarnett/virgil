package bridge

import (
	"context"
	"fmt"
)

// RunAgenticLoop drives the tool use loop until the model returns a final
// text response or maxTurns is exhausted.
//
// Loop:
//  1. Call CompleteWithTools with current history and tools.
//  2. If response has ToolCalls: execute each tool, append results to history, continue.
//  3. If response has Text: return it.
//  4. If maxTurns reached without Text: return error.
//
// onChunk, if non-nil, receives streamed text fragments (tool names, final text).
func RunAgenticLoop(ctx context.Context, p AgenticProvider, system, user string, tools []Tool, maxTurns int, onChunk func(string)) (string, error) {
	messages := []AgenticMessage{
		{Role: RoleUser, Content: user},
	}

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := p.CompleteWithTools(ctx, system, messages, tools)
		if err != nil {
			return "", err
		}

		if resp.Text != "" {
			if onChunk != nil {
				onChunk(resp.Text)
			}
			return resp.Text, nil
		}

		if len(resp.ToolCalls) == 0 {
			return "", fmt.Errorf("provider returned neither text nor tool calls on turn %d", turn+1)
		}

		// Append assistant turn with tool calls.
		messages = append(messages, AgenticMessage{
			Role:      RoleAssistant,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool and collect results.
		results := make([]ToolResult, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			if onChunk != nil {
				onChunk("[" + tc.Name + "]\n")
			}
			output, execErr := executeToolCall(ctx, tc, tools)
			if execErr != nil {
				results = append(results, ToolResult{CallID: tc.ID, Name: tc.Name, Content: execErr.Error(), IsError: true})
			} else {
				results = append(results, ToolResult{CallID: tc.ID, Name: tc.Name, Content: output})
			}
		}

		// Append user turn with tool results.
		messages = append(messages, AgenticMessage{
			Role:        RoleUser,
			ToolResults: results,
		})
	}

	return "", fmt.Errorf("agentic loop exhausted %d turns without a final response", maxTurns)
}

func executeToolCall(ctx context.Context, tc ToolCall, tools []Tool) (string, error) {
	for _, t := range tools {
		if t.Name == tc.Name {
			return t.Execute(ctx, tc.Input)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", tc.Name)
}
