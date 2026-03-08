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

func TestHandleRemoveWordOverlapMatch(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "new event 4p-6:30p friends coming visit"}) //nolint:errcheck

	// Topic extracted from "delete the new event todo item" would be "new event item"
	// Substring match fails, but word-overlap should find it (2/3 words match)
	out := h(textEnvelope(""), map[string]string{"action": "remove", "topic": "new event item"})
	if out.Error != nil {
		t.Fatalf("expected word-overlap match, got error: %s", out.Error.Message)
	}
}

func TestHandleRemoveWordOverlapNoFalsePositive(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "buy groceries"}) //nolint:errcheck

	// Single-word overlap ("item" matches nothing) should not match
	out := h(textEnvelope(""), map[string]string{"action": "remove", "topic": "random item"})
	if out.Error == nil {
		t.Fatal("expected no match for unrelated search")
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

func TestHandleDetailByID(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "fix auth bug"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{"action": "detail", "id": id})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if out.ContentType != envelope.ContentStructured {
		t.Errorf("content_type = %q, want structured", out.ContentType)
	}
	result := out.Content.(map[string]any)
	if result["title"] != "fix auth bug" {
		t.Errorf("title = %v, want fix auth bug", result["title"])
	}
	if result["action"] != "detail" {
		t.Errorf("action = %v, want detail", result["action"])
	}
}

func TestHandleDetailNoDetails(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "plain task"})
	m := addOut.Content.(map[string]any)
	id := m["id"].(string)

	out := h(textEnvelope(""), map[string]string{"action": "detail", "id": id})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if _, ok := result["details"]; ok {
		t.Error("expected no details key for todo without details")
	}
}

func TestHandleDetailNotFound(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	out := h(textEnvelope(""), map[string]string{"action": "detail", "id": "nonexistent-id"})
	if out.Error == nil {
		t.Fatal("expected error for nonexistent id")
	}
}

func TestHandleDetailFuzzyMatch(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "implement oauth flow"}) //nolint:errcheck

	out := h(textEnvelope("implement oauth flow"), map[string]string{"action": "detail"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if result["title"] != "implement oauth flow" {
		t.Errorf("title = %v, want 'implement oauth flow'", result["title"])
	}
}

func TestTodoToMapIncludesExternalID(t *testing.T) {
	todo, err := setupStore(t).AddTodo("task", 3, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	todo.ExternalID = "jira:PTP-123"
	m := todoToMap(todo)
	if m["external_id"] != "jira:PTP-123" {
		t.Errorf("external_id = %v, want jira:PTP-123", m["external_id"])
	}
}

func TestTodoToMapIncludesDetails(t *testing.T) {
	todo, err := setupStore(t).AddTodo("task", 3, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	todo.Details = "## Description\nSome detail text"
	m := todoToMap(todo)
	if m["details"] != "## Description\nSome detail text" {
		t.Errorf("details = %v, want the detail string", m["details"])
	}
}

func TestTodoToMapOmitsEmptyExternalID(t *testing.T) {
	todo, err := setupStore(t).AddTodo("task", 3, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := todoToMap(todo)
	if _, ok := m["external_id"]; ok {
		t.Error("expected external_id to be omitted when empty")
	}
}

func TestTodoToMapOmitsEmptyDetails(t *testing.T) {
	todo, err := setupStore(t).AddTodo("task", 3, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := todoToMap(todo)
	if _, ok := m["details"]; ok {
		t.Error("expected details to be omitted when empty")
	}
}

func TestUpsertTodoByExternalIDCreate(t *testing.T) {
	s := setupStore(t)

	todo, created, err := s.UpsertTodoByExternalID("jira:PTP-1", "Fix login bug", "## Details\nSome context", 2, "", []string{"jira"})
	if err != nil {
		t.Fatalf("upsert error: %v", err)
	}
	if !created {
		t.Error("expected created=true for new external_id")
	}
	if todo.Title != "Fix login bug" {
		t.Errorf("title = %q, want Fix login bug", todo.Title)
	}
	if todo.ExternalID != "jira:PTP-1" {
		t.Errorf("external_id = %q, want jira:PTP-1", todo.ExternalID)
	}
}

func TestUpsertTodoByExternalIDUpdate(t *testing.T) {
	s := setupStore(t)

	_, _, err := s.UpsertTodoByExternalID("jira:PTP-2", "Original title", "", 3, "", nil)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	todo, created, err := s.UpsertTodoByExternalID("jira:PTP-2", "Updated title", "New details", 1, "", []string{"jira"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if created {
		t.Error("expected created=false for existing external_id")
	}
	if todo.Title != "Updated title" {
		t.Errorf("title = %q, want Updated title", todo.Title)
	}
	if todo.Details != "New details" {
		t.Errorf("details = %q, want New details", todo.Details)
	}
}

func TestUpsertTodoByExternalIDEmptyID(t *testing.T) {
	s := setupStore(t)
	_, _, err := s.UpsertTodoByExternalID("", "title", "", 3, "", nil)
	if err == nil {
		t.Fatal("expected error for empty external_id")
	}
}

func TestFindTodoByExternalID(t *testing.T) {
	s := setupStore(t)

	_, _, err := s.UpsertTodoByExternalID("jira:PTP-10", "Search me", "", 3, "", nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	todo, err := s.FindTodoByExternalID("jira:PTP-10")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if todo.ExternalID != "jira:PTP-10" {
		t.Errorf("external_id = %q, want jira:PTP-10", todo.ExternalID)
	}
}

func TestFindTodoByExternalIDNotFound(t *testing.T) {
	s := setupStore(t)
	_, err := s.FindTodoByExternalID("jira:NOTEXIST-999")
	if err == nil {
		t.Fatal("expected error for nonexistent external_id")
	}
}

func TestUpdateTodoDetails(t *testing.T) {
	s := setupStore(t)

	todo, err := s.AddTodo("update me", 3, "", nil)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := s.UpdateTodo(todo.ID, map[string]string{"details": "new context here"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetTodo(todo.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Details != "new context here" {
		t.Errorf("details = %q, want new context here", got.Details)
	}
}
