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
	s.Save("OAuth uses short-lived tokens", nil, nil)

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

// --- Working State tests ---

func TestHandleWorkingState_Put(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	input.Content = "# OAuth Login Spec\n\nThis is the spec content."
	input.ContentType = envelope.ContentText

	flags := map[string]string{
		"action":    "working-state",
		"op":        "put",
		"namespace": "spec",
		"key":       "oauth-login",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Action != "working-state" {
		t.Errorf("expected action=working-state, got %s", result.Action)
	}
	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}

	// Verify it was actually stored
	content, found, err := s.GetState("spec", "oauth-login")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !found {
		t.Fatal("expected entry to be found after put")
	}
	if content != "# OAuth Login Spec\n\nThis is the spec content." {
		t.Fatalf("stored content mismatch: %q", content)
	}
}

func TestHandleWorkingState_Get(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	// Pre-populate state
	if err := s.PutState("spec", "oauth-login", "spec content here"); err != nil {
		t.Fatalf("PutState: %v", err)
	}

	input := envelope.New("input", "test")
	flags := map[string]string{
		"action":    "working-state",
		"op":        "get",
		"namespace": "spec",
		"key":       "oauth-login",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentText {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	content, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if content != "spec content here" {
		t.Fatalf("got %q, want %q", content, "spec content here")
	}
}

func TestHandleWorkingState_GetNotFound(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	flags := map[string]string{
		"action":    "working-state",
		"op":        "get",
		"namespace": "spec",
		"key":       "nonexistent",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	content, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if content != "" {
		t.Fatalf("expected empty content for not-found, got %q", content)
	}
}

func TestHandleWorkingState_Delete(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	// Pre-populate
	if err := s.PutState("spec", "oauth-login", "content"); err != nil {
		t.Fatalf("PutState: %v", err)
	}

	input := envelope.New("input", "test")
	flags := map[string]string{
		"action":    "working-state",
		"op":        "delete",
		"namespace": "spec",
		"key":       "oauth-login",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify it was deleted
	_, found, err := s.GetState("spec", "oauth-login")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if found {
		t.Fatal("expected entry to be deleted")
	}
}

func TestHandleWorkingState_PutNoKey(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	input.Content = "some content"
	input.ContentType = envelope.ContentText

	flags := map[string]string{
		"action":    "working-state",
		"op":        "put",
		"namespace": "spec",
		"key":       "",
	}

	result := handler(input, flags)
	testutil.AssertFatalError(t, result)
}

func TestHandleWorkingState_PutNoNamespace(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	input.Content = "some content"
	input.ContentType = envelope.ContentText

	flags := map[string]string{
		"action":    "working-state",
		"op":        "put",
		"namespace": "",
		"key":       "oauth-login",
	}

	result := handler(input, flags)
	testutil.AssertFatalError(t, result)
}

func TestHandleWorkingState_PutNoContent(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	input := envelope.New("input", "test")
	input.ContentType = envelope.ContentText
	// Content is nil / empty

	flags := map[string]string{
		"action":    "working-state",
		"op":        "put",
		"namespace": "spec",
		"key":       "oauth-login",
	}

	result := handler(input, flags)
	testutil.AssertFatalError(t, result)
}

func TestHandleWorkingState_List(t *testing.T) {
	s := testStore(t)
	handler := NewHandler(s, nil)

	// Pre-populate
	if err := s.PutState("spec", "a", "content-a"); err != nil {
		t.Fatalf("PutState a: %v", err)
	}
	if err := s.PutState("spec", "b", "content-b"); err != nil {
		t.Fatalf("PutState b: %v", err)
	}

	input := envelope.New("input", "test")
	flags := map[string]string{
		"action":    "working-state",
		"op":        "list",
		"namespace": "spec",
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
	entries, ok := result.Content.([]store.StateEntry)
	if !ok {
		t.Fatalf("expected []store.StateEntry, got %T", result.Content)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}
