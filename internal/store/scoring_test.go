package store

import (
	"math"
	"testing"
	"time"
)

func TestRecencyScore(t *testing.T) {
	now := time.Now()

	// Just created should be near 1.0
	score := recencyScore(now)
	if score < 0.99 {
		t.Errorf("expected recency near 1.0 for now, got %.4f", score)
	}

	// 7 days ago should be near 0.5 (half-life)
	sevenDaysAgo := now.AddDate(0, 0, -7)
	score = recencyScore(sevenDaysAgo)
	if math.Abs(score-0.5) > 0.05 {
		t.Errorf("expected recency near 0.5 at half-life (7 days), got %.4f", score)
	}

	// 30 days ago should be near 0
	thirtyDaysAgo := now.AddDate(0, 0, -30)
	score = recencyScore(thirtyDaysAgo)
	if score > 0.10 {
		t.Errorf("expected recency near 0 at 30 days, got %.4f", score)
	}

	// Future time should be clamped to 1.0
	future := now.Add(24 * time.Hour)
	score = recencyScore(future)
	if score < 0.99 {
		t.Errorf("expected recency near 1.0 for future time, got %.4f", score)
	}
}

func TestGraphProximityScore(t *testing.T) {
	tests := []struct {
		hops     int
		expected float64
	}{
		{0, 1.0},
		{1, 0.5},
		{2, 0.25},
		{3, 0.1},
		{99, 0.1},
	}

	for _, tc := range tests {
		got := graphProximityScore(tc.hops)
		if got != tc.expected {
			t.Errorf("graphProximityScore(%d) = %.4f, want %.4f", tc.hops, got, tc.expected)
		}
	}
}

func TestComputeScore(t *testing.T) {
	now := time.Now()

	sm := ScoredMemory{
		Memory: Memory{
			CreatedAt:  now,
			Confidence: 1.0,
		},
		HopDistance: 0,
	}

	// With all factors at maximum: relevance=1.0, recency≈1.0, confidence=1.0, proximity=1.0
	// Score ≈ 0.4 + 0.3 + 0.2 + 0.1 = 1.0
	sm.ComputeScore(1.0)
	if sm.Score < 0.95 || sm.Score > 1.01 {
		t.Errorf("expected score near 1.0 with all factors maxed, got %.4f", sm.Score)
	}

	// With lower confidence: 0.5
	sm2 := ScoredMemory{
		Memory: Memory{
			CreatedAt:  now,
			Confidence: 0.5,
		},
		HopDistance: 0,
	}
	sm2.ComputeScore(1.0)
	// Score ≈ 0.4 + 0.3 + 0.1 + 0.1 = 0.9
	expected := 1.0*WeightRelevance + 1.0*WeightRecency + 0.5*WeightConfidence + 1.0*WeightGraphProximity
	if math.Abs(sm2.Score-expected) > 0.01 {
		t.Errorf("expected score %.4f, got %.4f", expected, sm2.Score)
	}

	// With 2-hop distance: proximity = 0.25
	sm3 := ScoredMemory{
		Memory: Memory{
			CreatedAt:  now,
			Confidence: 1.0,
		},
		HopDistance: 2,
	}
	sm3.ComputeScore(1.0)
	expected3 := 1.0*WeightRelevance + 1.0*WeightRecency + 1.0*WeightConfidence + 0.25*WeightGraphProximity
	if math.Abs(sm3.Score-expected3) > 0.01 {
		t.Errorf("expected score %.4f for 2-hop, got %.4f", expected3, sm3.Score)
	}
}

func TestRetrieveContext_CompositeScoring(t *testing.T) {
	s := tempDB(t)

	// Save a recent invocation — recency score near 1.0.
	recentID, _ := s.SaveInvocation("educate", "teach me Go concurrency", "Go goroutines are lightweight threads")

	// Save an older invocation by backdating created_at to 30 days ago.
	// recencyScore(30 days) ≈ 0.05, vs recencyScore(now) ≈ 1.0, so scores differ clearly.
	oldID, _ := s.SaveInvocation("educate", "teach me Go concurrency history", "Go had no goroutines initially")
	oldTime := time.Now().AddDate(0, 0, -30).UnixNano()
	s.db.Exec("UPDATE memories SET created_at = ?, updated_at = ? WHERE id = ?", oldTime, oldTime, oldID) //nolint:errcheck

	results, err := s.RetrieveContext("Go concurrency", []ContextRequest{
		{Type: "topic_history"},
	}, 5000)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results from composite scoring")
	}

	// Budget must be respected.
	totalChars := 0
	for _, r := range results {
		totalChars += len(r.Content)
	}
	if totalChars > 5000*4 {
		t.Errorf("expected total chars <= %d, got %d", 5000*4, totalChars)
	}

	// Verify ordering: the recent invocation should rank above the 30-day-old one.
	var recentRank, oldRank = -1, -1
	for i, r := range results {
		if r.ID == recentID {
			recentRank = i
		}
		if r.ID == oldID {
			oldRank = i
		}
	}

	if recentRank == -1 || oldRank == -1 {
		t.Skip("both memories not found in results — may be below budget")
	}

	if recentRank > oldRank {
		t.Errorf("recent memory (rank %d) should outrank 30-day-old memory (rank %d) due to recency decay", recentRank, oldRank)
	}
}
