package router

import "github.com/justinpbarnett/virgil/internal/nlp"

// Stem reduces a lowercase word to its Porter2 stem using the Snowball algorithm.
func Stem(word string) string {
	return nlp.Stem(word)
}
