package study

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenEstimated reports whether token counts are estimates (true) or exact (false).
// When a tiktoken-compatible Go tokenizer replaces this, set to false.
const tokenEstimated = true

// CountTokens estimates the token count for a string.
// Differentiates between code and prose for better accuracy:
//   - Code averages ~2.5 chars per token (more symbols, short identifiers)
//   - Prose averages ~4.0 chars per token (longer words, fewer symbols)
//
// Uses a heuristic to detect code vs prose based on symbol density.
// Rounds up for conservatism — underestimating risks budget overruns.
func CountTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}

	charsPerToken := estimateCharsPerToken(s)
	tokens := int(float64(n)/charsPerToken + 0.99) // round up
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// estimateCharsPerToken returns the estimated chars-per-token ratio
// based on content characteristics.
func estimateCharsPerToken(s string) float64 {
	if len(s) < 20 {
		return 3.0 // too short to classify
	}

	// Sample the content for code indicators
	symbols := 0
	letters := 0
	sample := s
	if len(sample) > 500 {
		sample = sample[:500]
	}

	for _, r := range sample {
		switch {
		case unicode.IsLetter(r):
			letters++
		case r == '{' || r == '}' || r == '(' || r == ')' || r == '[' || r == ']' ||
			r == ';' || r == '=' || r == '<' || r == '>' || r == '&' || r == '|' ||
			r == '*' || r == '+' || r == '-' || r == '/' || r == ':' || r == '!':
			symbols++
		}
	}

	total := symbols + letters
	if total == 0 {
		return 3.0
	}

	symbolRatio := float64(symbols) / float64(total)

	// Also check for common code patterns
	hasCodeLines := strings.Contains(sample, "func ") || strings.Contains(sample, "def ") ||
		strings.Contains(sample, "class ") || strings.Contains(sample, "import ") ||
		strings.Contains(sample, "return ") || strings.Contains(sample, "if ") ||
		strings.Contains(sample, ":=") || strings.Contains(sample, "->")

	switch {
	case symbolRatio > 0.15 || hasCodeLines:
		return 2.5 // code-heavy
	case symbolRatio > 0.08:
		return 3.0 // mixed
	default:
		return 4.0 // prose-heavy
	}
}

// FitsInBudget returns true if adding tokenCount to used stays within budget.
func FitsInBudget(used, tokenCount, budget int) bool {
	return used+tokenCount <= budget
}
