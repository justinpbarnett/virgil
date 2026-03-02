package memory

import (
	"path/filepath"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreAction(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	input.Content = "OAuth uses short-lived tokens"
	input.ContentType = "text"

	result := handler(input, map[string]string{"action": "store"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", result.Pipe)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
}

func TestRetrieveAction(t *testing.T) {
	s := testStore(t)
	s.Save("OAuth uses short-lived tokens", nil)

	handler := NewHandler(s, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "retrieve", "query": "OAuth"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "list" {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
	entries, ok := result.Content.([]store.Entry)
	if !ok {
		t.Fatalf("expected []store.Entry, got %T", result.Content)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one result")
	}
}

func TestRetrieveNoResults(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"action": "retrieve", "query": "nonexistent"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "list" {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
}

func TestStoreEmptyContent(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)
	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(input, map[string]string{"action": "store"})

	testutil.AssertFatalError(t, result)
}

func TestDefaultActionIsRetrieve(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	if result.Action != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", result.Action)
	}
}
