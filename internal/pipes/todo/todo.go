package todo

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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

type todoData struct {
	Status     string `json:"status"`
	Priority   int    `json:"priority"`
	DueDate    string `json:"due_date,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Details    string `json:"details,omitempty"`
}

func parseTodoData(raw string) todoData {
	var d todoData
	if raw != "" {
		json.Unmarshal([]byte(raw), &d) //nolint:errcheck
	}
	return d
}

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
		case "detail":
			return handleDetail(s, input, flags, logger)
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

	data := todoData{
		Status:     "pending",
		Priority:   priority,
		DueDate:    dueDate,
		ExternalID: flags["external_id"],
		Details:    flags["details"],
	}

	id, err := s.SaveKind(store.KindTodo, title, data, tags, nil)
	if err != nil {
		logger.Error("add todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to add todo: %v", err))
		return out
	}

	logger.Info("added todo", "id", id, "title", title)
	m := memToMap(id, title, data, tags, time.Now())
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
		status = "pending"
	}

	limit := 25
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	var entries []store.Memory
	var err error
	if status == "all" {
		entries, err = s.QueryByKind(store.KindTodo, limit+20)
		if err == nil {
			filtered := entries[:0]
			for _, e := range entries {
				d := parseTodoData(e.Data)
				if d.Status != "removed" {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
			if len(entries) > limit {
				entries = entries[:limit]
			}
		}
	} else {
		entries, err = s.QueryByKindFiltered(store.KindTodo, map[string]any{"status": status}, limit)
	}
	if err != nil {
		logger.Error("list todos failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to list todos: %v", err))
		return out
	}

	sort.Slice(entries, func(i, j int) bool {
		di := parseTodoData(entries[i].Data)
		dj := parseTodoData(entries[j].Data)
		if di.Priority != dj.Priority {
			return di.Priority < dj.Priority
		}
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})

	logger.Info("listed todos", "count", len(entries), "status", status)
	items := make([]map[string]any, len(entries))
	for i, e := range entries {
		d := parseTodoData(e.Data)
		items[i] = memToMap(e.ID, e.Content, d, e.Tags, e.CreatedAt)
	}
	out.Content = items
	out.ContentType = envelope.ContentList
	return out
}

func handleDone(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "done")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	mem, err := resolveTodo(s, input, flags, "pending")
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	d := parseTodoData(mem.Data)
	d.Status = "done"

	newID, err := s.SupersedeMemory(mem.ID, mem.Content, d, mem.Tags)
	if err != nil {
		logger.Error("complete todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to complete todo: %v", err))
		return out
	}

	logger.Info("completed todo", "id", newID, "title", mem.Content)
	m := memToMap(newID, mem.Content, d, mem.Tags, mem.CreatedAt)
	m["action"] = "done"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func handleUndone(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "undone")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	mem, err := resolveTodo(s, input, flags, "done")
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	d := parseTodoData(mem.Data)
	d.Status = "pending"

	newID, err := s.SupersedeMemory(mem.ID, mem.Content, d, mem.Tags)
	if err != nil {
		logger.Error("uncomplete todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to uncomplete todo: %v", err))
		return out
	}

	logger.Info("uncompleted todo", "id", newID, "title", mem.Content)
	m := memToMap(newID, mem.Content, d, mem.Tags, mem.CreatedAt)
	m["action"] = "undone"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func handleRemove(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "remove")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	mem, err := resolveTodo(s, input, flags, "all")
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	d := parseTodoData(mem.Data)
	d.Status = "removed"

	_, err = s.SupersedeMemory(mem.ID, mem.Content, d, mem.Tags)
	if err != nil {
		logger.Error("remove todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to remove todo: %v", err))
		return out
	}

	logger.Info("removed todo", "id", mem.ID, "title", mem.Content)
	out.Content = map[string]any{"action": "remove", "status": "removed", "id": mem.ID, "title": mem.Content}
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

	mem, err := s.GetMemory(id)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("todo not found: %s", id))
		return out
	}

	d := parseTodoData(mem.Data)
	content := mem.Content
	tags := mem.Tags

	if v := flags["title"]; v != "" {
		content = v
	}
	if v := flags["priority"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 1 && p <= 5 {
			d.Priority = p
		}
	}
	if v := flags["due"]; v != "" {
		d.DueDate = resolveDueDate(v)
	}
	if v := flags["tags"]; v != "" {
		tags = strings.Split(v, ",")
	}

	if err := s.UpdateMemory(id, content, d, tags); err != nil {
		logger.Error("update todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to edit todo: %v", err))
		return out
	}

	logger.Info("edited todo", "id", id)
	out.Content = memToMap(id, content, d, tags, mem.CreatedAt)
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
		out.Error = envelope.FatalError("priority must be 1-5 for reorder action")
		return out
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		out.Error = envelope.FatalError(fmt.Sprintf("todo not found: %s", id))
		return out
	}

	d := parseTodoData(mem.Data)
	d.Priority = priority

	if err := s.UpdateData(id, d); err != nil {
		logger.Error("reorder todo failed", "error", err)
		out.Error = envelope.FatalError(fmt.Sprintf("failed to reorder todo: %v", err))
		return out
	}

	logger.Info("reordered todo", "id", id, "priority", priority)
	out.Content = map[string]any{"action": "reorder", "status": "reordered", "id": id, "priority": priority}
	out.ContentType = envelope.ContentStructured
	return out
}

func handleDetail(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("todo", "detail")
	out.Args = flags
	defer func() { out.Duration = time.Since(out.Timestamp) }()

	mem, err := resolveTodo(s, input, flags, "all")
	if err != nil {
		out.Error = envelope.FatalError(err.Error())
		return out
	}

	d := parseTodoData(mem.Data)
	logger.Info("detail todo", "id", mem.ID, "title", mem.Content)
	m := memToMap(mem.ID, mem.Content, d, mem.Tags, mem.CreatedAt)
	m["action"] = "detail"
	out.Content = m
	out.ContentType = envelope.ContentStructured
	return out
}

func resolveTodo(s *store.Store, input envelope.Envelope, flags map[string]string, status string) (store.Memory, error) {
	if id := flags["id"]; id != "" {
		mem, err := s.GetMemory(id)
		if err != nil {
			return store.Memory{}, fmt.Errorf("todo not found: %s", id)
		}
		return mem, nil
	}
	search := resolveInput(flags, input)
	if search == "" {
		return store.Memory{}, fmt.Errorf("id or title is required")
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

func fuzzyFind(s *store.Store, search string, status string) (store.Memory, error) {
	var entries []store.Memory
	var err error
	if status == "all" {
		entries, err = s.QueryByKind(store.KindTodo, 100)
	} else {
		entries, err = s.QueryByKindFiltered(store.KindTodo, map[string]any{"status": status}, 100)
	}
	if err != nil {
		return store.Memory{}, fmt.Errorf("failed to search todos: %v", err)
	}

	lower := strings.ToLower(search)
	searchWords := strings.Fields(lower)

	var bestMem store.Memory
	bestHits := 0
	for _, e := range entries {
		titleLower := strings.ToLower(e.Content)
		if strings.Contains(titleLower, lower) || strings.Contains(lower, titleLower) {
			return e, nil
		}

		if len(searchWords) == 0 {
			continue
		}
		titleWords := strings.Fields(titleLower)
		hits := 0
		for _, sw := range searchWords {
			for _, tw := range titleWords {
				if sw == tw || strings.Contains(tw, sw) || strings.Contains(sw, tw) {
					hits++
					break
				}
			}
		}
		if hits > bestHits {
			bestHits = hits
			bestMem = e
		}
	}

	minRequired := 2
	if len(searchWords) < minRequired {
		minRequired = len(searchWords)
	}
	if bestHits >= minRequired {
		return bestMem, nil
	}

	return store.Memory{}, fmt.Errorf("no %s todo found matching %q", status, search)
}

func resolveDueDate(due string) string {
	if due == "" {
		return ""
	}
	if len(due) == 10 && due[4] == '-' && due[7] == '-' {
		return due
	}
	if result, err := whenParser.Parse(due, time.Now()); err == nil && result != nil {
		return result.Time.Format("2006-01-02")
	}
	return due
}

func memToMap(id, title string, d todoData, tags []string, createdAt time.Time) map[string]any {
	m := map[string]any{
		"id":       id,
		"title":    title,
		"status":   d.Status,
		"priority": d.Priority,
	}
	if d.DueDate != "" {
		m["due_date"] = d.DueDate
	}
	if len(tags) > 0 {
		m["tags"] = tags
	}
	if d.ExternalID != "" {
		m["external_id"] = d.ExternalID
	}
	if d.Details != "" {
		m["details"] = d.Details
	}
	if !createdAt.IsZero() {
		m["created_at"] = createdAt.Format(time.RFC3339)
	}
	return m
}
