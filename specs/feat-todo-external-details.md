# Todo External ID & Details Spec

## Metadata

type: `feat`
task_id: `todo-external-details`

## What It Does

Adds two new fields to the todo system — `external_id` and `details` — enabling todos to be linked to external sources (Jira tickets, Slack threads, etc.) and to carry rich context beyond a one-line title.

1. **external_id** — A canonical dedup key for synced todos. Format: `{source}:{identifier}` (e.g., `jira:PTP-123`, `slack:C07ABC:1709234567.123`). Manual todos won't have one. UNIQUE constraint prevents duplicate imports.
2. **details** — A rich context blob. Holds JIRA descriptions + comments, Slack thread snippets, or any extended context. Sections separated by markdown-style headers.
3. **detail action** — A new pipe action for drilldown into a single todo's full details.

This is a schema extension + pipe enhancement — no new pipes, no new dependencies.

---

## Schema Changes

New migration file: `internal/store/migrations/004_todo_external_id_details.sql`

```sql
-- +goose Up
ALTER TABLE todos ADD COLUMN external_id TEXT;
ALTER TABLE todos ADD COLUMN details TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_external_id ON todos(external_id) WHERE external_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_todos_external_id;
ALTER TABLE todos DROP COLUMN details;
ALTER TABLE todos DROP COLUMN external_id;
```

**Notes:**

- Both columns are nullable. Existing todos get `NULL` for both — no backfill needed.
- The UNIQUE index uses a partial index (`WHERE external_id IS NOT NULL`) so that multiple manual todos (with `NULL` external_id) don't conflict.
- The index on `external_id` enables fast lookups for the upsert path.

---

## Store Changes (in store.go)

### Updated Struct

```go
type Todo struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    Status      string    `json:"status"`
    Priority    int       `json:"priority"`
    DueDate     string    `json:"due_date,omitempty"`
    Tags        []string  `json:"tags,omitempty"`
    MemoryID    string    `json:"memory_id,omitempty"`
    ExternalID  string    `json:"external_id,omitempty"`
    Details     string    `json:"details,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
    CompletedAt time.Time `json:"completed_at,omitempty"`
}
```

### New Methods

#### FindTodoByExternalID

```go
func (s *Store) FindTodoByExternalID(externalID string) (Todo, error)
```

Lookup a todo by its `external_id`. Returns `sql.ErrNoRows` if not found. Uses the unique index for fast resolution.

**Behavior:**

1. Query: `SELECT {cols} FROM todos WHERE external_id = ?`
2. Scan with `scanTodoFrom`.
3. Return the todo or `sql.ErrNoRows`.

#### UpsertTodoByExternalID

```go
func (s *Store) UpsertTodoByExternalID(externalID, title, details string, priority int, dueDate string, tags []string) (Todo, bool, error)
```

Insert if `external_id` doesn't exist, update `title`, `details`, `priority`, `due_date`, and `tags` if it does. Returns the todo and whether it was created (`true`) vs updated (`false`).

**Behavior:**

1. Clamp priority to [1, 5].
2. Execute SQLite `INSERT ... ON CONFLICT(external_id) DO UPDATE`:
   ```sql
   INSERT INTO todos (id, title, status, priority, due_date, tags, external_id, details, created_at)
   VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?)
   ON CONFLICT(external_id) DO UPDATE SET
       title = excluded.title,
       details = excluded.details,
       priority = excluded.priority,
       due_date = excluded.due_date,
       tags = excluded.tags
   ```
3. Determine created vs updated: query the todo by `external_id` afterward and compare `id` to the generated one. If the IDs match, it was created; otherwise, it was updated (the existing row kept its original ID).
4. Return `(todo, created, nil)`.

**Edge cases:**

- Empty `externalID` → return error. This method requires a non-empty external ID; use `AddTodo` for manual todos.
- Idempotent re-upsert with identical data → updates the row (no-op in practice), returns `(todo, false, nil)`.

### Updated Methods

#### scanTodoFrom

Add `external_id` and `details` to the column list and scan targets:

```go
func scanTodoFrom(s todoScanner) (Todo, error) {
    var t Todo
    var tagStr, memoryID, externalID, details string
    var createdNano, completedNano int64
    if err := s.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.DueDate,
        &tagStr, &memoryID, &externalID, &details,
        &createdNano, &completedNano); err != nil {
        return Todo{}, err
    }
    t.MemoryID = memoryID
    t.ExternalID = externalID
    t.Details = details
    // ... rest unchanged ...
}
```

#### Column list constant

Update the `cols` string used by `ListTodos`, `GetTodo`, and the new `FindTodoByExternalID`:

```
id, title, status, priority, COALESCE(due_date,''), tags, COALESCE(memory_id,''), COALESCE(external_id,''), COALESCE(details,''), created_at, COALESCE(completed_at,0)
```

Extract this into a package-level constant to avoid repetition across all query methods:

```go
const todoCols = `id, title, status, priority, COALESCE(due_date,''), tags, COALESCE(memory_id,''), COALESCE(external_id,''), COALESCE(details,''), created_at, COALESCE(completed_at,0)`
```

#### AddTodo

Keep as-is. Manual todos don't have `external_id` or `details`. The new columns default to `NULL`.

#### UpdateTodo

Add `"details"` and `"external_id"` as recognized update keys:

```go
case "title", "due_date", "tags", "details", "external_id":
    setClauses = append(setClauses, k+" = ?")
    args = append(args, v)
```

#### ListTodos / GetTodo

Update the `SELECT` column list to use the new `todoCols` constant. No logic changes — the new columns flow through `scanTodoFrom` automatically.

---

## Todo Pipe Changes (in todo.go)

### New Action: `detail`

```go
case "detail":
    return handleDetail(s, input, flags, logger)
```

#### handleDetail

```go
func handleDetail(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
    out := envelope.New("todo", "detail")
    out.Args = flags
    defer func() { out.Duration = time.Since(out.Timestamp) }()

    todo, err := resolveTodo(s, input, flags, "all")
    if err != nil {
        out.Error = envelope.FatalError(err.Error())
        return out
    }

    logger.Info("detail todo", "id", todo.ID, "title", todo.Title)
    m := todoToMap(todo)
    m["action"] = "detail"
    out.Content = m
    out.ContentType = envelope.ContentStructured
    return out
}
```

**Behavior:**

1. Resolve the todo by `id` flag or fuzzy title match (reuses existing `resolveTodo` with status `"all"`).
2. Return the full todo as structured content, including `details` and `external_id` via `todoToMap`.
3. The format template (see pipe.yaml changes) handles rendering — if `details` is empty, it renders "No details available."
4. If the todo is not found, return a fatal error.

### Updated todoToMap

Include the new fields when non-empty:

```go
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
    if t.ExternalID != "" {
        m["external_id"] = t.ExternalID
    }
    if t.Details != "" {
        m["details"] = t.Details
    }
    if !t.CreatedAt.IsZero() {
        m["created_at"] = t.CreatedAt.Format(time.RFC3339)
    }
    if !t.CompletedAt.IsZero() {
        m["completed_at"] = t.CompletedAt.Format(time.RFC3339)
    }
    return m
}
```

---

## pipe.yaml Changes

### Flags

Add two new flags and extend the `action` values:

```yaml
flags:
  action:
    description: Which todo operation to perform.
    values: [add, list, done, undone, remove, edit, reorder, detail]
    default: list
  # ... existing flags unchanged ...
  details:
    description: Detailed context for the todo item (JIRA description, Slack thread, etc.).
    default: ""
  external_id:
    description: External reference ID for dedup (e.g., jira:PTP-123).
    default: ""
```

### Vocabulary

Add verbs for the detail action:

```yaml
vocabulary:
  verbs:
    # ... existing verbs unchanged ...
    detail: [todo.detail]
    details: [todo.detail]
    expand: [todo.detail]
    describe: [todo.detail]
```

### Format

Add a `detail` format template for the structured drilldown view. This won't conflict with the existing `structured` template because `detail` is keyed by the action — the runtime selects the template based on the content shape.

Update the `structured` format to handle the detail action:

```yaml
format:
  list: |-
    {{if eq .Count 0}}No todos.{{else}}{{.Count}} todo{{if gt .Count 1}}s{{end}}:{{range .Items}}
    {{if eq .status "done"}}- ~~{{.title}}~~{{else}}- {{.title}}{{end}}{{if .due_date}} -- due {{.due_date}}{{end}} . p{{.priority}}{{end}}{{end}}
  structured: |-
    {{if eq .action "detail"}}{{.title}} . p{{.priority}} . {{.status}}{{if .due_date}} -- due {{.due_date}}{{end}}{{if .external_id}}
    Source: {{.external_id}}{{end}}{{if .details}}

    {{.details}}{{else}}
    No details available.{{end}}{{else if eq .action "done"}}Completed: {{.title}}{{else if eq .action "undone"}}Restored: {{.title}}{{else}}+ Added: {{.title}}{{end}}{{if and (ne .action "detail") .due_date}} -- due {{.due_date}}{{end}}{{if ne .action "detail"}} . p{{.priority}}{{end}}
```

### Triggers

Add patterns for the detail action:

```yaml
triggers:
  # ... existing exact and keywords unchanged ...
  patterns:
    # ... existing patterns ...
    - "show me {topic}"
    - "expand {topic}"
    - "details for {topic}"
    - "what is {topic}"
```

---

## Subprocess Entry Point

No changes to `cmd/main.go`. The handler receives the new action through the existing flag dispatch. The store is already passed in and will pick up the new columns after migration.

---

## Composition

### As a source (upstream)

A detail drilldown can feed into a draft or review pipe:

```
todo(action=detail, id=abc123) -> draft(type=memo)
```

Retrieve the todo's full details, then draft a memo from the context.

```
todo(action=detail, id=abc123) -> chat
```

Retrieve the todo's details and pass them to chat for discussion.

### As a standalone

Direct user invocation for drilldown:

```
todo(action=detail, id=abc123)
todo(action=detail, title="fix auth bug")
```

### Content type conventions

| Action | content_type | Content shape                           |
| ------ | ------------ | --------------------------------------- |
| detail | structured   | Todo map with `details` and `external_id` |
| add    | structured   | Todo map (unchanged)                    |
| list   | list         | Array of todo maps (unchanged)          |
| done   | structured   | Todo map (unchanged)                    |
| edit   | structured   | Todo map (unchanged)                    |

---

## Error Handling

| Scenario                            | Severity | Retryable | Message pattern                                          |
| ----------------------------------- | -------- | --------- | -------------------------------------------------------- |
| Detail with no id or title          | fatal    | false     | `"id or title is required"`                              |
| Detail todo not found               | fatal    | false     | `"no {status} todo found matching {query}"`              |
| Upsert with empty external_id       | fatal    | false     | `"external_id is required for upsert"`                   |
| FindTodoByExternalID not found      | —        | false     | Returns `sql.ErrNoRows` (caller decides handling)        |
| Migration failure                   | fatal    | false     | Goose migration error (blocks startup)                   |

All errors are returned in the envelope — the handler never panics or exits.

---

## Testing

### Migration

1. **Columns added** — Run migration on a database with existing todos. Verify `external_id` and `details` columns exist and are `NULL` for existing rows.
2. **No data loss** — Existing todo fields (title, status, priority, due_date, tags, memory_id) are unchanged after migration.
3. **Unique constraint** — Insert two todos with the same `external_id` → second insert fails with constraint violation (unless using upsert).
4. **Null uniqueness** — Insert multiple todos with `NULL` external_id → no constraint violation (partial index).

### FindTodoByExternalID

5. **Found** — Insert a todo with `external_id = "jira:PTP-123"`, find it by that ID. Verify all fields populated correctly.
6. **Not found** — Query for a nonexistent external_id → returns `sql.ErrNoRows`.

### UpsertTodoByExternalID

7. **Create new** — Upsert with a new external_id. Returns `(todo, true, nil)`. Todo exists in the database with correct fields.
8. **Update existing** — Upsert with an existing external_id and different title/details. Returns `(todo, false, nil)`. Database row updated.
9. **Idempotent re-upsert** — Upsert with identical data twice. Both return `(todo, false, nil)` on the second call. Data unchanged.
10. **Priority clamping** — Upsert with priority 0 → clamped to 1. Upsert with priority 10 → clamped to 5.
11. **Empty external_id** — Upsert with empty string → returns error.

### Detail Action

12. **With details** — Create a todo with details, invoke `detail` action. Output contains `details` field in structured content. Format template renders the details text.
13. **Without details** — Create a todo without details, invoke `detail` action. Output has no `details` key. Format template renders "No details available."
14. **Not found** — Invoke `detail` with a nonexistent id → fatal error.
15. **Fuzzy match** — Invoke `detail` with a title substring instead of id → resolves correctly.

### todoToMap

16. **Includes external_id when present** — Todo with `external_id = "jira:PTP-123"` → map has `"external_id"` key.
17. **Includes details when present** — Todo with details → map has `"details"` key.
18. **Omits external_id when empty** — Todo with no external_id → map does not have `"external_id"` key.
19. **Omits details when empty** — Todo with no details → map does not have `"details"` key.

### UpdateTodo

20. **Update details** — Call `UpdateTodo` with `{"details": "new context"}` → details column updated.
21. **Update external_id** — Call `UpdateTodo` with `{"external_id": "jira:NEW-1"}` → external_id column updated.

### Regression

22. **AddTodo unchanged** — Add a todo without external_id or details. Verify it works identically to before.
23. **ListTodos unchanged** — List todos. Verify existing todos appear with new fields as empty/omitted.
24. **CompleteTodo unchanged** — Complete a todo. Verify no errors from new columns.
25. **DeleteTodo unchanged** — Delete a todo. Verify no errors.

### Envelope Compliance

26. **Detail action output** — Has `pipe: "todo"`, `action: "detail"`, non-zero `Timestamp`, positive `Duration`, `content_type: "structured"`, `Error: nil`.

---

## Checklist

```
Schema
  [ ] Migration file at internal/store/migrations/004_todo_external_id_details.sql
  [ ] external_id column: TEXT, nullable, UNIQUE partial index
  [ ] details column: TEXT, nullable
  [ ] Migration is idempotent (IF NOT EXISTS on index)
  [ ] Down migration drops columns and index

Store
  [ ] Todo struct has ExternalID and Details fields with omitempty JSON tags
  [ ] todoCols constant extracted for shared column list
  [ ] scanTodoFrom scans external_id and details
  [ ] FindTodoByExternalID returns todo or sql.ErrNoRows
  [ ] UpsertTodoByExternalID uses ON CONFLICT, returns (todo, created, error)
  [ ] UpsertTodoByExternalID rejects empty external_id
  [ ] UpdateTodo accepts "details" and "external_id" keys
  [ ] ListTodos and GetTodo include new columns in SELECT
  [ ] AddTodo unchanged (new columns default to NULL)

Pipe Handler
  [ ] detail action dispatched in NewHandler switch
  [ ] handleDetail resolves todo via resolveTodo with status "all"
  [ ] handleDetail returns structured content with full todo map
  [ ] todoToMap includes external_id when non-empty
  [ ] todoToMap includes details when non-empty
  [ ] todoToMap omits both fields when empty

pipe.yaml
  [ ] action flag values include "detail"
  [ ] details flag declared with description and default
  [ ] external_id flag declared with description and default
  [ ] vocabulary: detail, details, expand, describe verbs map to todo.detail
  [ ] format: structured template handles action=detail with details/no-details branches
  [ ] triggers: patterns include "show me", "expand", "details for", "what is"

Testing
  [ ] Migration adds columns without breaking existing data
  [ ] FindTodoByExternalID: found vs not found
  [ ] UpsertTodoByExternalID: create, update, idempotent, priority clamp, empty external_id
  [ ] Detail action: with details, without details, not found, fuzzy match
  [ ] todoToMap: includes new fields when present, omits when empty
  [ ] UpdateTodo: details and external_id as update keys
  [ ] Existing operations: add, list, done, delete all still work
  [ ] Envelope compliance for detail action
```
