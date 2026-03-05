package router

import "github.com/justinpbarnett/virgil/internal/nlp"

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

// Stem reduces a lowercase word to its Porter2 stem using the Snowball algorithm.
func Stem(word string) string {
	return nlp.Stem(word)
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
