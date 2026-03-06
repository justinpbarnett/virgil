package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// ToolChunkPrefix is the sentinel prefix used to signal tool call events
// through the text chunk stream. Consumers (e.g. runtime) check for this
// prefix to distinguish tool events from regular text chunks.
const ToolChunkPrefix = "\x00tool:"

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
				onChunk(ToolChunkPrefix + tc.Name + "\t" + toolSummary(tc.Name, tc.Input))
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

// toolSummary extracts a human-readable detail string from tool call input.
func toolSummary(name string, input json.RawMessage) string {
	var args struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &args)

	switch name {
	case "read_file", "write_file", "edit_file":
		return filepath.Base(args.Path)
	case "list_dir":
		if args.Path == "" || args.Path == "." {
			return "."
		}
		return args.Path
	case "run_shell":
		cmd := args.Command
		if len(cmd) > 40 {
			cmd = cmd[:40] + "…"
		}
		return cmd
	default:
		return ""
	}
}

func executeToolCall(ctx context.Context, tc ToolCall, tools []Tool) (string, error) {
	for _, t := range tools {
		if t.Name == tc.Name {
			return t.Execute(ctx, tc.Input)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", tc.Name)
}
