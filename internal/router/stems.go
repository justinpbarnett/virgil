package router

import "strings"

// synonyms maps common paraphrases to keywords that exist in pipe trigger lists.
// This allows signals like "what's on my agenda" to score the calendar pipe
// even though "agenda" is not a registered keyword.
var synonyms = map[string]string{
	"agenda":      "calendar",
	"appointment": "calendar",
	"appt":        "calendar",
	"todo":        "calendar",
	"task":        "calendar",
	"remind":      "memory",
	"note":        "memory",
	"notes":       "memory",
	"save":        "memory",
	"store":       "memory",
	"email":       "mail",
	"msg":         "draft",
	"compose":     "draft",
	"headline":    "news",
	"headlines":   "news",
	"article":     "news",
	"articles":    "news",
	"debug":       "fix",
	"error":       "fix",
	"bug":         "fix",
	"broken":      "fix",
	"build":       "build",
	"compile":     "build",
	"test":        "build",
	"deploy":      "build",
}

// Stem applies a simplified English suffix-stripping algorithm to a lowercase
// word. The goal is to map morphological variants of the same word to a common
// stem so that "scheduling" and "schedule" both match the keyword "schedule".
func Stem(word string) string {
	l := len(word)
	if l <= 3 {
		return word
	}

	w := word
	stripped := false

	switch {
	case l > 6 && strings.HasSuffix(w, "ings"):
		w = w[:l-4]
		stripped = true
	case l > 5 && strings.HasSuffix(w, "ing"):
		w = w[:l-3]
		stripped = true
	case l > 4 && strings.HasSuffix(w, "ied"):
		return w[:l-3] + "y"
	case l > 4 && strings.HasSuffix(w, "ed"):
		w = w[:l-2]
		stripped = true
	case l > 4 && strings.HasSuffix(w, "ies"):
		return w[:l-2]
	case l > 4 && strings.HasSuffix(w, "ers"):
		w = w[:l-3]
		stripped = true
	case l > 3 && strings.HasSuffix(w, "er"):
		w = w[:l-2]
		stripped = true
	case l > 4 && (strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "zes") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "ches")):
		w = w[:l-2]
		stripped = true
	case l > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		// Simple plural — no double-consonant fix needed
		return w[:l-1]
	}

	if stripped {
		// Fix double consonant created by removing the suffix (e.g. "running" → "runn" → "run")
		if len(w) >= 2 && !isVowel(rune(w[len(w)-1])) && w[len(w)-1] == w[len(w)-2] {
			w = w[:len(w)-1]
		}
		// Strip trailing silent 'e' left by suffix removal (e.g. "scheduled" → "schedule" → "schedul")
		if len(w) > 4 && strings.HasSuffix(w, "e") {
			w = w[:len(w)-1]
		}
		return w
	}

	// No suffix removed — strip trailing silent -e when word is long enough
	if l > 4 && strings.HasSuffix(w, "e") {
		return w[:l-1]
	}

	return w
}

func isVowel(c rune) bool {
	return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u'
}

// StemAndExpand returns the stemmed form of word plus any synonym expansions.
// The expansions are the target keywords from the synonyms map (already stemmed
// via the index at build time).
func StemAndExpand(word string) []string {
	stemmed := Stem(word)
	results := []string{stemmed}

	// Check the original word for a synonym mapping
	if target, ok := synonyms[word]; ok && target != stemmed {
		results = append(results, target)
	}
	// Check the stemmed form too
	if target, ok := synonyms[stemmed]; ok && target != stemmed {
		alreadyPresent := false
		for _, r := range results {
			if r == target {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			results = append(results, target)
		}
	}

	return results
}
