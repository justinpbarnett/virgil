package tools

import (
	"context"
	"fmt"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/memory"
)

// RegisterMemoryTools registers memory_store, memory_search, and memory_facts.
func RegisterMemoryTools(reg *Registry, store *memory.Store) {
	reg.Register(&internal.Tool{
		Name:        "memory_store",
		Description: "Store something in memory. Use 'observation' for things you noticed, 'interaction' for exchanges with the user, 'fact' for distilled knowledge.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"memory_type": map[string]any{
					"type": "string",
					"enum": []string{"observation", "interaction", "fact"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "What to remember",
				},
				"topic": map[string]any{
					"type":        "string",
					"description": "Primary topic or entity",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "personal or bridge:{org_id}",
					"default":     "personal",
				},
				"entities": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
							"type": map[string]any{
								"type": "string",
								"enum": []string{"person", "project", "org", "tool"},
							},
							"role": map[string]any{
								"type": "string",
								"enum": []string{"subject", "mentioned", "owner", "participant"},
							},
						},
					},
				},
			},
			"required": []string{"memory_type", "content"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			memType, _ := params["memory_type"].(string)
			content, _ := params["content"].(string)
			if memType == "" || content == "" {
				return &internal.ToolResult{Error: "memory_type and content are required"}, nil
			}

			p := memory.StoreParams{
				Type:    memType,
				Content: content,
			}
			if v, ok := params["topic"].(string); ok {
				p.Topic = v
			}
			if v, ok := params["scope"].(string); ok {
				p.Scope = v
			}
			if v, ok := params["entities"].([]any); ok {
				p.Entities = parseEntities(v)
			}

			id, err := store.Store(p)
			if err != nil {
				return nil, fmt.Errorf("memory_store: %w", err)
			}

			entry, err := store.Get(id)
			if err != nil {
				return nil, fmt.Errorf("memory_store get: %w", err)
			}
			if entry == nil {
				return nil, fmt.Errorf("memory_store: stored memory %s not found after insert", id)
			}
			return &internal.ToolResult{Data: entry}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "memory_search",
		Description: "Search memory for information about a topic, person, project, or event. Returns relevant memories ranked by relevance.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What to search for",
				},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"observation", "interaction", "fact"},
					"description": "Filter by type",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "Filter by scope",
				},
				"limit": map[string]any{
					"type":    "integer",
					"default": 10,
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			query, _ := params["query"].(string)
			if query == "" {
				return &internal.ToolResult{Error: "query is required"}, nil
			}

			sp := memory.SearchParams{Query: query}
			if v, ok := params["type"].(string); ok {
				sp.Type = v
			}
			if v, ok := params["scope"].(string); ok {
				sp.Scope = v
			}
			if v, ok := params["limit"].(float64); ok {
				sp.Limit = int(v)
			}

			results, err := store.Search(sp)
			if err != nil {
				return nil, fmt.Errorf("memory_search: %w", err)
			}
			return &internal.ToolResult{Data: results}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "memory_facts",
		Description: "Get known facts about a topic, person, or project. Facts are distilled knowledge that persists indefinitely.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"about": map[string]any{
					"type":        "string",
					"description": "Person, topic, or project name",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "Filter by scope",
				},
			},
			"required": []string{"about"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			about, _ := params["about"].(string)
			if about == "" {
				return &internal.ToolResult{Error: "about is required"}, nil
			}

			scope, _ := params["scope"].(string)
			facts, err := store.Facts(about, scope)
			if err != nil {
				return nil, fmt.Errorf("memory_facts: %w", err)
			}
			return &internal.ToolResult{Data: facts}, nil
		},
	})
}

func parseEntities(raw []any) []memory.Entity {
	entities := make([]memory.Entity, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		e := memory.Entity{}
		if v, ok := m["name"].(string); ok {
			e.Name = v
		}
		if v, ok := m["type"].(string); ok {
			e.Type = v
		}
		if v, ok := m["role"].(string); ok {
			e.Role = v
		}
		if e.Name != "" {
			entities = append(entities, e)
		}
	}
	return entities
}
