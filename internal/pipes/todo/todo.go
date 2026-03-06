package todo

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
)

var whenParser = func() *when.Parser {
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)
	return w
}()

func NewHandler(s *store.Store, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		if action == "" {
			action = "list"
		}

		switch action {
		case "add":
			return handleAdd(s, input, flags, logger)
		case "list":
			return handleList(s, input, flags, logger)
		case "done":
			return handleDone(s, input, flags, logger)
		case "undone":
			return handleUndone(s, input, flags, logger)
		case "remove":
			return handleRemove(s, input, flags, logger)
		case "edit":
			return handleEdit(s, input, flags, logger)
		case "reorder":
			return handleReorder(s, input, flags, logger)
		default:
			out := envelope.New("todo", action)
			out.Args = flags
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s", action))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	}
}

func handleAdd(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "add")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	title := resolveInput(flags, input)
	if title == "" {
		out.Error = envelope.FatalError("title is required for add action")
		return out
	}

	priority := 3
	if p, err := strconv.Atoi(flags["priority"]); err == nil && p >= 1 && p <= 5 {
		priority = p
	}

	dueDate := resolveDueDate(flags["due"])

	var tags []string
	if t := flags["tags"]; t != "" {
		tags = strings.Split(t, ",")
	}

	todo, err := s.AddTodo(title, priority, dueDate, tags)
	if err != nil {
		logger.Error("add todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to add todo: %v", err))
		return out
	}

	signal := envelope.ContentToText(input.Content, input.ContentType)
	memID, err := s.SaveInvocation("todo", signal, "Added todo: "+title)
	if err != nil {
		logger.Warn("failed to save invocation memory", "error", err)
	} else if memID != "" {
		if err := s.SetTodoMemoryID(todo.ID, memID); err != nil {
			logger.Warn("failed to link memory to todo", "error", err)
		}
		todo.MemoryID = memID
	}

	logger.Info("added todo", "id", todo.ID, "title", title)
	m := todoToMap(todo)
	m["action"] = "add"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func handleList(s *store.Store, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "list")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	status := flags["status"]
	if status == "" {
		status = store.TodoStatusPending
	}

	limit := 25
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	todos, err := s.ListTodos(status, limit)
	if err != nil {
		logger.Error("list todos failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to list todos: %v", err))
		return out
	}

	logger.Info("listed todos", "count", len(todos), "status", status)
	items := make([]map[string]any, len(todos))
	for i, t := range todos {
		items[i] = todoToMap(t)
	}
	out.Content = items
	out.ContentType = envelope.ContentList
	return out
}

func handleDone(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "done")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	todo, err := resolveTodo(s, input, flags, store.TodoStatusPending)
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	if err := s.CompleteTodo(todo.ID); err != nil {
		logger.Error("complete todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to complete todo: %v", err))
		return out
	}

	signal := envelope.ContentToText(input.Content, input.ContentType)
	memID, err := s.SaveInvocation("todo", signal, "Completed todo: "+todo.Title)
	if err != nil {
		logger.Warn("failed to save completion memory", "error", err)
	} else if memID != "" && todo.MemoryID != "" {
		if err := s.CreateEdge(store.Edge{
			SourceID: memID,
			TargetID: todo.MemoryID,
			Relation: store.RelationRefinedFrom,
		}); err != nil {
			logger.Warn("failed to create refined_from edge", "error", err)
		}
	}

	todo.Status = store.TodoStatusDone
	logger.Info("completed todo", "id", todo.ID, "title", todo.Title)
	m := todoToMap(todo)
	m["action"] = "done"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func handleUndone(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "undone")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	todo, err := resolveTodo(s, input, flags, store.TodoStatusDone)
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	if err := s.UncompleteTodo(todo.ID); err != nil {
		logger.Error("uncomplete todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to uncomplete todo: %v", err))
		return out
	}

	todo.Status = store.TodoStatusPending
	logger.Info("uncompleted todo", "id", todo.ID, "title", todo.Title)
	m := todoToMap(todo)
	m["action"] = "undone"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func handleRemove(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "remove")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	todo, err := resolveTodo(s, input, flags, "all")
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	if err := s.DeleteTodo(todo.ID); err != nil {
		logger.Error("delete todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to remove todo: %v", err))
		return out
	}

	logger.Info("removed todo", "id", todo.ID)
	out.Content = map[string]any{"status": "removed", "id": todo.ID}
	out.ContentType = envelope.ContentStructured
	return out
}

func handleEdit(s *store.Store, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "edit")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	id := flags["id"]
	if id == "" {
		out.Error = envelope.FatalError("id is required for edit action")
		return out
	}

	updates := map[string]string{}
	if v := flags["title"]; v != "" {
		updates["title"] = v
	}
	if v := flags["priority"]; v != "" {
		updates["priority"] = v
	}
	if v := flags["due"]; v != "" {
		updates["due_date"] = resolveDueDate(v)
	}
	if v := flags["tags"]; v != "" {
		updates["tags"] = v
	}

	if err := s.UpdateTodo(id, updates); err != nil {
		logger.Error("update todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to edit todo: %v", err))
		return out
	}

	todo, err := s.GetTodo(id)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("failed to get updated todo: %v", err))
		return out
	}

	logger.Info("edited todo", "id", id)
	out.Content = todoToMap(todo)
	out.ContentType = envelope.ContentStructured
	return out
}

func handleReorder(s *store.Store, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "reorder")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	id := flags["id"]
	if id == "" {
		out.Error = envelope.FatalError("id is required for reorder action")
		return out
	}

	priority, err := strconv.Atoi(flags["priority"])
	if err != nil || priority < 1 || priority > 5 {
		out.Error = envelope.FatalError("priority must be 1–5 for reorder action")
		return out
	}

	if err := s.ReorderTodo(id, priority); err != nil {
		logger.Error("reorder todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to reorder todo: %v", err))
		return out
	}

	logger.Info("reordered todo", "id", id, "priority", priority)
	out.Content = map[string]any{"status": "reordered", "id": id, "priority": priority}
	out.ContentType = envelope.ContentStructured
	return out
}

func resolveTodo(s *store.Store, input envelope.Envelope, flags map[string]string, status string) (store.Todo, error) {
	if id := flags["id"]; id != "" {
		todo, err := s.GetTodo(id)
		if err != nil {
			if err == sql.ErrNoRows {
				return store.Todo{}, fmt.Errorf("todo not found: %s", id)
			}
			return store.Todo{}, fmt.Errorf("failed to get todo: %v", err)
		}
		return todo, nil
	}
	search := resolveInput(flags, input)
	if search == "" {
		return store.Todo{}, fmt.Errorf("id or title is required")
	}
	return fuzzyFind(s, search, status)
}

func resolveInput(flags map[string]string, input envelope.Envelope) string {
	if v := flags["title"]; v != "" {
		return v
	}
	if v := flags["topic"]; v != "" {
		return v
	}
	return envelope.ContentToText(input.Content, input.ContentType)
}

func fuzzyFind(s *store.Store, search string, status string) (store.Todo, error) {
	todos, err := s.ListTodos(status, 100)
	if err != nil {
		return store.Todo{}, fmt.Errorf("failed to search todos: %v", err)
	}
	lower := strings.ToLower(search)
	for _, t := range todos {
		titleLower := strings.ToLower(t.Title)
		// Match if title contains search OR search contains title (handles full-signal inputs)
		if strings.Contains(titleLower, lower) || strings.Contains(lower, titleLower) {
			return t, nil
		}
	}
	return store.Todo{}, fmt.Errorf("no %s todo found matching %q", status, search)
}

func resolveDueDate(due string) string {
	if due == "" {
		return ""
	}
	// Try ISO date passthrough
	if len(due) == 10 && due[4] == '-' && due[7] == '-' {
		return due
	}
	// Try natural language
	if result, err := whenParser.Parse(due, time.Now()); err == nil && result != nil {
		return result.Time.Format("2006-01-02")
	}
	return due
}

func todoToMap(t store.Todo) map[string]any {
	m := map[string]any{
		"id":       t.ID,
		"title":    t.Title,
		"status":   t.Status,
		"priority": t.Priority,
	}
	if t.DueDate != "" {
		m["due_date"] = t.DueDate
	}
	if len(t.Tags) > 0 {
		m["tags"] = t.Tags
	}
	if !t.CreatedAt.IsZero() {
		m["created_at"] = t.CreatedAt.Format(time.RFC3339)
	}
	if !t.CompletedAt.IsZero() {
		m["completed_at"] = t.CompletedAt.Format(time.RFC3339)
	}
	return m
}
