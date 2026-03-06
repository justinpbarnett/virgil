package store

import (
	"database/sql"
	"testing"
)

func TestAddTodo(t *testing.T) {
	s := tempDB(t)

	todo, err := s.AddTodo("buy groceries", 2, "2026-03-10", []string{"shopping", "errands"})
	if err != nil {
		t.Fatalf("AddTodo failed: %v", err)
	}

	if todo.ID == "" {
		t.Error("expected non-empty ID")
	}
	if todo.Title != "buy groceries" {
		t.Errorf("title = %q, want %q", todo.Title, "buy groceries")
	}
	if todo.Status != "pending" {
		t.Errorf("status = %q, want pending", todo.Status)
	}
	if todo.Priority != 2 {
		t.Errorf("priority = %d, want 2", todo.Priority)
	}
	if todo.DueDate != "2026-03-10" {
		t.Errorf("due_date = %q, want %q", todo.DueDate, "2026-03-10")
	}
	if len(todo.Tags) != 2 || todo.Tags[0] != "shopping" {
		t.Errorf("tags = %v, want [shopping errands]", todo.Tags)
	}
	if todo.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestAddTodoDefaultPriority(t *testing.T) {
	s := tempDB(t)

	todo, err := s.AddTodo("write tests", 3, "", nil)
	if err != nil {
		t.Fatalf("AddTodo failed: %v", err)
	}
	if todo.Priority != 3 {
		t.Errorf("priority = %d, want 3", todo.Priority)
	}
}

func TestAddTodoPriorityClamp(t *testing.T) {
	s := tempDB(t)

	lo, err := s.AddTodo("low priority", 0, "", nil)
	if err != nil {
		t.Fatalf("AddTodo failed: %v", err)
	}
	if lo.Priority != 1 {
		t.Errorf("priority clamped to %d, want 1", lo.Priority)
	}

	hi, err := s.AddTodo("high priority", 10, "", nil)
	if err != nil {
		t.Fatalf("AddTodo failed: %v", err)
	}
	if hi.Priority != 5 {
		t.Errorf("priority clamped to %d, want 5", hi.Priority)
	}
}

func TestListTodosOrder(t *testing.T) {
	s := tempDB(t)

	s.AddTodo("low", 5, "", nil)   //nolint:errcheck
	s.AddTodo("high", 1, "", nil)  //nolint:errcheck
	s.AddTodo("medium", 3, "", nil) //nolint:errcheck

	todos, err := s.ListTodos("pending", 25)
	if err != nil {
		t.Fatalf("ListTodos failed: %v", err)
	}
	if len(todos) != 3 {
		t.Fatalf("got %d todos, want 3", len(todos))
	}
	if todos[0].Title != "high" || todos[1].Title != "medium" || todos[2].Title != "low" {
		t.Errorf("wrong order: %v %v %v", todos[0].Title, todos[1].Title, todos[2].Title)
	}
}

func TestListTodosStatusFilter(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("task a", 3, "", nil)
	s.AddTodo("task b", 3, "", nil) //nolint:errcheck
	s.CompleteTodo(todo.ID)         //nolint:errcheck

	pending, err := s.ListTodos("pending", 25)
	if err != nil {
		t.Fatalf("ListTodos pending failed: %v", err)
	}
	if len(pending) != 1 || pending[0].Title != "task b" {
		t.Errorf("expected 1 pending todo, got %d", len(pending))
	}

	done, err := s.ListTodos("done", 25)
	if err != nil {
		t.Fatalf("ListTodos done failed: %v", err)
	}
	if len(done) != 1 || done[0].Title != "task a" {
		t.Errorf("expected 1 done todo, got %d", len(done))
	}

	all, err := s.ListTodos("all", 25)
	if err != nil {
		t.Fatalf("ListTodos all failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 todos, got %d", len(all))
	}
}

func TestGetTodo(t *testing.T) {
	s := tempDB(t)

	created, _ := s.AddTodo("find me", 2, "", nil)
	found, err := s.GetTodo(created.ID)
	if err != nil {
		t.Fatalf("GetTodo failed: %v", err)
	}
	if found.ID != created.ID || found.Title != "find me" {
		t.Errorf("got %+v, want %+v", found, created)
	}
}

func TestGetTodoMissing(t *testing.T) {
	s := tempDB(t)

	_, err := s.GetTodo("nonexistent-id")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestCompleteTodo(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("finish it", 3, "", nil)
	if err := s.CompleteTodo(todo.ID); err != nil {
		t.Fatalf("CompleteTodo failed: %v", err)
	}

	updated, err := s.GetTodo(todo.ID)
	if err != nil {
		t.Fatalf("GetTodo failed: %v", err)
	}
	if updated.Status != "done" {
		t.Errorf("status = %q, want done", updated.Status)
	}
	if updated.CompletedAt.IsZero() {
		t.Error("expected non-zero CompletedAt")
	}
}

func TestUpdateTodo(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("original title", 3, "", nil)
	err := s.UpdateTodo(todo.ID, map[string]string{
		"title":    "updated title",
		"priority": "1",
		"due_date": "2026-04-01",
		"tags":     "work,urgent",
	})
	if err != nil {
		t.Fatalf("UpdateTodo failed: %v", err)
	}

	updated, _ := s.GetTodo(todo.ID)
	if updated.Title != "updated title" {
		t.Errorf("title = %q, want updated title", updated.Title)
	}
	if updated.Priority != 1 {
		t.Errorf("priority = %d, want 1", updated.Priority)
	}
	if updated.DueDate != "2026-04-01" {
		t.Errorf("due_date = %q, want 2026-04-01", updated.DueDate)
	}
}

func TestDeleteTodo(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("delete me", 3, "", nil)
	if err := s.DeleteTodo(todo.ID); err != nil {
		t.Fatalf("DeleteTodo failed: %v", err)
	}

	_, err := s.GetTodo(todo.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestReorderTodo(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("move me", 3, "", nil)
	if err := s.ReorderTodo(todo.ID, 1); err != nil {
		t.Fatalf("ReorderTodo failed: %v", err)
	}

	updated, _ := s.GetTodo(todo.ID)
	if updated.Priority != 1 {
		t.Errorf("priority = %d, want 1", updated.Priority)
	}
}

func TestSetTodoMemoryID(t *testing.T) {
	s := tempDB(t)

	todo, _ := s.AddTodo("linked todo", 3, "", nil)
	memID, _ := s.SaveInvocation("todo", "test signal", "Added todo: linked todo")

	if err := s.SetTodoMemoryID(todo.ID, memID); err != nil {
		t.Fatalf("SetTodoMemoryID failed: %v", err)
	}

	updated, _ := s.GetTodo(todo.ID)
	if updated.MemoryID != memID {
		t.Errorf("memory_id = %q, want %q", updated.MemoryID, memID)
	}
}
