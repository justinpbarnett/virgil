package study

import (
	"math"
	"sort"
	"strings"
	"time"
)

// RankWeights controls the contribution of each signal to the composite score.
type RankWeights struct {
	SemanticProximity  float64
	QueryMatch         float64
	StructuralCentral  float64
	InformationDensity float64
	Recency            float64
	RoleAlignment      float64
	DiscoveryAgreement float64
}

// DefaultWeights returns the default ranking weights.
func DefaultWeights() RankWeights {
	return RankWeights{
		SemanticProximity:  0.25,
		QueryMatch:         0.25,
		StructuralCentral:  0.10,
		InformationDensity: 0.15,
		Recency:            0.05,
		RoleAlignment:      0.15,
		DiscoveryAgreement: 0.05,
	}
}

// rankItems scores and sorts extracted items by composite relevance.
func rankItems(items []ExtractedItem, query, role string, candidateSourceCounts map[string]int) []RankedItem {
	weights := DefaultWeights()
	now := time.Now()
	centrality := buildCentralityIndex(items)

	ranked := make([]RankedItem, 0, len(items))
	for _, item := range items {
		score := computeScore(item, query, role, weights, now, candidateSourceCounts, centrality)
		ranked = append(ranked, RankedItem{ExtractedItem: item, Score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		// Tie-break: smaller token count first (more budget-efficient)
		if ranked[i].TokenCount != ranked[j].TokenCount {
			return ranked[i].TokenCount < ranked[j].TokenCount
		}
		// Then by distance (closer to entry point)
		if ranked[i].Metadata.Distance != ranked[j].Metadata.Distance {
			return ranked[i].Metadata.Distance < ranked[j].Metadata.Distance
		}
		// Then alphabetical by path (deterministic)
		return ranked[i].Metadata.Path < ranked[j].Metadata.Path
	})

	return ranked
}

func computeScore(item ExtractedItem, query, role string, weights RankWeights, now time.Time, sourceAgreement map[string]int, centrality map[string]float64) float64 {
	var score float64

	score += weights.SemanticProximity * proximityScore(item.Metadata.Distance)
	score += weights.QueryMatch * queryMatchScore(item, query)
	score += weights.StructuralCentral * centralityScore(item, centrality)
	score += weights.InformationDensity * densityScore(item)
	score += weights.Recency * recencyScore(item.Metadata.Modified, now)
	score += weights.RoleAlignment * roleAlignmentScore(item, role)
	score += weights.DiscoveryAgreement * agreementScore(item.ID, sourceAgreement)

	return score
}

// proximityScore: closer to entry point = higher score.
// Distance 0 = 1.0, distance 1 = 0.7, distance 2 = 0.4, distance 3+ = 0.1
func proximityScore(distance int) float64 {
	switch {
	case distance <= 0:
		return 1.0
	case distance == 1:
		return 0.7
	case distance == 2:
		return 0.4
	default:
		return 0.1
	}
}

// queryMatchScore: how well the item matches the query string.
func queryMatchScore(item ExtractedItem, query string) float64 {
	if query == "" {
		return 0.5 // neutral if no query
	}

	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0.5
	}

	content := strings.ToLower(item.Content)
	path := strings.ToLower(item.Metadata.Path)
	symbol := strings.ToLower(item.Metadata.Symbol)

	matchCount := 0
	for _, term := range terms {
		if strings.Contains(content, term) || strings.Contains(path, term) || strings.Contains(symbol, term) {
			matchCount++
		}
	}

	return float64(matchCount) / float64(len(terms))
}

// densityScore: ratio of unique identifiers to total tokens.
// Higher density = more information per token.
func densityScore(item ExtractedItem) float64 {
	if item.TokenCount == 0 {
		return 0
	}

	// Count unique "interesting" tokens (words that look like identifiers)
	words := strings.Fields(item.Content)
	seen := make(map[string]struct{}, len(words))
	identifiers := 0
	for _, w := range words {
		w = strings.Trim(w, "(){}[],;:\"'`")
		if len(w) < 2 {
			continue
		}
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		// Simple heuristic: contains uppercase or underscore = likely identifier
		if containsIdentifierChar(w) {
			identifiers++
		}
	}

	ratio := float64(identifiers) / float64(item.TokenCount)
	// Normalize to 0-1 range. 0.3+ identifier ratio is high density.
	return math.Min(ratio/0.3, 1.0)
}

func containsIdentifierChar(w string) bool {
	for _, c := range w {
		if c == '_' || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// recencyScore: more recently modified = higher score.
func recencyScore(modified time.Time, now time.Time) float64 {
	if modified.IsZero() {
		return 0.3 // neutral for unknown modification time
	}
	age := now.Sub(modified)
	switch {
	case age < 24*time.Hour:
		return 1.0
	case age < 7*24*time.Hour:
		return 0.8
	case age < 30*24*time.Hour:
		return 0.6
	case age < 90*24*time.Hour:
		return 0.4
	default:
		return 0.2
	}
}

// roleAlignmentScore: how well the item's kind matches the role's priorities.
func roleAlignmentScore(item ExtractedItem, role string) float64 {
	kind := item.Metadata.Kind
	gran := item.Granularity

	switch role {
	case "builder":
		// Builders want implementation detail
		switch {
		case gran == GranDeclaration:
			return 1.0
		case kind == KindFunction || kind == KindMethod:
			return 0.9
		case kind == KindType:
			return 0.7
		default:
			return 0.4
		}

	case "reviewer":
		// Reviewers want contracts and interfaces
		switch {
		case kind == KindInterface || gran == GranInterface:
			return 1.0
		case gran == GranSignature:
			return 0.9
		case kind == KindType:
			return 0.8
		default:
			return 0.3
		}

	case "debugger":
		// Debuggers want execution paths
		switch {
		case gran == GranDeclaration && (kind == KindFunction || kind == KindMethod):
			return 1.0
		case kind == KindType:
			return 0.6
		default:
			return 0.4
		}

	case "planner":
		// Planners want architecture
		switch {
		case kind == KindInterface || gran == GranInterface:
			return 1.0
		case gran == GranFileSummary || kind == KindFile:
			return 0.9
		case kind == KindType:
			return 0.7
		default:
			return 0.3
		}

	default: // "general"
		return 0.5
	}
}

// agreementScore: how many discovery methods independently found this item.
func agreementScore(id string, sourceAgreement map[string]int) float64 {
	count := sourceAgreement[id]
	switch {
	case count >= 3:
		return 1.0
	case count == 2:
		return 0.7
	default:
		return 0.3
	}
}

// buildCentralityIndex counts how many other items reference each item's path or symbol.
// Items that are referenced by many others are structural hubs — architecturally important.
func buildCentralityIndex(items []ExtractedItem) map[string]float64 {
	// Collect all paths and symbols as reference targets
	targets := make(map[string]string) // path or symbol → item ID
	for _, item := range items {
		if item.Metadata.Path != "" {
			targets[item.Metadata.Path] = item.ID
		}
		if item.Metadata.Symbol != "" {
			targets[item.Metadata.Symbol] = item.ID
		}
	}

	// Count how many items reference each target
	refCounts := make(map[string]int) // item ID → reference count
	for _, item := range items {
		content := item.Content
		for target, targetID := range targets {
			if targetID == item.ID {
				continue // don't count self-references
			}
			if strings.Contains(content, target) {
				refCounts[targetID]++
			}
		}
	}

	// Normalize to 0-1 range
	maxRefs := 0
	for _, count := range refCounts {
		if count > maxRefs {
			maxRefs = count
		}
	}

	centrality := make(map[string]float64)
	if maxRefs == 0 {
		return centrality
	}
	for id, count := range refCounts {
		centrality[id] = float64(count) / float64(maxRefs)
	}
	return centrality
}

// centralityScore: how many other candidates reference or depend on this item.
func centralityScore(item ExtractedItem, centrality map[string]float64) float64 {
	if score, ok := centrality[item.ID]; ok {
		return score
	}
	return 0.0
}
