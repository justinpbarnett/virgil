package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// --- Working State tests ---

func TestPutState_Insert(t *testing.T) {
	s := tempDB(t)

	if err := s.PutState("spec", "oauth-login", "# OAuth Login Spec"); err != nil {
		t.Fatalf("PutState: %v", err)
	}

	content, found, err := s.GetState("spec", "oauth-login")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !found {
		t.Fatal("expected entry to be found")
	}
	if content != "# OAuth Login Spec" {
		t.Fatalf("got content %q, want %q", content, "# OAuth Login Spec")
	}
}

func TestPutState_Update(t *testing.T) {
	s := tempDB(t)

	if err := s.PutState("spec", "oauth-login", "v1"); err != nil {
		t.Fatalf("PutState v1: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := s.PutState("spec", "oauth-login", "v2"); err != nil {
		t.Fatalf("PutState v2: %v", err)
	}

	content, found, err := s.GetState("spec", "oauth-login")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !found {
		t.Fatal("expected entry to be found")
	}
	if content != "v2" {
		t.Fatalf("got content %q, want %q", content, "v2")
	}

	// Verify only one row exists (upsert, not duplicate insert)
	entries, err := s.ListState("spec")
	if err != nil {
		t.Fatalf("ListState: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after upsert, got %d", len(entries))
	}
}

func TestGetState_NotFound(t *testing.T) {
	s := tempDB(t)

	content, found, err := s.GetState("spec", "nonexistent")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

func TestDeleteState(t *testing.T) {
	s := tempDB(t)

	if err := s.PutState("spec", "oauth-login", "content"); err != nil {
		t.Fatalf("PutState: %v", err)
	}

	if err := s.DeleteState("spec", "oauth-login"); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}

	_, found, err := s.GetState("spec", "oauth-login")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if found {
		t.Fatal("expected entry to be deleted")
	}
}

func TestDeleteState_NotFound(t *testing.T) {
	s := tempDB(t)

	if err := s.DeleteState("spec", "nonexistent"); err != nil {
		t.Fatalf("DeleteState on nonexistent key: %v", err)
	}
}

func TestListState(t *testing.T) {
	s := tempDB(t)

	if err := s.PutState("spec", "a", "content-a"); err != nil {
		t.Fatalf("PutState a: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.PutState("spec", "b", "content-b"); err != nil {
		t.Fatalf("PutState b: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.PutState("spec", "c", "content-c"); err != nil {
		t.Fatalf("PutState c: %v", err)
	}

	entries, err := s.ListState("spec")
	if err != nil {
		t.Fatalf("ListState: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Should be ordered by updated_at DESC (c, b, a)
	if entries[0].Key != "c" {
		t.Fatalf("expected first entry key 'c', got %q", entries[0].Key)
	}
	if entries[1].Key != "b" {
		t.Fatalf("expected second entry key 'b', got %q", entries[1].Key)
	}
	if entries[2].Key != "a" {
		t.Fatalf("expected third entry key 'a', got %q", entries[2].Key)
	}

	// Verify content and namespace
	if entries[0].Content != "content-c" {
		t.Fatalf("expected content 'content-c', got %q", entries[0].Content)
	}
	if entries[0].Namespace != "spec" {
		t.Fatalf("expected namespace 'spec', got %q", entries[0].Namespace)
	}
}

// --- Invocations tests ---

func TestSaveInvocation(t *testing.T) {
	s := tempDB(t)
	if err := s.SaveInvocation("educate", "teach me Go", "What do you already know?"); err != nil {
		t.Fatalf("SaveInvocation: %v", err)
	}
}

func TestSearchInvocations_Basic(t *testing.T) {
	s := tempDB(t)
	s.SaveInvocation("educate", "teach me Go concurrency", "What do you know about goroutines?")
	s.SaveInvocation("educate", "teach me Python", "Let's start with Python basics.")
	s.SaveInvocation("draft", "write a blog about Go", "Here is a draft blog post about Go.")

	results, err := s.SearchInvocations("Go", "", 10, time.Time{})
	if err != nil {
		t.Fatalf("SearchInvocations: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'Go'")
	}
}

func TestSearchInvocations_FilterByPipe(t *testing.T) {
	s := tempDB(t)
	s.SaveInvocation("educate", "teach me Go", "goroutines question")
	s.SaveInvocation("draft", "write about Go", "blog about Go")

	results, err := s.SearchInvocations("Go", "educate", 10, time.Time{})
	if err != nil {
		t.Fatalf("SearchInvocations: %v", err)
	}
	for _, r := range results {
		if r.Pipe != "educate" {
			t.Errorf("expected pipe=educate, got %s", r.Pipe)
		}
	}
}

func TestSearchInvocations_FilterBySince(t *testing.T) {
	s := tempDB(t)
	s.SaveInvocation("educate", "teach me Go", "goroutines")

	future := time.Now().Add(time.Hour)
	results, err := s.SearchInvocations("Go", "", 10, future)
	if err != nil {
		t.Fatalf("SearchInvocations: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for future since, got %d", len(results))
	}
}

func TestRetrieveContext_WorkingState(t *testing.T) {
	s := tempDB(t)
	s.PutState("project", "current", "working on virgil memory refactor")

	results, err := s.RetrieveContext("", []ContextRequest{{Type: "working_state"}}, 500)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected working_state results")
	}
	found := false
	for _, r := range results {
		if r.Type == "working_state" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected entry with type=working_state")
	}
}

func TestRetrieveContext_TopicHistory(t *testing.T) {
	s := tempDB(t)
	s.SaveInvocation("educate", "teach me Go channels", "What do you know about channels?")

	results, err := s.RetrieveContext("Go", []ContextRequest{{Type: "topic_history"}}, 500)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Type == "topic_history" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected entry with type=topic_history")
	}
}

func TestRetrieveContext_BudgetEnforced(t *testing.T) {
	s := tempDB(t)
	// Insert enough content to potentially exceed a small budget
	for i := 0; i < 10; i++ {
		s.SaveInvocation("educate", "teach me Go topic "+string(rune('A'+i)), "long answer about Go "+string(rune('A'+i)))
	}

	results, err := s.RetrieveContext("Go", []ContextRequest{{Type: "topic_history"}}, 50)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}
	totalChars := 0
	for _, r := range results {
		totalChars += len(r.Content)
	}
	// Budget is 50 tokens = 200 chars
	if totalChars > 200 {
		t.Errorf("expected total chars <= 200 (budget), got %d", totalChars)
	}
}

func TestRetrieveContext_DisabledReturnsEmpty(t *testing.T) {
	s := tempDB(t)
	s.PutState("project", "current", "some state")

	results, err := s.RetrieveContext("", []ContextRequest{}, 500)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty requests, got %d", len(results))
	}
}

func TestListState_Empty(t *testing.T) {
	s := tempDB(t)

	entries, err := s.ListState("empty-namespace")
	if err != nil {
		t.Fatalf("ListState: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestNamespaceIsolation(t *testing.T) {
	s := tempDB(t)

	if err := s.PutState("ns1", "key", "content-ns1"); err != nil {
		t.Fatalf("PutState ns1: %v", err)
	}
	if err := s.PutState("ns2", "key", "content-ns2"); err != nil {
		t.Fatalf("PutState ns2: %v", err)
	}

	// Get from ns1
	content, found, err := s.GetState("ns1", "key")
	if err != nil {
		t.Fatalf("GetState ns1: %v", err)
	}
	if !found {
		t.Fatal("expected ns1 entry to be found")
	}
	if content != "content-ns1" {
		t.Fatalf("ns1: got %q, want %q", content, "content-ns1")
	}

	// Get from ns2
	content, found, err = s.GetState("ns2", "key")
	if err != nil {
		t.Fatalf("GetState ns2: %v", err)
	}
	if !found {
		t.Fatal("expected ns2 entry to be found")
	}
	if content != "content-ns2" {
		t.Fatalf("ns2: got %q, want %q", content, "content-ns2")
	}

	// Delete from ns1 shouldn't affect ns2
	if err := s.DeleteState("ns1", "key"); err != nil {
		t.Fatalf("DeleteState ns1: %v", err)
	}

	_, found, err = s.GetState("ns2", "key")
	if err != nil {
		t.Fatalf("GetState ns2 after ns1 delete: %v", err)
	}
	if !found {
		t.Fatal("ns2 entry should still exist after ns1 delete")
	}

	// List should only show entries in the given namespace
	entries, err := s.ListState("ns1")
	if err != nil {
		t.Fatalf("ListState ns1: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries in ns1 after delete, got %d", len(entries))
	}

	entries, err = s.ListState("ns2")
	if err != nil {
		t.Fatalf("ListState ns2: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in ns2, got %d", len(entries))
	}
}
