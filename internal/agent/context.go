package agent

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/memory"
)

func (a *Agent) assembleContext(signal internal.Signal) string {
	var parts []string

	entities := extractEntityHints(signal.Content)
	if len(entities) > 0 {
		factsByEntity, err := a.store.FactsBatch(entities, signal.Bridge)
		if err != nil {
			slog.Warn("context: facts lookup failed", "entities", entities, "err", err)
		} else {
			for _, entity := range entities {
				if facts := factsByEntity[entity]; len(facts) > 0 {
					parts = append(parts, formatMemories(fmt.Sprintf("Facts about %s:", entity), facts))
				}
			}
		}
	}

	if topic := inferTopic(signal); topic != "" {
		interactions, err := a.store.SearchRecent(topic, memory.TypeInteraction, 5)
		if err != nil {
			slog.Warn("context: interaction search failed", "topic", topic, "err", err)
		} else if len(interactions) > 0 {
			parts = append(parts, formatMemories("Recent interactions:", interactions))
		}
	}

	observations, err := a.store.SearchRecent("", memory.TypeObservation, 10)
	if err != nil {
		slog.Warn("context: observation search failed", "err", err)
	} else if len(observations) > 0 {
		parts = append(parts, formatMemories("Recent observations:", observations))
	}

	return strings.Join(parts, "\n\n")
}

func extractEntityHints(content string) []string {
	words := strings.Fields(content)
	seen := make(map[string]bool)
	var entities []string

	for i, w := range words {
		clean := strings.Trim(w, ".,!?;:\"'()[]")
		if clean == "" || len(clean) < 2 {
			continue
		}
		if i > 0 && clean[0] >= 'A' && clean[0] <= 'Z' {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				seen[lower] = true
				entities = append(entities, clean)
			}
		}
	}
	return entities
}

func inferTopic(signal internal.Signal) string {
	content := strings.TrimSpace(signal.Content)
	if utf8.RuneCountInString(content) > 100 {
		runes := []rune(content)
		content = string(runes[:100])
	}
	for _, sep := range []string{".", "?", "!"} {
		if idx := strings.Index(content, sep); idx > 0 {
			content = content[:idx]
			break
		}
	}
	return strings.TrimSpace(content)
}

func formatMemories(title string, entries []memory.Entry) string {
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, title)
	for _, e := range entries {
		lines = append(lines, "- "+e.Content)
	}
	return strings.Join(lines, "\n")
}
