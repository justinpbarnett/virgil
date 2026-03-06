package runtime

import (
	"fmt"
	"strings"
)

// Condition is a parsed condition expression used in pipeline step guards and
// loop until clauses. Deliberately minimal: three operators, no combinators,
// no arithmetic. Complex logic belongs in pipe handlers.
type Condition struct {
	Field    string // dot-path, e.g. "verify.error"
	Operator string // "truthy", "==", or "null"
	Value    string // literal value for equality; empty for truthy/null
}

// ParseCondition parses a condition expression string into a Condition.
//
// Supported forms:
//   - "field"              -> truthy check (present, non-nil, non-empty string)
//   - "field == null"      -> null check (absent or nil)
//   - "field == value"     -> equality check (unquoted or quoted value)
//   - "field == \"value\"" -> equality check with quoted value
//
// Unsupported operators (!=, >, <, >=, <=, &&, ||) produce an error at parse
// time so invalid configs fail fast at pipeline load rather than silently
// misbehaving at runtime.
func ParseCondition(expr string) (Condition, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Condition{}, fmt.Errorf("empty condition expression")
	}

	// Reject unsupported operators before attempting to split.
	for _, op := range []string{" != ", " > ", " < ", " >= ", " <= ", " && ", " || "} {
		if strings.Contains(expr, op) {
			return Condition{}, fmt.Errorf("unsupported operator in condition: %q", expr)
		}
	}

	parts := strings.SplitN(expr, " == ", 2)
	field := strings.TrimSpace(parts[0])

	if field == "" {
		return Condition{}, fmt.Errorf("empty field in condition: %q", expr)
	}

	if len(parts) == 1 {
		// No == found: truthy check.
		return Condition{Field: field, Operator: "truthy"}, nil
	}

	value := strings.TrimSpace(parts[1])
	// Strip surrounding double quotes from value.
	value = strings.Trim(value, "\"")

	if value == "null" {
		return Condition{Field: field, Operator: "null"}, nil
	}

	return Condition{Field: field, Operator: "==", Value: value}, nil
}

// Evaluate evaluates the condition against a pipeline context map.
// Returns true if the condition is satisfied.
//
// The context map stores step outputs under dot-path keys, e.g.:
//   - ctx["verify.error"]  = *EnvelopeError (nil when verify passed)
//   - ctx["verify.passed"] = bool
//   - ctx["loop.iteration"] = int (current 1-indexed iteration)
func (c Condition) Evaluate(ctx map[string]any) bool {
	val, exists := ctx[c.Field]

	switch c.Operator {
	case "truthy":
		if !exists || val == nil {
			return false
		}
		if s, ok := val.(string); ok {
			return s != ""
		}
		return true

	case "null":
		return !exists || val == nil

	case "==":
		if !exists || val == nil {
			return false
		}
		return fmt.Sprintf("%v", val) == c.Value

	default:
		return false
	}
}
