package tools

import (
	"context"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/memory"
)

// RegisterPeopleTools registers the people_lookup tool.
func RegisterPeopleTools(reg *Registry, store *memory.Store) {
	reg.Register(&internal.Tool{
		Name:        "people_lookup",
		Description: "Look up a person: who they are, relationship to user, recent interactions, organizational context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Person's name or email",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return &internal.ToolResult{Error: "name is required"}, nil
			}

			facts, err := store.Facts(name, "")
			if err != nil {
				return nil, err
			}

			interactions, err := store.Search(memory.SearchParams{
				Query: name,
				Type:  memory.TypeInteraction,
				Limit: 10,
			})
			if err != nil {
				return nil, err
			}

			observations, err := store.Search(memory.SearchParams{
				Query: name,
				Type:  memory.TypeObservation,
				Limit: 10,
			})
			if err != nil {
				return nil, err
			}

			profile := map[string]any{
				"name":         name,
				"facts":        facts,
				"interactions": interactions,
				"observations": observations,
			}

			return &internal.ToolResult{Data: profile}, nil
		},
	})
}
