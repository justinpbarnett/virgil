package nlp

import "github.com/kljensen/snowball"

// Stem reduces a lowercase word to its Porter2 stem using the Snowball algorithm.
func Stem(word string) string {
	if stemmed, err := snowball.Stem(word, "english", false); err == nil {
		return stemmed
	}
	return word
}

// LookupStemmedList looks up word in m, falling back to its Porter2 stem.
// Returns all matching values (slice) or nil if not found.
func LookupStemmedList(m map[string][]string, word string) ([]string, bool) {
	if v, ok := m[word]; ok {
		return v, true
	}
	if stemmed := Stem(word); stemmed != word {
		if v, ok := m[stemmed]; ok {
			return v, true
		}
	}
	return nil, false
}

// stopWords contains common English stop words plus application-specific
// entries. Both the parser and router use this shared set for token filtering.
var stopWords = map[string]bool{
	// articles / determiners
	"a": true, "an": true, "the": true, "this": true, "that": true,
	"these": true, "those": true, "some": true, "any": true, "all": true,
	"each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "other": true, "no": true, "nor": true, "not": true,
	"such": true, "own": true, "same": true,

	// pronouns
	"i": true, "me": true, "my": true, "myself": true,
	"you": true, "your": true, "yours": true, "yourself": true,
	"he": true, "him": true, "his": true, "himself": true,
	"she": true, "her": true, "hers": true, "herself": true,
	"it": true, "its": true, "itself": true,
	"we": true, "us": true, "our": true, "ours": true, "ourselves": true,
	"they": true, "them": true, "their": true, "theirs": true, "themselves": true,
	"who": true, "whom": true, "which": true,

	// prepositions / adverbs
	"on": true, "in": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "from": true, "about": true, "by": true, "as": true,
	"into": true, "through": true, "during": true, "before": true, "after": true,
	"above": true, "below": true, "between": true, "under": true, "over": true,
	"against": true, "up": true, "down": true, "out": true, "off": true,
	"again": true, "further": true, "then": true, "once": true,
	"here": true, "there": true, "where": true, "when": true, "how": true,
	"what": true, "what's": true, "why": true,

	// conjunctions
	"and": true, "or": true, "but": true, "if": true, "because": true,
	"until": true, "while": true, "so": true, "than": true, "too": true,

	// be / have / do
	"am": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true,
	"has": true, "have": true, "had": true, "having": true,
	"do": true, "does": true, "did": true, "doing": true,

	// modals
	"can": true, "could": true, "will": true, "would": true,
	"should": true, "shall": true, "may": true, "might": true, "must": true,

	// adverbs / fillers
	"just": true, "very": true, "also": true, "only": true, "now": true,
	"already": true, "still": true, "even": true, "right": true,
	"please": true, "hey": true, "ok": true, "well": true, "like": true,

	// contractions
	"i'm": true, "i'll": true, "i've": true, "i'd": true,
	"you're": true, "you'll": true, "you've": true, "you'd": true,
	"he's": true, "she's": true, "it's": true, "that's": true,
	"we're": true, "we'll": true, "we've": true, "we'd": true,
	"they're": true, "they'll": true, "they've": true, "they'd": true,
	"there's": true, "here's": true, "where's": true, "how's": true,
	"who's": true, "when's": true, "why's": true,
	"don't": true, "doesn't": true, "didn't": true, "won't": true,
	"wouldn't": true, "shouldn't": true, "couldn't": true, "can't": true,
	"isn't": true, "aren't": true, "wasn't": true, "weren't": true,
	"hasn't": true, "haven't": true, "hadn't": true,

	// application-specific
	"virgil": true, "post": true,
}

// IsStopWord reports whether w is a stop word.
func IsStopWord(w string) bool {
	return stopWords[w]
}

// Filter returns tokens that are not stop words.
func Filter(words []string) []string {
	result := make([]string, 0, len(words))
	for _, w := range words {
		if !stopWords[w] {
			result = append(result, w)
		}
	}
	return result
}
