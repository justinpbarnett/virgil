package parser

import (
	"strings"

	"github.com/justinpbarnett/virgil/internal/nlp"
)

type ParsedSignal struct {
	Verb       string            // pipe name (resolved from vocabulary) - empty if ambiguous
	Action     string            // extracted from pipe.action mapping, empty otherwise
	Verbs      []string          // all matching verbs (when ambiguous)
	Actions    map[string]string // verb -> action mapping for all matches
	Type       string
	Types      []string // all matching types (when ambiguous)
	Source     string
	Sources    []string // all matching sources (when ambiguous)
	Modifier   string
	Modifiers  []string // all matching modifiers (when ambiguous)
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
	result := ParsedSignal{
		Raw:     signal,
		Actions: make(map[string]string),
	}
	lower := strings.ToLower(signal)
	words := tokenize(lower)
	used := make([]bool, len(words))

	result.IsQuestion = detectQuestion(words, lower)
	result.Modifiers, used = p.extractModifiers(words, lower, used)
	result.Verbs, result.Actions, used = p.extractVerbs(words, used)
	used = p.consumeVerbEchoes(words, used, result.Verbs)
	result.Type, result.Types, used = p.extractType(words, used)
	result.Source, result.Sources, used = p.extractSource(words, used)
	result.Topic = extractTopic(words, used)

	// If only one verb match, set the singular fields
	if len(result.Verbs) == 1 {
		result.Verb = result.Verbs[0]
		result.Action = result.Actions[result.Verb]
	}

	// If single modifier match, also set singular
	if len(result.Modifiers) == 1 {
		result.Modifier = result.Modifiers[0]
	}

	// Interrogative detection: flip "store" to "retrieve" in questions
	result.Action = maybeFlipToRetrieve(result.Action, words, lower)

	return result
}

func tokenize(lower string) []string {
	words := strings.Fields(lower)
	for i, w := range words {
		words[i] = CleanToken(w)
	}
	return words
}

func detectQuestion(words []string, lower string) bool {
	return len(words) > 0 && whWords[words[0]] && strings.HasSuffix(strings.TrimSpace(lower), "?")
}

func (p *Parser) extractModifiers(words []string, lower string, used []bool) ([]string, []bool) {
	var modifiers []string

	// Try multi-word modifiers first
	for phrase, canonicalList := range p.vocab.Modifiers {
		phraseLower := strings.ToLower(phrase)
		if !strings.Contains(lower, phraseLower) {
			continue
		}
		modifiers = append(modifiers, canonicalList...)
		phraseWords := strings.Fields(phraseLower)
		for i := 0; i <= len(words)-len(phraseWords); i++ {
			if matchPhrase(words, i, phraseWords) {
				for j := range phraseWords {
					used[i+j] = true
				}
				break
			}
		}
		break
	}

	// If no multi-word match, try single-word modifiers
	if len(modifiers) == 0 {
		for i, w := range words {
			if used[i] {
				continue
			}
			if canonicalList, ok := p.vocab.Modifiers[w]; ok {
				modifiers = append(modifiers, canonicalList...)
				used[i] = true
				break
			}
		}
	}

	return modifiers, used
}

func matchPhrase(words []string, start int, phraseWords []string) bool {
	for j, pw := range phraseWords {
		if words[start+j] != pw {
			return false
		}
	}
	return true
}

func (p *Parser) extractVerbs(words []string, used []bool) ([]string, map[string]string, []bool) {
	verbs := []string{}
	actions := make(map[string]string)

	for i, w := range words {
		if used[i] {
			continue
		}
		mappings, ok := nlp.LookupStemmedList(p.vocab.Verbs, w)
		if !ok {
			continue
		}
		for _, mapping := range mappings {
			pipeName, action := splitMapping(mapping)
			verbs = append(verbs, pipeName)
			if _, exists := actions[pipeName]; !exists {
				actions[pipeName] = action
			}
		}
		used[i] = true
		break
	}

	return verbs, actions, used
}

// consumeVerbEchoes marks remaining words as used if they are verb-vocabulary
// entries that map to the same pipe(s) as the already-extracted verb. This
// prevents words like "done" from leaking into the topic when "mark" already
// established the action (e.g. "mark X as done").
func (p *Parser) consumeVerbEchoes(words []string, used []bool, matchedVerbs []string) []bool {
	if len(matchedVerbs) == 0 {
		return used
	}
	pipeSet := make(map[string]bool, len(matchedVerbs))
	for _, v := range matchedVerbs {
		pipeSet[v] = true
	}
	for i, w := range words {
		if used[i] {
			continue
		}
		mappings, ok := nlp.LookupStemmedList(p.vocab.Verbs, w)
		if !ok {
			continue
		}
		for _, mapping := range mappings {
			pipeName, _ := splitMapping(mapping)
			if pipeSet[pipeName] {
				used[i] = true
				break
			}
		}
	}
	return used
}

func splitMapping(mapping string) (string, string) {
	if idx := strings.Index(mapping, "."); idx >= 0 {
		return mapping[:idx], mapping[idx+1:]
	}
	return mapping, ""
}

func (p *Parser) extractType(words []string, used []bool) (string, []string, []bool) {
	return p.extractVocabMatches(words, used, p.vocab.Types)
}

func (p *Parser) extractSource(words []string, used []bool) (string, []string, []bool) {
	return p.extractVocabMatches(words, used, p.vocab.Sources)
}

func (p *Parser) extractVocabMatches(words []string, used []bool, vocab map[string][]string) (string, []string, []bool) {
	var matches []string
	for i, w := range words {
		if used[i] {
			continue
		}
		canonicalList, ok := nlp.LookupStemmedList(vocab, w)
		if !ok {
			continue
		}
		matches = append(matches, canonicalList...)
		used[i] = true
		break
	}

	singular := ""
	if len(matches) == 1 {
		singular = matches[0]
	}
	return singular, matches, used
}

func extractTopic(words []string, used []bool) string {
	var topicWords []string
	for i, w := range words {
		if used[i] || nlp.IsStopWord(w) {
			continue
		}
		topicWords = append(topicWords, w)
	}
	return strings.Join(topicWords, " ")
}

func maybeFlipToRetrieve(action string, words []string, lower string) string {
	if action != "store" {
		return action
	}
	if detectInterrogative(words, lower) {
		return "retrieve"
	}
	return action
}

func detectInterrogative(words []string, lower string) bool {
	// Check for interrogative words before the verb
	for j := 0; j < len(words) && j < 3; j++ {
		if interrogativeWords[words[j]] {
			return true
		}
	}
	// Or just check if it ends with a question mark
	return strings.HasSuffix(lower, "?")
}
