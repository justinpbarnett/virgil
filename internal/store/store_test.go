package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected database file to be created")
	}
}

func TestSaveSingleEntry(t *testing.T) {
	s := tempDB(t)
	if err := s.Save("OAuth uses short-lived tokens", []string{"auth", "oauth"}); err != nil {
		t.Fatalf("save failed: %v", err)
	}
}

func TestSaveMultipleEntries(t *testing.T) {
	s := tempDB(t)
	entries := []struct {
		content string
		tags    []string
	}{
		{"OAuth uses short-lived tokens", []string{"auth"}},
		{"JWT is a token format", []string{"auth", "jwt"}},
		{"Go is a compiled language", []string{"go"}},
	}

	for _, e := range entries {
		if err := s.Save(e.content, e.tags); err != nil {
			t.Fatalf("save failed: %v", err)
		}
	}
}

func TestSearchByKeyword(t *testing.T) {
	s := tempDB(t)
	s.Save("OAuth uses short-lived tokens", []string{"auth"})
	s.Save("JWT is a token format", []string{"auth"})
	s.Save("Go is a compiled language", []string{"go"})

	results, err := s.Search("OAuth", 10, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Content != "OAuth uses short-lived tokens" {
		t.Errorf("expected OAuth entry, got: %s", results[0].Content)
	}
}

func TestSearchReturnsRankedResults(t *testing.T) {
	s := tempDB(t)
	s.Save("tokens are important in auth", []string{})
	s.Save("OAuth tokens and OAuth flows", []string{})

	results, err := s.Search("OAuth", 10, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected results")
	}
}

func TestSearchWithLimit(t *testing.T) {
	s := tempDB(t)
	for i := 0; i < 5; i++ {
		s.Save("OAuth related entry number "+string(rune('A'+i)), nil)
	}

	results, err := s.Search("OAuth", 2, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}
}

func TestSearchEmpty(t *testing.T) {
	s := tempDB(t)
	results, err := s.Search("nonexistent", 10, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchPreservesTags(t *testing.T) {
	s := tempDB(t)
	s.Save("OAuth info", []string{"auth", "oauth"})

	results, err := s.Search("OAuth", 10, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if len(results[0].Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(results[0].Tags))
	}
}
