package router

import (
	"context"
	"log/slog"
	"strings"

	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// Routing layers, evaluated in order until a match is found.
const (
	LayerExact    = 1 // exact phrase match
	LayerKeyword  = 2 // keyword scoring above threshold
	LayerCategory = 3 // category narrowing / parsed verb
	LayerFallback = 4 // no match, fall back to chat
)

type RouteResult struct {
	Pipe       string
	Confidence float64
	Layer      int
}

type Router struct {
	exactMap     map[string]string            // signal → pipe name
	keywordIndex map[string][]string          // stemmed keyword → []pipe names
	pipeKeywords map[string][]string          // pipe name → []stemmed keywords
	categories   map[string][]pipe.Definition // category → []definitions
	definitions  map[string]pipe.Definition
	missLog      *MissLog
	threshold    float64
	logger       *slog.Logger
}

func NewRouter(defs []pipe.Definition, missLog *MissLog, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Router{
		exactMap:     make(map[string]string),
		keywordIndex: make(map[string][]string),
		pipeKeywords: make(map[string][]string),
		categories:   make(map[string][]pipe.Definition),
		definitions:  make(map[string]pipe.Definition),
		missLog:      missLog,
		threshold:    0.6,
		logger:       logger,
	}

	for _, def := range defs {
		r.definitions[def.Name] = def

		// Build exact match map
		for _, exact := range def.Triggers.Exact {
			r.exactMap[strings.ToLower(exact)] = def.Name
		}

		// Build keyword inverted index using stemmed forms so that morphological
		// variants of a keyword match at query time.
		stemmed := make([]string, 0, len(def.Triggers.Keywords))
		seen := make(map[string]bool)
		for _, kw := range def.Triggers.Keywords {
			sk := Stem(strings.ToLower(kw))
			if !seen[sk] {
				seen[sk] = true
				stemmed = append(stemmed, sk)
				r.keywordIndex[sk] = append(r.keywordIndex[sk], def.Name)
			}
		}
		r.pipeKeywords[def.Name] = stemmed

		// Build category map
		r.categories[def.Category] = append(r.categories[def.Category], def)
	}

	r.logger.Debug("router built", "pipes", len(defs), "keywords", len(r.keywordIndex))
	return r
}

func (r *Router) Route(ctx context.Context, signal string, parsed parser.ParsedSignal) RouteResult {
	lower := strings.ToLower(signal)

	// Layer 1: Exact match
	if pipeName, ok := r.exactMap[lower]; ok {
		result := RouteResult{Pipe: pipeName, Confidence: 1.0, Layer: LayerExact}
		r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
		return result
	}

	// Layer 2: Keyword scoring with stemming and synonym expansion
	words := tokenize(lower)
	scores := make(map[string]int)
	var keywordsFound []string

	for _, w := range words {
		for _, form := range StemAndExpand(w) {
			if pipes, ok := r.keywordIndex[form]; ok {
				keywordsFound = append(keywordsFound, w)
				for _, p := range pipes {
					scores[p]++
				}
				break // only count each signal word once even if it expands to multiple hits
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

	r.logger.Debug("keyword scoring", "scores", scores, "keywords_found", keywordsFound, "best", bestPipe, "best_score", bestScore)

	if bestScore >= r.threshold {
		result := RouteResult{Pipe: bestPipe, Confidence: bestScore, Layer: LayerKeyword}
		r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
		return result
	}

	// Wh-questions skip Layer 3: the verb/source match is too aggressive
	// for questions (e.g. "what would be cool to visualize?" matched
	// visualize via verb). Questions still reach Layer 4 (fallback/AI).
	if !parsed.IsQuestion {
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
				result := RouteResult{Pipe: bestPipe, Confidence: bestScore, Layer: LayerCategory}
				r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
				return result
			}
		}

		// Also try parsed verb/source directly if they match a pipe.
		for _, candidate := range []string{parsed.Verb, parsed.Source} {
			if candidate == "" {
				continue
			}
			if _, ok := r.definitions[candidate]; ok {
				result := RouteResult{Pipe: candidate, Confidence: 0.8, Layer: LayerCategory}
				r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
				return result
			}
		}
	}

	// Layer 4: Deterministic fallback — default to chat and log the miss.
	// The classifier has been removed; signals that reach here are either
	// genuinely conversational or have no matching keyword/category.
	var keywordsNotFound []string
	for _, w := range words {
		stemmed := Stem(w)
		if _, ok := r.keywordIndex[stemmed]; !ok {
			keywordsNotFound = append(keywordsNotFound, w)
		}
	}

	if r.missLog != nil {
		r.missLog.Log(MissEntry{
			Signal:           signal,
			KeywordsFound:    keywordsFound,
			KeywordsNotFound: keywordsNotFound,
			FallbackPipe:     "chat",
			AIConfidence:     0.0,
		})
	}

	r.logger.Warn("miss", "signal", signal)
	result := RouteResult{Pipe: "chat", Confidence: 0.0, Layer: LayerFallback}
	r.logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)
	return result
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
			for _, kw := range r.pipeKeywords[def.Name] {
				if tw == kw {
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

// tokenize splits s into cleaned tokens. Callers must pass a lowercased string.
func tokenize(s string) []string {
	fields := strings.Fields(s)
	for i, f := range fields {
		fields[i] = parser.CleanToken(f)
	}
	return fields
}
