package parser

import (
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
)

func testVocab() *Vocabulary {
	return LoadVocabulary(config.VocabularyConfig{
		Verbs: map[string]string{
			"draft":    "draft",
			"write":    "draft",
			"compose":  "draft",
			"remember": "memory.store",
			"recall":   "memory.retrieve",
			"know":     "memory.retrieve",
			"check":    "calendar",
			"show":     "calendar",
		},
		Types: map[string]string{
			"blog":  "blog",
			"email": "email",
			"pr":    "pr",
			"memo":  "memo",
		},
		Sources: map[string]string{
			"notes":    "memory",
			"calendar": "calendar",
			"memory":   "memory",
		},
		Modifiers: map[string]string{
			"recent":    "recent",
			"today":     "today",
			"tomorrow":  "tomorrow",
			"this week": "this-week",
		},
	})
}

func TestParseDraftBlogPost(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("draft a blog post about OAuth")

	if result.Verb != "draft" {
		t.Errorf("expected verb=draft, got %s", result.Verb)
	}
	if result.Action != "" {
		t.Errorf("expected action empty, got %s", result.Action)
	}
	if result.Type != "blog" {
		t.Errorf("expected type=blog, got %s", result.Type)
	}
	if result.Topic != "oauth" {
		t.Errorf("expected topic=oauth, got '%s'", result.Topic)
	}
}

func TestParseCalendarToday(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("what's on my calendar today")

	// "what's" is a stop word, not a verb — calendar routing comes from
	// source=calendar + exact matches in triggers, not from vocabulary verbs.
	if result.Verb != "" {
		t.Errorf("expected verb empty, got %s", result.Verb)
	}
	if result.Source != "calendar" {
		t.Errorf("expected source=calendar, got %s", result.Source)
	}
	if result.Modifier != "today" {
		t.Errorf("expected modifier=today, got %s", result.Modifier)
	}
}

func TestParseRemember(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("remember that OAuth uses short-lived tokens")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "store" {
		t.Errorf("expected action=store, got %s", result.Action)
	}
	if result.Topic == "" {
		t.Error("expected non-empty topic")
	}
}

func TestParseKnowAbout(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("what do I know about OAuth")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", result.Action)
	}
	if result.Topic != "oauth" {
		t.Errorf("expected topic=oauth, got '%s'", result.Topic)
	}
}

func TestParseUnrecognized(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("xyzzy foobar nonsense")

	if result.Verb != "" {
		t.Errorf("expected empty verb, got %s", result.Verb)
	}
	if result.Type != "" {
		t.Errorf("expected empty type, got %s", result.Type)
	}
	if result.Topic == "" {
		t.Error("expected non-empty topic for unrecognized signal")
	}
}

func TestParseWithSource(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("draft a blog post about my notes on OAuth")

	if result.Verb != "draft" {
		t.Errorf("expected verb=draft, got %s", result.Verb)
	}
	if result.Type != "blog" {
		t.Errorf("expected type=blog, got %s", result.Type)
	}
	if result.Source != "memory" {
		t.Errorf("expected source=memory, got %s", result.Source)
	}
}

func TestParseCaseInsensitive(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("Draft A Blog Post About OAuth")

	if result.Verb != "draft" {
		t.Errorf("expected verb=draft, got %s", result.Verb)
	}
	if result.Type != "blog" {
		t.Errorf("expected type=blog, got %s", result.Type)
	}
}

func TestParseInterrogativeRemember(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("do you remember what my name is?")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve (interrogative), got %s", result.Action)
	}
}

func TestParseInterrogativeRememberNoQuestionMark(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("can you remember my birthday")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve (interrogative), got %s", result.Action)
	}
}

func TestParseImperativeRemember(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("remember that my name is Justin")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "store" {
		t.Errorf("expected action=store (imperative), got %s", result.Action)
	}
}

func TestParseQuestionMarkFlipsStore(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("remember my name?")

	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve (question mark), got %s", result.Action)
	}
}

func TestParsePunctuationStripped(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("what do I know about OAuth?")

	if result.Verb != "memory" {
		t.Errorf("expected verb=memory, got %s", result.Verb)
	}
	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", result.Action)
	}
	if result.Topic != "oauth" {
		t.Errorf("expected topic=oauth, got '%s'", result.Topic)
	}
}

func TestParseRawPreserved(t *testing.T) {
	p := New(testVocab())
	result := p.Parse("Draft A Blog Post")

	if result.Raw != "Draft A Blog Post" {
		t.Errorf("expected raw to be preserved, got '%s'", result.Raw)
	}
}
