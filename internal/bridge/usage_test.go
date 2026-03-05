package bridge

import "testing"

func TestCostForKnownModel(t *testing.T) {
	// claude-sonnet-4-20250514: $3/MTok input, $15/MTok output
	// 1M input + 1M output = $3 + $15 = $18
	cost := CostFor("claude-sonnet-4-20250514", 1_000_000, 1_000_000)
	if cost != 18.00 {
		t.Errorf("expected 18.00, got %f", cost)
	}
}

func TestCostForUnknownModel(t *testing.T) {
	cost := CostFor("unknown-model-xyz", 1_000_000, 1_000_000)
	if cost != 0.0 {
		t.Errorf("expected 0.0 for unknown model, got %f", cost)
	}
}

func TestCostForZeroTokens(t *testing.T) {
	cost := CostFor("gpt-4o", 0, 0)
	if cost != 0.0 {
		t.Errorf("expected 0.0 for zero tokens, got %f", cost)
	}
}

func TestCostForInputOnly(t *testing.T) {
	// gpt-4o input: $2.50/MTok
	cost := CostFor("gpt-4o", 1_000_000, 0)
	if cost != 2.50 {
		t.Errorf("expected 2.50, got %f", cost)
	}
}

func TestCostForOutputOnly(t *testing.T) {
	// gpt-4o output: $10.00/MTok
	cost := CostFor("gpt-4o", 0, 1_000_000)
	if cost != 10.00 {
		t.Errorf("expected 10.00, got %f", cost)
	}
}

func TestCostForLargeTokenCounts(t *testing.T) {
	// gpt-4o-mini: $0.15/MTok input, $0.60/MTok output
	// 10M each = $1.50 + $6.00 = $7.50
	cost := CostFor("gpt-4o-mini", 10_000_000, 10_000_000)
	expected := 0.15*10 + 0.60*10
	if cost != expected {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCostForSmallTokenCounts(t *testing.T) {
	// 1000 input + 500 output with gpt-4o
	// (1000/1M)*2.50 + (500/1M)*10 = 0.0025 + 0.005 = 0.0075
	cost := CostFor("gpt-4o", 1000, 500)
	expected := (1000.0/1_000_000)*2.50 + (500.0/1_000_000)*10.00
	if cost != expected {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}
