package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/memory"
)

const omiBaseURL = "https://api.omi.me/v1/dev"

// RegisterOmiTools registers omi_ingest.
func RegisterOmiTools(reg *Registry, cfg *config.Config, store *memory.Store) {
	apiKey := os.Getenv(cfg.Channels.Omi.APIKeyEnv)

	reg.Register(&internal.Tool{
		Name:        "omi_ingest",
		Description: "Ingest recent conversations from Omi wearable pendant. Pulls transcribed conversations and stores them as observations in memory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"since": map[string]any{
					"type":        "string",
					"description": "Only conversations after this time (today, yesterday, 2026-03-20). Defaults to last sync.",
				},
				"limit": map[string]any{"type": "integer", "default": 50},
			},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if apiKey == "" {
				return &internal.ToolResult{Error: "Omi API key not configured (set " + cfg.Channels.Omi.APIKeyEnv + ")"}, nil
			}

			since, _ := params["since"].(string)
			limit := intParam(params, "limit", 50)

			u := fmt.Sprintf("%s/user/conversations?limit=%d", omiBaseURL, limit)

			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+apiKey)

			rawResp, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("omi_ingest fetch: %w", err)
			}
			defer rawResp.Body.Close()
			if rawResp.StatusCode >= 400 {
				return nil, fmt.Errorf("omi_ingest fetch: HTTP %d", rawResp.StatusCode)
			}

			var conversations []any
			if err := json.NewDecoder(io.LimitReader(rawResp.Body, maxResponseBytes)).Decode(&conversations); err != nil {
				return nil, fmt.Errorf("omi_ingest parse: %w", err)
			}

			// Filter by since client-side (API doesn't support server-side time filtering)
			var sinceUnix int64
			if since != "" {
				if t := parseDateTime(since); !t.IsZero() {
					sinceUnix = t.Unix()
				}
			}

			stored := 0
			failed := 0
			for _, m := range conversations {
				mem, ok := m.(map[string]any)
				if !ok {
					continue
				}

				if sinceUnix > 0 {
					startedAt, _ := mem["started_at"].(string)
					if t, err := time.Parse(time.RFC3339, startedAt); err != nil || t.Unix() < sinceUnix {
						continue
					}
				}

				transcript := omiExtractTranscript(mem)
				if transcript == "" {
					continue
				}

				topic := omiExtractTopic(mem)
				entities := omiExtractEntities(mem)
				scope := omiInferScope(transcript)

				_, err := store.Store(memory.StoreParams{
					Type:     memory.TypeObservation,
					Content:  transcript,
					Topic:    topic,
					Scope:    scope,
					Source:   "omi",
					Entities: entities,
				})
				if err != nil {
					convID, _ := mem["id"].(string)
					slog.Warn("omi_ingest store error", "conversation_id", convID, "err", err)
					failed++
					continue
				}
				stored++
			}

			result := map[string]any{
				"fetched": len(conversations),
				"stored":  stored,
			}
			if failed > 0 {
				result["failed"] = failed
			}
			return &internal.ToolResult{Data: result}, nil
		},
	})
}

func omiExtractTranscript(mem map[string]any) string {
	if t, ok := mem["transcript"].(string); ok && t != "" {
		return t
	}

	if segments, ok := mem["transcript_segments"].([]any); ok {
		var parts []string
		for _, s := range segments {
			if seg, ok := s.(map[string]any); ok {
				speaker, _ := seg["speaker"].(string)
				text, _ := seg["text"].(string)
				if text != "" {
					if speaker != "" {
						parts = append(parts, fmt.Sprintf("%s: %s", speaker, text))
					} else {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	if s, ok := mem["structured"].(map[string]any); ok {
		if overview, ok := s["overview"].(string); ok {
			return overview
		}
	}

	return ""
}

func omiExtractTopic(mem map[string]any) string {
	if s, ok := mem["structured"].(map[string]any); ok {
		if title, ok := s["title"].(string); ok && title != "" {
			return title
		}
	}
	return "omi conversation"
}

func omiExtractEntities(mem map[string]any) []memory.Entity {
	var entities []memory.Entity

	if segments, ok := mem["transcript_segments"].([]any); ok {
		seen := make(map[string]bool)
		for _, s := range segments {
			if seg, ok := s.(map[string]any); ok {
				if speaker, ok := seg["speaker"].(string); ok && speaker != "" && !seen[speaker] {
					seen[speaker] = true
					entities = append(entities, memory.Entity{
						Name: speaker,
						Type: "person",
						Role: memory.RoleParticipant,
					})
				}
			}
		}
	}

	return entities
}

func omiInferScope(transcript string) string {
	lower := strings.ToLower(transcript)
	switch {
	case strings.Contains(lower, "enver") || strings.Contains(lower, "unity") || strings.Contains(lower, "vr"):
		return "bridge:enver"
	case strings.Contains(lower, "passion") || strings.Contains(lower, "church") || strings.Contains(lower, "rock"):
		return "bridge:passion"
	default:
		return "personal"
	}
}
