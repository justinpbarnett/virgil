package router

import (
	"context"
	"testing"

	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

func TestStemCommonWords(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"scheduling", "schedul"},
		{"schedule", "schedul"},
		{"meetings", "meet"},
		{"meeting", "meet"},
		{"running", "run"},
		{"drafted", "draft"},
		{"compose", "compos"},
		{"composed", "compos"},
		{"composing", "compos"},
		{"calendars", "calendar"},
		{"builds", "build"},
		{"building", "build"},
		{"categories", "categori"},
		{"fixed", "fix"},
		{"fixing", "fix"},
		{"emails", "email"},
		{"notes", "note"},
	}
	for _, tc := range cases {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStemShortWords(t *testing.T) {
	// Short words (<=3 chars) are returned as-is
	for _, w := range []string{"a", "be", "run", "cat"} {
		if got := Stem(w); got != w {
			t.Errorf("Stem(%q) = %q, want %q (short word unchanged)", w, got, w)
		}
	}
}

func TestStemAndExpandSynonyms(t *testing.T) {
	// "agenda" should expand to include "calendar"
	results := StemAndExpand("agenda")
	found := false
	for _, r := range results {
		if r == "calendar" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("StemAndExpand(\"agenda\") = %v, want to include \"calendar\"", results)
	}
}

func TestStemAndExpandNoSynonym(t *testing.T) {
	// Words without synonyms return just the stem
	results := StemAndExpand("calendar")
	if len(results) == 0 {
		t.Error("StemAndExpand returned empty slice")
	}
	if results[0] != Stem("calendar") {
		t.Errorf("first result should be stem, got %q", results[0])
	}
}

func TestRoutingWithStemming(t *testing.T) {
	// "scheduling" → "schedul" matches calendar keyword "schedule" → "schedul"
	// Signal includes 3 of 4 calendar keywords to exceed 0.6 threshold
	r := NewRouter(testDefs(), nil, nil)
	result := r.Route(context.Background(), "show my scheduling events for the next meeting", parser.ParsedSignal{})
	if result.Pipe != "calendar" {
		t.Errorf("expected calendar for 'scheduling', got %s", result.Pipe)
	}
}

func TestRoutingWithSynonym(t *testing.T) {
	defs := []pipe.Definition{
		{
			Name:     "calendar",
			Category: "time",
			Triggers: pipe.Triggers{
				Keywords: []string{"calendar", "schedule", "meeting"},
			},
		},
		{
			Name:     "chat",
			Category: "general",
			Triggers: pipe.Triggers{Keywords: []string{"chat"}},
		},
	}
	r := NewRouter(defs, nil, nil)

	// "agenda" expands to "calendar" via synonym; "schedule" adds a second hit (2/3 ≥ 0.6)
	result := r.Route(context.Background(), "what's on my agenda and schedule", parser.ParsedSignal{})
	if result.Pipe != "calendar" {
		t.Errorf("expected calendar for 'agenda', got %s", result.Pipe)
	}
}

func TestLayer4DefaultsToChat(t *testing.T) {
	// Without a classifier, Layer 4 must default to chat immediately
	r := NewRouter(testDefs(), nil, nil)
	result := r.Route(context.Background(), "xyzzy absolutely unmatched signal", parser.ParsedSignal{})
	if result.Pipe != "chat" {
		t.Errorf("expected chat fallback, got %s", result.Pipe)
	}
	if result.Layer != LayerFallback {
		t.Errorf("expected LayerFallback, got %d", result.Layer)
	}
}
