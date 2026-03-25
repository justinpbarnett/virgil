package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/db"
	"github.com/justinpbarnett/virgil/internal/observe"
)

// RegisterTaskTools registers task_list, task_create, and task_complete.
func RegisterTaskTools(reg *Registry, database *sql.DB) {
	reg.Register(&internal.Tool{
		Name:        "task_list",
		Description: "List tasks from the internal tracker.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type": "string",
					"enum": []string{"open", "done", "dropped"},
				},
				"priority": map[string]any{
					"type": "string",
					"enum": []string{"urgent", "high", "normal", "low"},
				},
				"bridge": map[string]any{
					"type":        "string",
					"description": "Filter by org scope",
				},
				"source": map[string]any{
					"type":        "string",
					"description": "Filter by source (email, meeting, jira, user)",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
			},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			var where []string
			var args []any

			if v, ok := params["status"].(string); ok && v != "" {
				where = append(where, "status = ?")
				args = append(args, v)
			}
			if v, ok := params["priority"].(string); ok && v != "" {
				where = append(where, "priority = ?")
				args = append(args, v)
			}
			if v, ok := params["bridge"].(string); ok && v != "" {
				where = append(where, "bridge = ?")
				args = append(args, v)
			}
			if v, ok := params["source"].(string); ok && v != "" {
				where = append(where, "source = ?")
				args = append(args, v)
			}

			limit := intParam(params, "limit", 20)

			query := "SELECT id, title, description, status, priority, source, source_id, bridge, due_at, created_at, completed_at FROM tasks"
			if len(where) > 0 {
				query += " WHERE " + strings.Join(where, " AND ")
			}
			query += " ORDER BY created_at DESC LIMIT ?"
			args = append(args, limit)

			rows, err := database.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("task_list: %w", err)
			}
			defer rows.Close()

			var tasks []map[string]any
			for rows.Next() {
				var id, title, status, priority, createdAt string
				var description, source, sourceID, bridge, dueAt, completedAt sql.NullString

				if err := rows.Scan(&id, &title, &description, &status, &priority, &source, &sourceID, &bridge, &dueAt, &createdAt, &completedAt); err != nil {
					return nil, fmt.Errorf("task_list scan: %w", err)
				}

				task := map[string]any{
					"id":         id,
					"title":      title,
					"status":     status,
					"priority":   priority,
					"created_at": createdAt,
				}
				if description.Valid {
					task["description"] = description.String
				}
				if source.Valid {
					task["source"] = source.String
				}
				if sourceID.Valid {
					task["source_id"] = sourceID.String
				}
				if bridge.Valid {
					task["bridge"] = bridge.String
				}
				if dueAt.Valid {
					task["due_at"] = dueAt.String
				}
				if completedAt.Valid {
					task["completed_at"] = completedAt.String
				}
				tasks = append(tasks, task)
			}
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("task_list rows: %w", err)
			}
			if tasks == nil {
				tasks = []map[string]any{}
			}
			return &internal.ToolResult{Data: tasks}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "task_create",
		Description: "Create a task in the internal tracker.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"priority": map[string]any{
					"type":    "string",
					"enum":    []string{"urgent", "high", "normal", "low"},
					"default": "normal",
				},
				"source":    map[string]any{"type": "string"},
				"source_id": map[string]any{"type": "string"},
				"bridge":    map[string]any{"type": "string"},
				"due_at":    map[string]any{"type": "string"},
			},
			"required": []string{"title"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			title, _ := params["title"].(string)
			if title == "" {
				return &internal.ToolResult{Error: "title is required"}, nil
			}

			description, _ := params["description"].(string)
			priority, _ := params["priority"].(string)
			if priority == "" {
				priority = "normal"
			}
			source, _ := params["source"].(string)
			sourceID, _ := params["source_id"].(string)
			bridge, _ := params["bridge"].(string)
			dueAtStr, _ := params["due_at"].(string)

			var dueAt *time.Time
			if dueAtStr != "" {
				t := parseDateTime(dueAtStr)
				if t.IsZero() {
					return &internal.ToolResult{Error: fmt.Sprintf("could not parse due_at %q", dueAtStr)}, nil
				}
				dueAt = &t
			}

			id := generateTaskID()
			_, err := database.ExecContext(ctx,
				`INSERT INTO tasks (id, title, description, priority, source, source_id, bridge, due_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				id, title, db.NullStr(description), priority, db.NullStr(source), db.NullStr(sourceID), db.NullStr(bridge), dueAt,
			)
			if err != nil {
				return nil, fmt.Errorf("task_create: %w", err)
			}

			return &internal.ToolResult{Data: map[string]any{
				"id":       id,
				"title":    title,
				"priority": priority,
				"status":   "open",
			}}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "task_complete",
		Description: "Mark a task as done.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
			"required": []string{"task_id"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			taskID, _ := params["task_id"].(string)
			if taskID == "" {
				return &internal.ToolResult{Error: "task_id is required"}, nil
			}

			result, err := database.ExecContext(ctx,
				"UPDATE tasks SET status = 'done', completed_at = datetime('now') WHERE id = ?",
				taskID,
			)
			if err != nil {
				return nil, fmt.Errorf("task_complete: %w", err)
			}

			rows, _ := result.RowsAffected()
			if rows == 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("task %q not found", taskID)}, nil
			}

			return &internal.ToolResult{Data: map[string]any{
				"task_id": taskID,
				"status":  "done",
			}}, nil
		},
	})
}

func generateTaskID() string {
	return observe.GenerateSpanID()
}
