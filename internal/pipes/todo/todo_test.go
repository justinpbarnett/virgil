package todo

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Kind != store.KindTodo {
		t.Errorf("kind = %q, want todo", mem.Kind)
	}
	if mem.Content != "buy milk" {
		t.Errorf("content = %q, want buy milk", mem.Content)
	}
}

func TestHandleList(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "task one"})
	h(textEnvelope(""), map[string]string{"action": "add", "title": "task two"})

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

	h(textEnvelope(""), map[string]string{"action": "add", "title": "other task"})
	h(textEnvelope(""), map[string]string{"action": "done", "id": id})

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

	h(textEnvelope(""), map[string]string{"action": "add", "title": "buy groceries"})

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

func TestHandleDoneCreatesSupersedeEdge(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	addOut := h(textEnvelope(""), map[string]string{"action": "add", "title": "test edge task"})
	m := addOut.Content.(map[string]any)
	origID := m["id"].(string)

	doneOut := h(textEnvelope(""), map[string]string{"action": "done", "id": origID})
	if doneOut.Error != nil {
		t.Fatalf("unexpected error: %s", doneOut.Error.Message)
	}
	newID := doneOut.Content.(map[string]any)["id"].(string)

	newMem, err := s.GetMemory(newID)
	if err != nil {
		t.Fatalf("GetMemory new: %v", err)
	}
	var d todoData
	json.Unmarshal([]byte(newMem.Data), &d) //nolint:errcheck
	if d.Status != "done" {
		t.Errorf("new entry status = %q, want done", d.Status)
	}

	// Original should be superseded (not in pending list)
	entries, _ := s.QueryByKindFiltered(store.KindTodo, map[string]any{"status": "pending"}, 25)
	for _, e := range entries {
		if e.ID == origID {
			t.Error("original entry should be superseded")
		}
	}
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

	// Verify not in pending list (superseded)
	entries, err := s.QueryByKindFiltered(store.KindTodo, map[string]any{"status": "pending"}, 25)
	if err != nil {
		t.Fatalf("QueryByKindFiltered: %v", err)
	}
	for _, e := range entries {
		if e.ID == id {
			t.Error("removed todo should not appear in pending list")
		}
	}
}

func TestHandleRemoveWordOverlapMatch(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "new event 4p-6:30p friends coming visit"})

	out := h(textEnvelope(""), map[string]string{"action": "remove", "topic": "new event item"})
	if out.Error != nil {
		t.Fatalf("expected word-overlap match, got error: %s", out.Error.Message)
	}
}

func TestHandleRemoveWordOverlapNoFalsePositive(t *testing.T) {
	s := setupStore(t)
	h := NewHandler(s, nil)

	h(textEnvelope(""), map[string]string{"action": "add", "title": "buy groceries"})

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

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	var d todoData
	json.Unmarshal([]byte(mem.Data), &d) //nolint:errcheck
	if d.Priority != 1 {
		t.Errorf("priority = %d, want 1", d.Priority)
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

	h(textEnvelope(""), map[string]string{"action": "add", "title": "implement oauth flow"})

	out := h(textEnvelope("implement oauth flow"), map[string]string{"action": "detail"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	result := out.Content.(map[string]any)
	if result["title"] != "implement oauth flow" {
		t.Errorf("title = %v, want 'implement oauth flow'", result["title"])
	}
}

func TestMemToMapIncludesExternalID(t *testing.T) {
	d := todoData{Status: "pending", Priority: 3, ExternalID: "jira:PTP-123"}
	m := memToMap("test-id", "task", d, nil, time.Now())
	if m["external_id"] != "jira:PTP-123" {
		t.Errorf("external_id = %v, want jira:PTP-123", m["external_id"])
	}
}

func TestMemToMapIncludesDetails(t *testing.T) {
	d := todoData{Status: "pending", Priority: 3, Details: "## Description\nSome detail text"}
	m := memToMap("test-id", "task", d, nil, time.Now())
	if m["details"] != "## Description\nSome detail text" {
		t.Errorf("details = %v, want the detail string", m["details"])
	}
}

func TestMemToMapOmitsEmptyExternalID(t *testing.T) {
	d := todoData{Status: "pending", Priority: 3}
	m := memToMap("test-id", "task", d, nil, time.Now())
	if _, ok := m["external_id"]; ok {
		t.Error("expected external_id to be omitted when empty")
	}
}

func TestMemToMapOmitsEmptyDetails(t *testing.T) {
	d := todoData{Status: "pending", Priority: 3}
	m := memToMap("test-id", "task", d, nil, time.Now())
	if _, ok := m["details"]; ok {
		t.Error("expected details to be omitted when empty")
	}
}
