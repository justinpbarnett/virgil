package agent

import (
	"fmt"
	"strings"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/memory"
)

func (a *Agent) assembleContext(signal internal.Signal) string {
	var parts []string

	entities := extractEntityHints(signal.Content)
	for _, entity := range entities {
		facts, err := a.Store.Facts(entity, signal.Bridge)
		if err != nil {
			continue
		}
		if len(facts) > 0 {
			parts = append(parts, formatFacts(entity, facts))
		}
	}

	if topic := inferTopic(signal); topic != "" {
		interactions, err := a.Store.SearchRecent(topic, memory.TypeInteraction, 5)
		if err == nil && len(interactions) > 0 {
			parts = append(parts, formatInteractions(interactions))
		}
	}

	observations, err := a.Store.SearchRecent("", memory.TypeObservation, 10)
	if err == nil && len(observations) > 0 {
		parts = append(parts, formatObservations(observations))
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
	if len(content) > 100 {
		content = content[:100]
	}
	for _, sep := range []string{".", "?", "!"} {
		if idx := strings.Index(content, sep); idx > 0 {
			content = content[:idx]
			break
		}
	}
	return strings.TrimSpace(content)
}

func formatFacts(entity string, facts []memory.Entry) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Facts about %s:", entity))
	for _, f := range facts {
		lines = append(lines, "- "+f.Content)
	}
	return strings.Join(lines, "\n")
}

func formatInteractions(interactions []memory.Entry) string {
	var lines []string
	lines = append(lines, "Recent interactions:")
	for _, i := range interactions {
		lines = append(lines, "- "+i.Content)
	}
	return strings.Join(lines, "\n")
}

func formatObservations(observations []memory.Entry) string {
	var lines []string
	lines = append(lines, "Recent observations:")
	for _, o := range observations {
		lines = append(lines, "- "+o.Content)
	}
	return strings.Join(lines, "\n")
}
