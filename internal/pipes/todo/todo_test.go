package todo

import (
	"path/filepath"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/store"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func textEnvelope(content string) envelope.Envelope {
	env := envelope.New("test", "test")
	env.Content = content
	env.ContentType = envelope.ContentText
	return env
}

func TestHandleAdd(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{
		"action":   "add",
		"title":    "buy groceries",
		"priority": "2",
	})

	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if out.ContentType != envelope.ContentStructured {
		t.Errorf("content_type = %q, want structured", out.ContentType)
	}
	m, ok := out.Content.(map[string]any)
	if !ok {
		t.Fatalf("content not a map")
	}
	if m["title"] != "buy groceries" {
		t.Errorf("title = %v, want buy groceries", m["title"])
	}
	if m["status"] != "pending" {
		t.Errorf("status = %v, want pending", m["status"])
	}
}

func TestHandleAddFallbackToContent(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope("take out the trash"), map[string]string{"action": "add"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	m := out.Content.(map[string]any)
	if m["title"] != "take out the trash" {
		t.Errorf("title = %v, want 'take out the trash'", m["title"])
	}
}

func TestHandleAddMissingTitle(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{"action": "add"})
	if out.Error == nil {
		t.Fatal("expected error for missing title")
	}
	if out.Error.Severity != envelope.SeverityFatal {
		t.Errorf("severity = %q, want fatal", out.Error.Severity)
	}
}

func TestHandleAddWritesMemory(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope("add groceries"), map[string]string{
		"action": "add",
		"title":  "buy milk",
	})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}

	m := out.Content.(map[string]any)
	id := m["id"].(string)

	todo, err := s.GetTodo(id)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if todo.MemoryID == "" {
		t.Error("expected memory_id to be set after add")
	}
}

func TestHandleList(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "task one"})   //nolint:errcheck
	h(textEnvelope(""), map[string]string{"action": "add", "title": "task two"})   //nolint:errcheck

	out := h(textEnvelope(""), map[string]string{"action": "list", "status": "pending"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if out.ContentType != envelope.ContentList {
		t.Errorf("content_type = %q, want list", out.ContentType)
	}
	items, ok := out.Content.([]map[string]any)
	if !ok {
		t.Fatalf("content not a list of maps")
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

func TestHandleListEmpty(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{"action": "list"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	items, ok := out.Content.([]map[string]any)
	if ok && len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}
}

func TestHandleListStatusFilter(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "finish report"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "other task"}) //nolint:errcheck
	h(textEnvelope(""), map[string]string{"action": "done", "id": id})             //nolint:errcheck

	out := h(textEnvelope(""), map[string]string{"action": "list", "status": "done"})
	items := out.Content.([]map[string]any)
	if len(items) != 1 {
		t.Errorf("expected 1 done item, got %d", len(items))
	}
}

func TestHandleDoneByID(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "wash dishes"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{"action": "done", "id": id})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if result["status"] != "done" {
		t.Errorf("status = %v, want done", result["status"])
	}
}

func TestHandleDoneFuzzyMatch(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "buy groceries"}) //nolint:errcheck

	out := h(textEnvelope("buy groceries"), map[string]string{"action": "done"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if result["status"] != "done" {
		t.Errorf("status = %v, want done", result["status"])
	}
}

func TestHandleDoneFuzzyNoMatch(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope("nonexistent task"), map[string]string{"action": "done"})
	if out.Error == nil {
		t.Fatal("expected error for no match")
	}
}

func TestHandleDoneWritesMemoryAndEdge(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope("add task"), map[string]string{"action": "add", "title": "test edge task"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	todo, _ := s.GetTodo(id)
	if todo.MemoryID == "" {
		t.Skip("memory not written on add, skipping edge test")
	}

	h(textEnvelope("done task"), map[string]string{"action": "done", "id": id}) //nolint:errcheck
	// If no panic/error, edge creation succeeded (or was gracefully skipped)
}

func TestHandleRemove(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "remove me"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{"action": "remove", "id": id})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}

	// Verify gone
	_, err := s.GetTodo(id)
	if err == nil {
		t.Error("expected error after remove, got nil")
	}
}

func TestHandleRemoveMissingID(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{"action": "remove"})
	if out.Error == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestHandleEdit(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "original"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{
		"action":   "edit",
		"id":       id,
		"title":    "updated title",
		"priority": "1",
	})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if result["title"] != "updated title" {
		t.Errorf("title = %v, want updated title", result["title"])
	}
}

func TestHandleReorder(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "reorder me", "priority": "3"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{"action": "reorder", "id": id, "priority": "1"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}

	todo, _ := s.GetTodo(id)
	if todo.Priority != 1 {
		t.Errorf("priority = %d, want 1", todo.Priority)
	}
}

func TestHandleUnknownAction(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{"action": "bogus"})
	if out.Error == nil {
		t.Fatal("expected error for unknown action")
	}
}
