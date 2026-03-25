package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/justinpbarnett/virgil/internal/tools"
)

// Run starts an MCP server on stdio, exposing all registered tools.
// Blocks until ctx is cancelled or the transport closes.
func Run(ctx context.Context, reg *tools.Registry) error {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "virgil",
		Version: "v1.0",
	}, nil)

	registered := 0
	for _, t := range reg.Definitions() {
		schema, err := json.Marshal(t.Parameters)
		if err != nil {
			slog.Warn("mcp: skip tool, can't marshal schema", "tool", t.Name, "err", err)
			continue
		}

		t := t // capture
		server.AddTool(&mcpsdk.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(schema),
		}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			var params map[string]any
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
					return &mcpsdk.CallToolResult{
						Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("bad arguments: %v", err)}},
						IsError: true,
					}, nil
				}
			}

			result, err := t.Execute(ctx, params)
			if err != nil {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: err.Error()}},
					IsError: true,
				}, nil
			}

			if result.Error != "" {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: result.Error}},
					IsError: true,
				}, nil
			}

			var text string
			if result.Text != "" {
				text = result.Text
			} else if result.Data != nil {
				out, err := json.Marshal(result.Data)
				if err != nil {
					text = fmt.Sprintf("%v", result.Data)
				} else {
					text = string(out)
				}
			}

			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
			}, nil
		})
		registered++
	}

	slog.Info("mcp server starting", "tools", registered)
	return server.Run(ctx, &mcpsdk.StdioTransport{})
}
