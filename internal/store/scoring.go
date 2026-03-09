package store

import (
	"math"
	"time"
)

// ScoredMemory pairs a memory with its composite retrieval score.
type ScoredMemory struct {
	Memory
	Score       float64
	HopDistance int    // 0 = direct match, 1 = 1-hop, 2 = 2-hop
	SourceType  string // context request type that produced this result
}

// Scoring weights. Initial values — tune via real usage.
const (
	WeightRelevance      = 0.4
	WeightRecency        = 0.3
	WeightConfidence     = 0.2
	WeightGraphProximity = 0.1

	RecencyHalfLifeDays = 7.0
)

// Default relevance values by source type. Initial guesses -- tune empirically.
const (
	RelevanceTopicHistory  = 1.0 // BM25-matched, overwritten with normalized rank when available
	RelevanceWorkingState  = 0.8
	RelevanceUserPrefs     = 0.7
	RelevanceRecentHistory = 0.5
	RelevanceRelational    = 0.4
)

// Default confidence by memory kind, used at write time and as fallback at read time.
const (
	ConfidenceExplicit     = 0.9
	ConfidenceWorkingState = 0.7
	ConfidenceInvocation   = 0.5
)

// ComputeScore calculates the composite retrieval score.
func (sm *ScoredMemory) ComputeScore(relevance float64) {
	recency := recencyScore(sm.CreatedAt)
	proximity := graphProximityScore(sm.HopDistance)
	sm.Score = (relevance * WeightRelevance) +
		(recency * WeightRecency) +
		(sm.Confidence * WeightConfidence) +
		(proximity * WeightGraphProximity)
}

// defaultRelevance returns the default relevance value for a given source type.
func defaultRelevance(sourceType string) float64 {
	switch sourceType {
	case "topic_history":
		return RelevanceTopicHistory
	case "working_state":
		return RelevanceWorkingState
	case "user_preferences":
		return RelevanceUserPrefs
	case "recent_history":
		return RelevanceRecentHistory
	case "relational":
		return RelevanceRelational
	default:
		return 0.5
	}
}

// recencyScore returns an exponential decay value in [0, 1].
// 1.0 at now, 0.5 at halfLife days ago, approaching 0 for old memories.
func recencyScore(created time.Time) float64 {
	age := time.Since(created).Hours() / 24.0
	if age < 0 {
		age = 0
	}
	return math.Exp(-0.693 * age / RecencyHalfLifeDays) // ln(2) ≈ 0.693
}

// graphProximityScore maps hop distance to a proximity value.
func graphProximityScore(hops int) float64 {
	switch hops {
	case 0:
		return 1.0
	case 1:
		return 0.5
	case 2:
		return 0.25
	default:
		return 0.1
	}
}
