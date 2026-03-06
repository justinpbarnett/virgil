package router

import (
	"context"
	"log/slog"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/justinpbarnett/virgil/internal/nlp"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// Routing layers, evaluated in order until a match is found.
const (
	LayerExact    = 1 // exact phrase match
	LayerKeyword  = 2 // BM25 keyword scoring above threshold
	LayerCategory = 3 // parsed verb / source match
	LayerFallback = 4 // no match, fall back to chat
)

type RouteResult struct {
	Pipe             string
	Confidence       float64
	Layer            int
	KeywordsFound    []string // populated at Layer 4 for miss logging
	KeywordsNotFound []string // populated at Layer 4 for miss logging
}

type Router struct {
	exactMap    map[string]string          // signal → pipe name
	index       bleve.Index                // BM25 scoring index
	definitions map[string]pipe.Definition // pipe name → definition
	pipeTerms   map[string]map[string]bool // pipe → stemmed terms (for dampening)
	allTerms    map[string]bool            // union of all stemmed terms (for miss logging)
	logger      *slog.Logger
}

// pipeDoc is the document type indexed in bleve.
type pipeDoc struct {
	Content string `json:"content"`
}

func NewRouter(defs []pipe.Definition, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Router{
		exactMap:    make(map[string]string),
		definitions: make(map[string]pipe.Definition),
		pipeTerms:   make(map[string]map[string]bool),
		allTerms:    make(map[string]bool),
		logger:      logger,
	}

	// Build exact match map
	for _, def := range defs {
		r.definitions[def.Name] = def
		for _, exact := range def.Triggers.Exact {
			r.exactMap[parser.CleanToken(strings.ToLower(exact))] = def.Name
		}
	}

	// Precompute routing terms once per pipe for indexing and term tracking
	pipeWords := make(map[string][]string, len(defs))
	for _, def := range defs {
		words := pipeRoutingTerms(def)
		pipeWords[def.Name] = words

		terms := make(map[string]bool, len(words))
		for _, w := range words {
			s := Stem(strings.ToLower(w))
			terms[s] = true
			r.allTerms[s] = true
		}
		r.pipeTerms[def.Name] = terms
	}

	// Build bleve index
	var err error
	r.index, err = buildIndex(defs, pipeWords)
	if err != nil {
		logger.Error("failed to build bleve index, keyword scoring disabled", "error", err)
	}

	logger.Debug("router built", "pipes", len(defs), "terms", len(r.allTerms))
	return r
}

// pipeRoutingTerms returns the vocabulary words that should route to this pipe:
// keywords, source keys, and verb keys whose target matches the pipe name.
func pipeRoutingTerms(def pipe.Definition) []string {
	terms := make([]string, 0, len(def.Triggers.Keywords))
	terms = append(terms, def.Triggers.Keywords...)
	for word := range def.Vocabulary.Sources {
		terms = append(terms, word)
	}
	for word, targets := range def.Vocabulary.Verbs {
		for _, target := range targets {
			if target == def.Name || strings.HasPrefix(target, def.Name+".") {
				terms = append(terms, word)
				break
			}
		}
	}
	return terms
}

// buildIndex creates an in-memory bleve index with one document per pipe.
// Each document's content is composed of routing terms and description.
// Uses the English analyzer (unicode tokenize → lowercase → stop words → Porter stem)
// for BM25 scoring.
func buildIndex(defs []pipe.Definition, pipeWords map[string][]string) (bleve.Index, error) {
	indexMapping := bleve.NewIndexMapping()

	// Use English analyzer for BM25 with stemming and stop words
	docMapping := mapping.NewDocumentMapping()
	contentField := mapping.NewTextFieldMapping()
	contentField.Analyzer = "en"
	docMapping.AddFieldMappingsAt("content", contentField)
	indexMapping.DefaultMapping = docMapping
	indexMapping.DefaultAnalyzer = "en"

	index, err := bleve.NewMemOnly(indexMapping)
	if err != nil {
		return nil, err
	}

	for _, def := range defs {
		parts := append([]string(nil), pipeWords[def.Name]...)
		if def.Description != "" {
			parts = append(parts, def.Description)
		}
		doc := pipeDoc{Content: strings.Join(parts, " ")}
		if err := index.Index(def.Name, doc); err != nil {
			return nil, err
		}
	}

	return index, nil
}

func (r *Router) Route(_ context.Context, signal string, parsed parser.ParsedSignal) RouteResult {
	lower := strings.ToLower(signal)

	// Layer 1: Exact match (punctuation stripped at index time via CleanToken)
	if pipeName, ok := r.exactMap[parser.CleanToken(lower)]; ok {
		r.logger.Info("routed", "pipe", pipeName, "layer", LayerExact)
		return RouteResult{Pipe: pipeName, Confidence: 1.0, Layer: LayerExact}
	}

	// Tokenize once for layers 2 and 4
	words := tokenize(lower)
	scoringWords := nlp.Filter(words)

	// Layer 2: BM25 keyword scoring via bleve
	if r.index != nil {
		bestPipe, confidence := r.searchBM25(lower, scoringWords, parsed)
		if bestPipe != "" {
			r.logger.Info("routed", "pipe", bestPipe, "layer", LayerKeyword, "confidence", confidence)
			return RouteResult{Pipe: bestPipe, Confidence: confidence, Layer: LayerKeyword}
		}
	}

	// Layer 3: Parsed verb / source matching
	if !parsed.IsQuestion && parsed.Verb != "" {
		if _, ok := r.definitions[parsed.Verb]; ok {
			r.logger.Info("routed", "pipe", parsed.Verb, "layer", LayerCategory)
			return RouteResult{Pipe: parsed.Verb, Confidence: 0.8, Layer: LayerCategory}
		}
	}

	// Source-based routing for all signals including questions
	if parsed.Source != "" {
		if _, ok := r.definitions[parsed.Source]; ok {
			r.logger.Info("routed", "pipe", parsed.Source, "layer", LayerCategory)
			return RouteResult{Pipe: parsed.Source, Confidence: 0.8, Layer: LayerCategory}
		}
	}

	// Layer 4: Fallback — return chat and let the server handle AI planning
	return r.buildFallback(lower, scoringWords)
}

// searchBM25 queries the bleve index with the signal and returns the best matching
// pipe name and a normalized confidence score. Returns ("", 0) if no match qualifies.
func (r *Router) searchBM25(lower string, scoringWords []string, parsed parser.ParsedSignal) (string, float64) {
	query := bleve.NewMatchQuery(lower)
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 3

	results, err := r.index.Search(searchReq)
	if err != nil || results.Total == 0 {
		return "", 0
	}

	top := results.Hits[0]
	score := top.Score

	// Normalize BM25 score to [0, 1) using sigmoid: score / (score + k)
	// k=1 means a BM25 score of 1.0 maps to confidence 0.5
	confidence := score / (score + 1.0)

	// Question dampening: for questions, require stronger evidence.
	// Count how many of the user's meaningful words hit the top pipe's terms.
	if parsed.IsQuestion && len(scoringWords) > 1 {
		hits := r.countPipeHits(scoringWords, top.ID)
		if hits < 2 {
			r.logger.Debug("question dampened", "best", top.ID, "hits", hits, "scoring_words", len(scoringWords))
			return "", 0
		}
	}

	// Require minimum confidence
	if confidence < 0.3 {
		return "", 0
	}

	return top.ID, confidence
}

// countPipeHits counts how many scoring words match the given pipe's term set.
func (r *Router) countPipeHits(scoringWords []string, pipeName string) int {
	terms := r.pipeTerms[pipeName]
	hits := 0
	for _, w := range scoringWords {
		stemmed := Stem(w)
		if terms[stemmed] {
			hits++
		}
	}
	return hits
}

// buildFallback constructs a Layer 4 fallback result with miss metadata.
func (r *Router) buildFallback(lower string, scoringWords []string) RouteResult {
	var keywordsFound []string
	var keywordsNotFound []string
	for _, w := range scoringWords {
		stemmed := Stem(w)
		if r.allTerms[stemmed] {
			keywordsFound = append(keywordsFound, w)
		} else {
			keywordsNotFound = append(keywordsNotFound, w)
		}
	}

	r.logger.Warn("miss", "signal", lower)
	r.logger.Info("routed", "pipe", "chat", "layer", LayerFallback)
	return RouteResult{
		Pipe:             "chat",
		Confidence:       0.0,
		Layer:            LayerFallback,
		KeywordsFound:    keywordsFound,
		KeywordsNotFound: keywordsNotFound,
	}
}

// Close releases the bleve index resources.
func (r *Router) Close() error {
	if r.index != nil {
		return r.index.Close()
	}
	return nil
}

// tokenize splits s into cleaned tokens. Callers must pass a lowercased string.
func tokenize(s string) []string {
	fields := strings.Fields(s)
	for i, f := range fields {
		fields[i] = parser.CleanToken(f)
	}
	return fields
}
