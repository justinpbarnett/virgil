package store

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestSaveKind(t *testing.T) {
	s := tempDB(t)

	type data struct {
		Status   string `json:"status"`
		Priority int    `json:"priority"`
	}

	id, err := s.SaveKind(KindTodo, "buy groceries", data{Status: "pending", Priority: 2}, []string{"shopping"}, nil)
	if err != nil {
		t.Fatalf("SaveKind: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Kind != KindTodo {
		t.Errorf("kind = %q, want todo", mem.Kind)
	}
	if mem.Content != "buy groceries" {
		t.Errorf("content = %q, want buy groceries", mem.Content)
	}
	if mem.Confidence != ConfidenceTodo {
		t.Errorf("confidence = %f, want %f", mem.Confidence, ConfidenceTodo)
	}
	if len(mem.Tags) != 1 || mem.Tags[0] != "shopping" {
		t.Errorf("tags = %v, want [shopping]", mem.Tags)
	}

	var d data
	json.Unmarshal([]byte(mem.Data), &d) //nolint:errcheck
	if d.Status != "pending" || d.Priority != 2 {
		t.Errorf("data = %+v, want pending/2", d)
	}
}

func TestQueryByKind(t *testing.T) {
	s := tempDB(t)

	s.SaveKind(KindTodo, "todo 1", nil, nil, nil)     //nolint:errcheck
	s.SaveKind(KindTodo, "todo 2", nil, nil, nil)     //nolint:errcheck
	s.SaveKind(KindExplicit, "fact 1", nil, nil, nil) //nolint:errcheck

	entries, err := s.QueryByKind(KindTodo, 10)
	if err != nil {
		t.Fatalf("QueryByKind: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Kind != KindTodo {
			t.Errorf("kind = %q, want todo", e.Kind)
		}
	}
}

func TestQueryByKindExcludesExpired(t *testing.T) {
	s := tempDB(t)

	past := time.Now().Add(-time.Hour)
	s.SaveKind(KindTodo, "expired", nil, nil, &past) //nolint:errcheck
	s.SaveKind(KindTodo, "active", nil, nil, nil)    //nolint:errcheck

	entries, err := s.QueryByKind(KindTodo, 10)
	if err != nil {
		t.Fatalf("QueryByKind: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if entries[0].Content != "active" {
		t.Errorf("content = %q, want active", entries[0].Content)
	}
}

func TestQueryByKindExcludesSuperseded(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveKind(KindTodo, "original", map[string]string{"status": "pending"}, nil, nil)
	s.SupersedeMemory(id1, "original", map[string]string{"status": "done"}, nil) //nolint:errcheck

	entries, err := s.QueryByKind(KindTodo, 10)
	if err != nil {
		t.Fatalf("QueryByKind: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1 (the superseding entry)", len(entries))
	}
	for _, e := range entries {
		if e.ID == id1 {
			t.Error("superseded entry should not appear")
		}
	}
}

func TestQueryByKindFiltered(t *testing.T) {
	s := tempDB(t)

	s.SaveKind(KindTodo, "pending task", map[string]string{"status": "pending"}, nil, nil) //nolint:errcheck
	s.SaveKind(KindTodo, "done task", map[string]string{"status": "done"}, nil, nil)       //nolint:errcheck

	entries, err := s.QueryByKindFiltered(KindTodo, map[string]any{"status": "pending"}, 10)
	if err != nil {
		t.Fatalf("QueryByKindFiltered: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if entries[0].Content != "pending task" {
		t.Errorf("content = %q, want pending task", entries[0].Content)
	}
}

func TestSearchByKind(t *testing.T) {
	s := tempDB(t)

	s.SaveKind(KindTodo, "buy groceries", nil, nil, nil)    //nolint:errcheck
	s.SaveKind(KindExplicit, "buy presents", nil, nil, nil) //nolint:errcheck

	entries, err := s.SearchByKind("buy", KindTodo, 10)
	if err != nil {
		t.Fatalf("SearchByKind: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if len(entries) > 0 && entries[0].Kind != KindTodo {
		t.Errorf("kind = %q, want todo", entries[0].Kind)
	}
}

func TestUpdateData(t *testing.T) {
	s := tempDB(t)

	type data struct {
		Status   string `json:"status"`
		Priority int    `json:"priority"`
	}

	id, _ := s.SaveKind(KindTodo, "task", data{Status: "pending", Priority: 3}, nil, nil)

	if err := s.UpdateData(id, data{Status: "pending", Priority: 1}); err != nil {
		t.Fatalf("UpdateData: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	var d data
	json.Unmarshal([]byte(mem.Data), &d) //nolint:errcheck
	if d.Priority != 1 {
		t.Errorf("priority = %d, want 1", d.Priority)
	}
}

func TestUpdateMemoryWithTags(t *testing.T) {
	s := tempDB(t)

	id, _ := s.SaveKind(KindTodo, "original", map[string]string{"status": "pending"}, []string{"work"}, nil)

	if err := s.UpdateMemory(id, "updated", map[string]string{"status": "pending"}, []string{"work", "urgent"}); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Content != "updated" {
		t.Errorf("content = %q, want updated", mem.Content)
	}
	if len(mem.Tags) != 2 || mem.Tags[1] != "urgent" {
		t.Errorf("tags = %v, want [work urgent]", mem.Tags)
	}
}

func TestSupersedeMemory(t *testing.T) {
	s := tempDB(t)

	oldID, _ := s.SaveKind(KindTodo, "task", map[string]string{"status": "pending"}, nil, nil)

	newID, err := s.SupersedeMemory(oldID, "task", map[string]string{"status": "done"}, nil)
	if err != nil {
		t.Fatalf("SupersedeMemory: %v", err)
	}
	if newID == "" || newID == oldID {
		t.Error("expected new distinct ID")
	}

	// Old entry should have halved confidence
	old, _ := s.GetMemory(oldID)
	if old.Confidence >= ConfidenceTodo {
		t.Errorf("old confidence = %f, should be halved", old.Confidence)
	}

	// New entry should exist with correct data
	newMem, _ := s.GetMemory(newID)
	var d map[string]string
	json.Unmarshal([]byte(newMem.Data), &d) //nolint:errcheck
	if d["status"] != "done" {
		t.Errorf("new status = %q, want done", d["status"])
	}
}

func TestSupersessionChain(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveKind(KindTodo, "task", map[string]string{"status": "pending"}, nil, nil)
	id2, _ := s.SupersedeMemory(id1, "task", map[string]string{"status": "done"}, nil)
	id3, _ := s.SupersedeMemory(id2, "task", map[string]string{"status": "pending"}, nil)

	entries, err := s.QueryByKind(KindTodo, 10)
	if err != nil {
		t.Fatalf("QueryByKind: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (only latest)", len(entries))
	}
	if entries[0].ID != id3 {
		t.Errorf("got id %q, want %q (latest in chain)", entries[0].ID, id3)
	}
}

func TestFindByKindAndDataField(t *testing.T) {
	s := tempDB(t)

	s.SaveKind(KindTodo, "task 1", map[string]string{"external_id": "jira:PTP-1"}, nil, nil) //nolint:errcheck
	s.SaveKind(KindTodo, "task 2", map[string]string{"external_id": "jira:PTP-2"}, nil, nil) //nolint:errcheck

	mem, err := s.FindByKindAndDataField(KindTodo, "$.external_id", "jira:PTP-1")
	if err != nil {
		t.Fatalf("FindByKindAndDataField: %v", err)
	}
	if mem.Content != "task 1" {
		t.Errorf("content = %q, want task 1", mem.Content)
	}
}

func TestFindByKindAndDataFieldNotFound(t *testing.T) {
	s := tempDB(t)

	_, err := s.FindByKindAndDataField(KindTodo, "$.external_id", "nonexistent")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestFindByKindAndDataPrefix(t *testing.T) {
	s := tempDB(t)

	s.SaveKind(KindTodo, "jira task", map[string]string{"external_id": "jira:PTP-1"}, nil, nil)   //nolint:errcheck
	s.SaveKind(KindTodo, "slack task", map[string]string{"external_id": "slack:C_1"}, nil, nil)   //nolint:errcheck
	s.SaveKind(KindTodo, "jira task 2", map[string]string{"external_id": "jira:PTP-2"}, nil, nil) //nolint:errcheck

	entries, err := s.FindByKindAndDataPrefix(KindTodo, "$.external_id", "jira:", 10)
	if err != nil {
		t.Fatalf("FindByKindAndDataPrefix: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestGetMemoryNotFound(t *testing.T) {
	s := tempDB(t)

	_, err := s.GetMemory("nonexistent-id")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSaveKindWithValidUntil(t *testing.T) {
	s := tempDB(t)

	future := time.Now().Add(24 * time.Hour)
	id, err := s.SaveKind(KindReminder, "call dentist", nil, nil, &future)
	if err != nil {
		t.Fatalf("SaveKind: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Kind != KindReminder {
		t.Errorf("kind = %q, want reminder", mem.Kind)
	}
}
