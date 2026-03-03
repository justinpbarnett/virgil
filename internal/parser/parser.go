package parser

import (
	"strings"
)

type ParsedSignal struct {
	Verb       string // pipe name (resolved from vocabulary)
	Action     string // extracted from pipe.action mapping, empty otherwise
	Type       string
	Source     string
	Modifier   string
	Topic      string
	Raw        string
	IsQuestion bool // true when signal is a wh-question, not a command
}

type Parser struct {
	vocab *Vocabulary
}

func New(vocab *Vocabulary) *Parser {
	return &Parser{vocab: vocab}
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "my": true,
	"on": true, "in": true, "at": true, "to": true,
	"for": true, "of": true, "is": true, "that": true,
	"about": true, "do": true, "i": true, "it": true,
	"me": true, "and": true, "or": true, "but": true,
	"with": true, "from": true, "post": true,
	"what": true, "what's": true, "how": true, "when": true, "where": true,
	"can": true, "does": true, "did": true, "will": true,
	"you": true, "could": true, "would": true, "should": true,
	"are": true, "was": true, "were": true, "has": true, "have": true,
}

// whWords are wh-question starters used to detect question signals.
var whWords = map[string]bool{
	"what": true, "what's": true, "how": true, "when": true,
	"where": true, "who": true, "why": true, "which": true,
}

// interrogativeWords are words that, when appearing before a verb,
// indicate the signal is a question rather than a command.
var interrogativeWords = map[string]bool{
	"do": true, "does": true, "did": true,
	"can": true, "could": true,
	"would": true, "should": true, "will": true,
	"is": true, "are": true, "was": true, "were": true,
	"has": true, "have": true,
}

// CleanToken strips trailing punctuation from a word.
func CleanToken(s string) string {
	return strings.TrimRight(s, ".,?!;:")
}

func (p *Parser) Parse(signal string) ParsedSignal {
	signal = strings.TrimSpace(signal)
	result := ParsedSignal{Raw: signal}
	lower := strings.ToLower(signal)
	words := strings.Fields(lower)
	for i, w := range words {
		words[i] = CleanToken(w)
	}
	used := make([]bool, len(words))

	// Detect wh-questions: starts with a wh-word and ends with "?"
	if len(words) > 0 && whWords[words[0]] && strings.HasSuffix(strings.TrimSpace(lower), "?") {
		result.IsQuestion = true
	}

	// Try multi-word modifiers first
	for phrase, canonical := range p.vocab.Modifiers {
		phraseLower := strings.ToLower(phrase)
		if strings.Contains(lower, phraseLower) {
			result.Modifier = canonical
			// Mark words as used
			phraseWords := strings.Fields(phraseLower)
			for i := 0; i <= len(words)-len(phraseWords); i++ {
				match := true
				for j, pw := range phraseWords {
					if words[i+j] != pw {
						match = false
						break
					}
				}
				if match {
					for j := range phraseWords {
						used[i+j] = true
					}
					break
				}
			}
			break
		}
	}

	// Extract verb (first match wins)
	verbIdx := -1
	for i, w := range words {
		if used[i] {
			continue
		}
		if mapping, ok := p.vocab.Verbs[w]; ok {
			if strings.Contains(mapping, ".") {
				parts := strings.SplitN(mapping, ".", 2)
				result.Verb = parts[0]
				result.Action = parts[1]
			} else {
				result.Verb = mapping
			}
			used[i] = true
			verbIdx = i
			break
		}
	}

	// Interrogative detection: if the signal is a question and the action
	// would be "store", flip to "retrieve". Catches patterns like
	// "do you remember X?" where "remember" maps to store but the intent
	// is retrieval.
	if result.Action == "store" {
		interrogative := false
		if verbIdx > 0 {
			for j := 0; j < verbIdx; j++ {
				if interrogativeWords[words[j]] {
					interrogative = true
					break
				}
			}
		}
		if !interrogative && strings.HasSuffix(lower, "?") {
			interrogative = true
		}
		if interrogative {
			result.Action = "retrieve"
		}
	}

	// Extract type
	for i, w := range words {
		if used[i] {
			continue
		}
		if canonical, ok := p.vocab.Types[w]; ok {
			result.Type = canonical
			used[i] = true
			break
		}
	}

	// Extract source
	for i, w := range words {
		if used[i] {
			continue
		}
		if canonical, ok := p.vocab.Sources[w]; ok {
			result.Source = canonical
			used[i] = true
			break
		}
	}

	// Extract single-word modifier if not already found
	if result.Modifier == "" {
		for i, w := range words {
			if used[i] {
				continue
			}
			if canonical, ok := p.vocab.Modifiers[w]; ok {
				result.Modifier = canonical
				used[i] = true
				break
			}
		}
	}

	// Topic: remaining words after removing stop words and used words
	var topicWords []string
	for i, w := range words {
		if used[i] {
			continue
		}
		if stopWords[w] {
			continue
		}
		topicWords = append(topicWords, w)
	}
	result.Topic = strings.Join(topicWords, " ")

	return result
}
