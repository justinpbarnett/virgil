package parser

import "github.com/justinpbarnett/virgil/internal/config"

type Vocabulary struct {
	Verbs     map[string]string // word → pipe name or pipe.action
	Types     map[string]string
	Sources   map[string]string
	Modifiers map[string]string
}

func LoadVocabulary(cfg config.VocabularyConfig) *Vocabulary {
	return &Vocabulary{
		Verbs:     cfg.Verbs,
		Types:     cfg.Types,
		Sources:   cfg.Sources,
		Modifiers: cfg.Modifiers,
	}
}
