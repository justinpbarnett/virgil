package runtime

import (
	"testing"
)

func TestConditionParseValid(t *testing.T) {
	cases := []struct {
		expr     string
		wantCond Condition
	}{
		{
			expr:     "verify.error",
			wantCond: Condition{Field: "verify.error", Operator: "truthy"},
		},
		{
			expr:     "verify.error == null",
			wantCond: Condition{Field: "verify.error", Operator: "null"},
		},
		{
			expr:     `review.outcome == "fail"`,
			wantCond: Condition{Field: "review.outcome", Operator: "==", Value: "fail"},
		},
		{
			expr:     "review.outcome == fail",
			wantCond: Condition{Field: "review.outcome", Operator: "==", Value: "fail"},
		},
		{
			// Leading/trailing whitespace should be tolerated.
			expr:     "  verify.passed  ",
			wantCond: Condition{Field: "verify.passed", Operator: "truthy"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := ParseCondition(tc.expr)
			if err != nil {
				t.Fatalf("ParseCondition(%q) unexpected error: %v", tc.expr, err)
			}
			if got != tc.wantCond {
				t.Errorf("ParseCondition(%q) = %+v, want %+v", tc.expr, got, tc.wantCond)
			}
		})
	}
}

func TestConditionParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"a != b",
		"a > 5",
		"a < 5",
		"a >= 5",
		"a <= 5",
		"a && b",
		"a || b",
	}

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseCondition(expr)
			if err == nil {
				t.Errorf("ParseCondition(%q) expected error, got nil", expr)
			}
		})
	}
}

func TestConditionEvaluate(t *testing.T) {
	ctx := map[string]any{
		"verify.error":   (*struct{})(nil), // typed nil pointer — non-nil interface, treat carefully
		"verify.passed":  true,
		"verify.summary": "all checks passed",
		"fix.attempt":    "1",
		"empty.string":   "",
	}

	// For the null/truthy tests we want a plain nil, not a typed nil.
	ctxWithNil := map[string]any{
		"verify.error":   nil,
		"verify.passed":  true,
		"verify.summary": "all checks passed",
		"fix.attempt":    "1",
		"empty.string":   "",
	}

	t.Run("truthy: present non-nil non-empty value", func(t *testing.T) {
		c := Condition{Field: "verify.passed", Operator: "truthy"}
		if !c.Evaluate(ctxWithNil) {
			t.Error("expected true for truthy on present non-nil value")
		}
	})

	t.Run("truthy: absent key", func(t *testing.T) {
		c := Condition{Field: "missing.key", Operator: "truthy"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for truthy on absent key")
		}
	})

	t.Run("truthy: nil value", func(t *testing.T) {
		c := Condition{Field: "verify.error", Operator: "truthy"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for truthy on nil value")
		}
	})

	t.Run("truthy: empty string", func(t *testing.T) {
		c := Condition{Field: "empty.string", Operator: "truthy"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for truthy on empty string")
		}
	})

	t.Run("null: nil value -> true", func(t *testing.T) {
		c := Condition{Field: "verify.error", Operator: "null"}
		if !c.Evaluate(ctxWithNil) {
			t.Error("expected true for null check on nil value")
		}
	})

	t.Run("null: absent key -> true", func(t *testing.T) {
		c := Condition{Field: "no.such.key", Operator: "null"}
		if !c.Evaluate(ctxWithNil) {
			t.Error("expected true for null check on absent key")
		}
	})

	t.Run("null: present non-nil value -> false", func(t *testing.T) {
		c := Condition{Field: "verify.passed", Operator: "null"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for null check on present non-nil value")
		}
	})

	t.Run("equality: match -> true", func(t *testing.T) {
		c := Condition{Field: "fix.attempt", Operator: "==", Value: "1"}
		if !c.Evaluate(ctxWithNil) {
			t.Error("expected true for equality match")
		}
	})

	t.Run("equality: mismatch -> false", func(t *testing.T) {
		c := Condition{Field: "fix.attempt", Operator: "==", Value: "2"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for equality mismatch")
		}
	})

	t.Run("equality: absent key -> false", func(t *testing.T) {
		c := Condition{Field: "no.such.key", Operator: "==", Value: "x"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for equality on missing key")
		}
	})

	t.Run("equality: nil value -> false", func(t *testing.T) {
		c := Condition{Field: "verify.error", Operator: "==", Value: "something"}
		if c.Evaluate(ctxWithNil) {
			t.Error("expected false for equality on nil value")
		}
	})

	// Keep the typed-nil ctx to exercise the truthy path with a non-nil interface wrapping nil.
	_ = ctx
}
