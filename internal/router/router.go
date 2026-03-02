package router

import (
	"strings"

	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

type RouteResult struct {
	Pipe       string
	Confidence float64
	Layer      int
}

type Router struct {
	exactMap     map[string]string            // signal → pipe name
	keywordIndex map[string][]string           // keyword → []pipe names
	pipeKeywords map[string][]string           // pipe name → []keywords
	categories   map[string][]pipe.Definition  // category → []definitions
	definitions  map[string]pipe.Definition
	missLog      *MissLog
	threshold    float64
}

func NewRouter(defs []pipe.Definition, missLog *MissLog) *Router {
	r := &Router{
		exactMap:     make(map[string]string),
		keywordIndex: make(map[string][]string),
		pipeKeywords: make(map[string][]string),
		categories:   make(map[string][]pipe.Definition),
		definitions:  make(map[string]pipe.Definition),
		missLog:      missLog,
		threshold:    0.6,
	}

	for _, def := range defs {
		r.definitions[def.Name] = def

		// Build exact match map
		for _, exact := range def.Triggers.Exact {
			r.exactMap[strings.ToLower(exact)] = def.Name
		}

		// Build keyword inverted index
		r.pipeKeywords[def.Name] = def.Triggers.Keywords
		for _, kw := range def.Triggers.Keywords {
			lower := strings.ToLower(kw)
			r.keywordIndex[lower] = append(r.keywordIndex[lower], def.Name)
		}

		// Build category map
		r.categories[def.Category] = append(r.categories[def.Category], def)
	}

	return r
}

func (r *Router) Route(signal string, parsed parser.ParsedSignal) RouteResult {
	lower := strings.ToLower(signal)

	// Layer 1: Exact match
	if pipeName, ok := r.exactMap[lower]; ok {
		return RouteResult{Pipe: pipeName, Confidence: 1.0, Layer: 1}
	}

	// Layer 2: Keyword scoring
	words := tokenize(lower)
	scores := make(map[string]int)
	var keywordsFound []string

	for _, w := range words {
		if pipes, ok := r.keywordIndex[w]; ok {
			keywordsFound = append(keywordsFound, w)
			for _, p := range pipes {
				scores[p]++
			}
		}
	}

	bestPipe := ""
	bestScore := 0.0
	for pipeName, hits := range scores {
		total := len(r.pipeKeywords[pipeName])
		if total == 0 {
			continue
		}
		score := float64(hits) / float64(total)
		if score > bestScore {
			bestScore = score
			bestPipe = pipeName
		}
	}

	if bestScore >= r.threshold {
		return RouteResult{Pipe: bestPipe, Confidence: bestScore, Layer: 2}
	}

	// Layer 3: Category narrowing
	if bestPipe != "" {
		def := r.definitions[bestPipe]
		categoryPipes := r.categories[def.Category]

		for _, cp := range categoryPipes {
			// Score using parsed components
			confidence := r.scoreParsedMatch(cp, parsed)
			if confidence > bestScore {
				bestScore = confidence
				bestPipe = cp.Name
			}
		}

		if bestScore >= r.threshold {
			return RouteResult{Pipe: bestPipe, Confidence: bestScore, Layer: 3}
		}
	}

	// Also try parsed verb directly if it matches a pipe
	if parsed.Verb != "" {
		if _, ok := r.definitions[parsed.Verb]; ok {
			return RouteResult{Pipe: parsed.Verb, Confidence: 0.8, Layer: 3}
		}
	}

	// Layer 4: Stub fallback
	var keywordsNotFound []string
	for _, w := range words {
		if _, ok := r.keywordIndex[w]; !ok {
			keywordsNotFound = append(keywordsNotFound, w)
		}
	}

	if r.missLog != nil {
		r.missLog.Log(MissEntry{
			Signal:           signal,
			KeywordsFound:    keywordsFound,
			KeywordsNotFound: keywordsNotFound,
			FallbackPipe:     "chat",
		})
	}

	return RouteResult{Pipe: "chat", Confidence: 0.0, Layer: 4}
}

func (r *Router) scoreParsedMatch(def pipe.Definition, parsed parser.ParsedSignal) float64 {
	score := 0.0
	checks := 0.0

	// Check if parsed verb matches this pipe
	if parsed.Verb != "" {
		checks++
		if parsed.Verb == def.Name {
			score++
		}
	}

	// Check if source references this pipe
	if parsed.Source != "" {
		checks++
		if parsed.Source == def.Name {
			score++
		}
	}

	// Check keywords from parsed topic
	if parsed.Topic != "" {
		topicWords := tokenize(parsed.Topic)
		for _, tw := range topicWords {
			for _, kw := range def.Triggers.Keywords {
				if tw == strings.ToLower(kw) {
					score += 0.5
					break
				}
			}
		}
		checks++
	}

	if checks == 0 {
		return 0
	}
	return score / checks
}

func tokenize(s string) []string {
	return strings.Fields(strings.ToLower(s))
}
