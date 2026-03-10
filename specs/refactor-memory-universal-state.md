# Refactor: Memory as Universal State Layer

## Metadata

type: `refactor`
task_id: `memory-universal-state`
prompt: `Unify all persistent state into the memory system. No separate tables for todos, reminders, goals, or anything else. One table (memories), one edge table (memory_edges), one FTS5 index. Everything that persists is a memory entry differentiated by kind, with structured data and edges. Pipes own interpretation, memory owns storage.`

## Refactor Description

Virgil currently has two persistent state systems: the `memories` table (3 kinds: explicit, working_state, invocation) and the `todos` table (dedicated schema with 15 store methods). The vision is to eliminate all state outside memory. Every persistent thing -- todos, reminders, goals, summaries, metrics -- is a memory entry with a `kind` discriminator and structured data. Pipes are filters over memory, not owners of storage.

This refactor builds on the already-completed memory-as-infrastructure work (context injection, auto-save). The next step is to make memory the *only* state layer, starting with migrating todos and establishing the patterns that future kinds (reminders, goals) will follow.

## Current State

**Schema** -- Two separate persistence systems:
- `memories` table (`internal/store/migrations/001_initial_schema.sql`) with `memories_fts` FTS5 index and `memory_edges` graph
- `todos` table (`internal/store/migrations/003_todos.sql`, `004_todo_external_id_details.sql`) with dedicated columns: title, status, priority, due_date, tags, pipe_affinity, memory_id, external_id, details

**Store** (`internal/store/store.go`) -- 1246 lines. Memory methods (Save, Search, PutState, GetState, SaveInvocation, RetrieveContext) plus ~15 todo-specific methods (AddTodo, ListTodos, GetTodo, CompleteTodo, DeleteTodo, UpdateTodo, ReorderTodo, UpsertTodoByExternalID, etc.). The `Memory` struct has no structured data field -- content is plain text.

**Todo pipe** (`internal/pipes/todo/todo.go`) -- 442 lines. Eight actions (add, list, done, undone, remove, edit, reorder, detail) that call store todo methods directly. Memory disabled in pipe.yaml. On add/done, saves invocation memories and creates edges as side effects, but the source of truth is the todos table.

**Kind taxonomy** -- Only 3 kinds: `explicit` (confidence 0.9), `working_state` (0.7), `invocation` (0.5). No support for kind-specific structured data.

**Miss log** (`internal/router/misslog.go`) -- JSONL file, not in memory system. Second persistent store outside SQLite.

**Context assembly** (`store.go:1053`) -- `RetrieveContext` gathers from fixed source types (topic_history, working_state, recent_history, user_preferences, relational). No kind-filtered retrieval.

## Target State

**One table, one index, one graph.** The `memories` table gains a `data TEXT` column for kind-specific structured JSON. The `todos` table is dropped. All persistent state lives in memories.

**Extended kind taxonomy:**

| Kind | Confidence | Content (FTS-indexed) | Data (JSON, machine-readable) |
|---|---|---|---|
| `explicit` | 0.9 | User-stated fact | null |
| `working_state` | 0.7 | Artifact content | null |
| `invocation` | 0.5 | "signal -> output" | null |
| `todo` | 0.8 | Task title/description | `{status, priority, due_date, external_id, details}` |
| `reminder` | 0.8 | Reminder description | `{fire_at, status, snooze_count}` |
| `goal` | 0.9 | Goal objective | `{status, blocked_on, progress_pct}` |
| `summary` | 0.6 | Condensed narrative | `{summary_level, period_start, period_end, source_count}` |

**Supersession chains for semantic state transitions.** When a todo changes state (pending -> done, done -> pending), a new memory entry is created. A `refined_from` edge links new -> old. The old entry stays (history preserved). The "current" version is the entry not targeted by a refined_from edge. Minor edits (title, priority, due date) use in-place `UpdateData` to avoid chain bloat.

**Kind-filtered store methods.** A single `SaveKind(kind, content, data, tags)` method replaces kind-specific insert methods. A single `QueryByKind(kind, filters, limit)` method replaces kind-specific list methods. Filters operate on `json_extract(data, ...)` for structured queries.

**Unified retrieval.** `RetrieveContext` gains a `kind_filter` context request type. The planner can request "active goals" or "pending todos" as part of context assembly, using the same scoring/budget system as other memory.

**Todo pipe becomes a memory pipe with filters.** It reads/writes memory entries of kind `todo` through the same infrastructure. No dedicated table, no dedicated store methods. The pipe owns the content schema interpretation, not the storage.

## Relevant Files

- `internal/store/store.go` -- Add `data` field to Memory struct, add generic kind-aware CRUD methods, remove todo-specific methods after migration
- `internal/store/graph.go` -- Add `CurrentInChain(id)` helper for finding the latest entry in a supersession chain
- `internal/store/scoring.go` -- Add confidence defaults for new kinds
- `internal/store/todo_test.go` -- Rewrite to test kind-based queries instead of dedicated todo table
- `internal/pipes/todo/todo.go` -- Rewrite all handlers to use kind-based memory operations
- `internal/pipes/todo/todo_test.go` -- Update tests for new storage model
- `internal/runtime/memory.go` -- Update context assembly to support kind-filtered requests

### New Files

- `internal/store/migrations/006_memory_data_column.sql` -- Add `data TEXT` column to memories
- `internal/store/migrations/007_migrate_todos_to_memories.sql` -- Data migration from todos to memories
- `internal/store/migrations/008_drop_todos.sql` -- Drop todos table
- `internal/store/kind.go` -- Kind constants, content schemas, kind-specific helpers

## Migration Strategy

This refactor is incremental. Each phase is independently deployable and testable. The todos table coexists with memory-based todos during transition.

**Phase 1: Schema + generic methods.** Add `data` column to memories. Add kind-aware CRUD methods to the store. No behavioral change -- existing code continues to use the todos table.

**Phase 2: Todo pipe rewrite.** Rewrite the todo pipe to read/write memory entries with kind=todo. Both storage paths work during transition (old todos table still exists but is no longer written to).

**Phase 3: Data migration.** Migrate existing todos to memory entries. Preserve IDs, edges, and timestamps. Drop the todos table.

**Phase 4: Context assembly.** Add kind-filtered retrieval to `RetrieveContext`. The planner can inject active todos/goals into pipe context. Enable memory in the todo pipe's pipe.yaml.

**Phase 5 (follow-up specs): New kinds.** Reminder kind + runtime polling, goal kind + progress chains, tiered summarization, miss log migration. These follow the patterns established in phases 1-4.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add kind constants and data field to Memory struct

In `internal/store/kind.go` (new file):

- Define kind constants: `KindExplicit`, `KindWorkingState`, `KindInvocation`, `KindTodo`, `KindReminder`, `KindGoal`, `KindSummary`
- Define confidence defaults per kind: `DefaultConfidence(kind string) float64`
- Move existing confidence constants from `scoring.go` and add new ones (todo: 0.8, reminder: 0.8, goal: 0.9, summary: 0.6)

In `internal/store/store.go`:

- Add `Data string` field to `Memory` struct with `json:"data,omitempty"` tag
- Update all scan functions that read Memory to include the data column (handle NULL as empty string)

### 2. Add data column migration

In `internal/store/migrations/006_memory_data_column.sql`:

```sql
-- +goose Up
ALTER TABLE memories ADD COLUMN data TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN in older versions; this is a no-op
```

### 3. Add generic kind-aware CRUD methods

In `internal/store/store.go`:

- `SaveKind(kind, content string, data any, tags []string, validUntil *time.Time) (string, error)` -- Inserts a memory entry with the given kind. Marshals `data` to JSON if non-nil. Uses `DefaultConfidence(kind)`. Returns the new ID.
- `QueryByKind(kind string, limit int) ([]Memory, error)` -- Returns all non-expired, non-superseded entries of the given kind, ordered by created_at DESC.
- `QueryByKindFiltered(kind string, jsonFilters map[string]any, limit int) ([]Memory, error)` -- Like QueryByKind but adds `json_extract(data, '$.key') = value` filters. For querying pending todos, active reminders, etc.
- `SearchByKind(query string, kind string, limit int) ([]Memory, error)` -- FTS search restricted to a specific kind.
- `GetMemory(id string) (Memory, error)` -- Get a single memory by ID (already partially exists but needs the data column).
- `UpdateData(id string, data any) error` -- Updates the `data` JSON column in place for minor edits (title, priority, due date changes). Does not create a new entry or supersession chain. Updates `updated_at` timestamp.
- `SupersedeMemory(oldID string, newContent string, newData any, newTags []string) (string, error)` -- Creates a new memory entry with the same kind as the old one, creates a refined_from edge (new -> old), and halves the old entry's confidence. Returns the new ID. Used for semantic state transitions (pending -> done).

### 4. Add IsSuperseded helper

In `internal/store/graph.go`:

- `IsSuperseded(id string) (bool, error)` -- Returns true if the memory is the target of any `refined_from` edge (meaning something has replaced it).
- Update `QueryByKind` and `QueryByKindFiltered` to exclude superseded entries by default: `AND m.id NOT IN (SELECT target_id FROM memory_edges WHERE relation = 'refined_from')`.

### 5. Rewrite todo pipe to use memory-backed storage

In `internal/pipes/todo/todo.go`:

- `handleAdd`: Call `s.SaveKind("todo", title, todoData{Status: "pending", Priority: priority, DueDate: dueDate, ExternalID: externalID, Details: details}, tags, nil)` instead of `s.AddTodo(...)`.
- `handleList`: Call `s.QueryByKindFiltered("todo", map[string]any{"status": statusFilter}, limit)` instead of `s.ListTodos(...)`. Parse data JSON into todoData struct for formatting.
- `handleDone`: Call `s.SupersedeMemory(todo.ID, todo.Content, todoData{...Status: "done"...}, todo.Tags)` instead of `s.CompleteTodo(...)`. The new entry carries the same content but with status=done in data. This is a semantic state transition, so supersession preserves the history.
- `handleUndone`: Call `s.SupersedeMemory(todo.ID, todo.Content, todoData{...Status: "pending"...}, todo.Tags)`. Reverse of done -- also a semantic transition.
- `handleRemove`: Call `s.SupersedeMemory(todo.ID, todo.Content, todoData{...Status: "removed"...}, todo.Tags)`. Soft-delete via supersession so history is preserved and context assembly can still reference "removed 3 tasks about OAuth."
- `handleEdit`: Call `s.UpdateData(todo.ID, updatedData)`. Minor edit -- in-place update, no supersession chain. If the title changes, also update `content` column directly so FTS stays current.
- `handleReorder`: Call `s.UpdateData(todo.ID, todoData{...Priority: newPriority...})`. Minor edit -- in-place update.
- `handleDetail`: Call `s.GetMemory(id)`, parse data JSON.
- `resolveTodo` / `fuzzyFind`: Rewrite to use `s.QueryByKindFiltered` + `s.SearchByKind` instead of `s.ListTodos`.

Define `todoData` struct in the todo package:
```go
type todoData struct {
    Status     string `json:"status"`
    Priority   int    `json:"priority"`
    DueDate    string `json:"due_date,omitempty"`
    ExternalID string `json:"external_id,omitempty"`
    Details    string `json:"details,omitempty"`
}
```

The pipe parses `memory.Data` into this struct using `json.Unmarshal`. The store doesn't know about todoData -- it stores raw JSON.

When superseding (done, undone, remove), copy `content` exactly from the old entry. Semantic meaning lives in `data.status`, not content text. This keeps FTS clean -- searching "OAuth" finds both the original and completion entries, and the pipe distinguishes by parsing data.

### 6. Handle external ID upsert for Jira sync

External IDs (e.g., "jira:PTP-123") currently use a dedicated column with UNIQUE constraint. In the memory model:

- Store external_id in the data JSON field.
- Add `FindByKindAndDataField(kind, jsonPath string, value any) (Memory, error)` to the store. Uses `json_extract(data, jsonPath) = ?` with kind filter.
- `UpsertTodoByExternalID` becomes: find existing via `FindByKindAndDataField("todo", "$.external_id", externalID)`. If found, supersede it. If not, create new.
- `ListTodosWithExternalIDPrefix` becomes: `QueryByKindFiltered("todo", map[string]any{"external_id LIKE": prefix + "%"}, limit)` -- or a dedicated method since LIKE queries on json_extract need special handling.

### 7. Update todo pipe.yaml to enable memory

In `internal/pipes/todo/pipe.yaml`:

- Change `memory: disabled: true` to:
```yaml
memory:
  context:
    - type: kind_filter
      kind: todo
      depth: 30d
    - type: kind_filter
      kind: goal
  budget: 1000
```

This lets the todo pipe receive active goals as context, enabling it to link new todos to relevant goals.

### 8. Data migration: todos to memories

In `internal/store/migrations/007_migrate_todos_to_memories.sql`:

```sql
-- +goose Up
INSERT INTO memories (id, created_at, updated_at, kind, source_pipe, content, tags, confidence, data)
SELECT
    id,
    created_at,
    COALESCE(completed_at, created_at),
    'todo',
    'todo',
    title,
    tags,
    0.8,
    json_object(
        'status', status,
        'priority', priority,
        'due_date', COALESCE(due_date, ''),
        'external_id', COALESCE(external_id, ''),
        'details', COALESCE(details, '')
    )
FROM todos
WHERE id NOT IN (SELECT id FROM memories);

-- Preserve memory_id edges: if a todo had a memory_id linking to its creation invocation,
-- create a produced_by edge from the todo memory to that invocation memory.
INSERT OR IGNORE INTO memory_edges (id, source_id, target_id, relation, strength, created_at)
SELECT
    lower(hex(randomblob(16))),
    t.id,
    t.memory_id,
    'produced_by',
    1.0,
    t.created_at
FROM todos t
WHERE t.memory_id IS NOT NULL AND t.memory_id != ''
  AND t.id IN (SELECT id FROM memories)
  AND t.memory_id IN (SELECT id FROM memories);

-- +goose Down
DELETE FROM memories WHERE kind = 'todo' AND source_pipe = 'todo';
```

### 9. Drop todos table

In `internal/store/migrations/008_drop_todos.sql`:

```sql
-- +goose Up
DROP TABLE IF EXISTS todos;

-- +goose Down
-- Recreating the table without data; the down migration of 007 restores from memories
CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 3,
    due_date TEXT,
    tags TEXT NOT NULL DEFAULT '',
    pipe_affinity TEXT NOT NULL DEFAULT '',
    memory_id TEXT,
    external_id TEXT UNIQUE,
    details TEXT,
    created_at INTEGER NOT NULL,
    completed_at INTEGER
);
```

### 10. Remove todo-specific store methods

In `internal/store/store.go`:

- Remove: `AddTodo`, `ListTodos`, `GetTodo`, `FindTodoByExternalID`, `UpsertTodoByExternalID`, `UpdateTodo`, `CompleteTodo`, `UncompleteTodo`, `DeleteTodo`, `ReorderTodo`, `SetTodoMemoryID`, `ListTodosWithExternalIDPrefix`, `scanTodos`, `scanTodoFrom`, `clampPriority`
- Remove: `Todo` struct, `TodoStatusPending`, `TodoStatusDone` constants, `todoCols` variable
- Move `todo_test.go` tests to verify the same operations via kind-based methods

### 11. Add kind_filter to context assembly

In `internal/store/store.go` (`RetrieveContext`):

- Add a new context request type `kind_filter`:
  ```go
  case "kind_filter":
      kind := req.Kind // new field on ContextRequest
      entries, err := s.QueryByKind(kind, 10)
      // ...score and add to candidates
  ```
- Add `Kind string` field to `ContextRequest`
- Add `Kind string` field to `config.MemoryContextEntry`

In `internal/runtime/memory.go`:

- Update `InjectContext` to pass the Kind field through from config to store request

### 12. Update scoring for new kinds

In `internal/store/scoring.go`:

- Add relevance constant for kind_filter results: `RelevanceKindFilter = 0.6`
- Update `defaultRelevance` to handle "kind_filter" source type

In `internal/store/kind.go`:

- `DefaultConfidence` returns appropriate confidence per kind (used by `SaveKind`)

### 13. Write comprehensive tests

In `internal/store/kind_test.go` (new file):

- Test `SaveKind` creates entries with correct kind, content, and data JSON
- Test `QueryByKind` returns only entries of the specified kind, excludes expired, excludes superseded
- Test `QueryByKindFiltered` applies json_extract filters correctly
- Test `SearchByKind` restricts FTS results to a single kind
- Test `UpdateData` modifies data JSON in place without creating new entries
- Test `SupersedeMemory` creates new entry, creates refined_from edge, halves old confidence
- Test `FindByKindAndDataField` locates entries by JSON field value
- Test supersession chain: create 3 versions, only the latest appears in QueryByKind

In `internal/pipes/todo/todo_test.go`:

- Rewrite all existing tests to verify the same behaviors via memory-backed storage
- Add test: creating a todo then marking done produces two memory entries linked by refined_from
- Add test: listing pending todos excludes done/superseded entries
- Add test: fuzzy find works on memory content field

### 14. Migrate miss log to memory (optional, follow-up)

In `internal/router/misslog.go`:

- Change from JSONL append to `SaveKind("miss", signal, missData{...}, nil, nil)`
- This eliminates the last persistent store outside SQLite
- The miss log becomes queryable via FTS and participates in context assembly

## Testing Strategy

**Existing tests that must pass unchanged:**
- `internal/store/store_test.go` -- All memory CRUD, FTS, context assembly tests
- `internal/store/graph_test.go` -- Edge creation, traversal tests
- `internal/runtime/memory_test.go` -- Injection and auto-save tests
- `internal/pipes/memory/...` -- Memory pipe tests

**Tests that will be rewritten:**
- `internal/store/todo_test.go` -- Rewritten to test kind-based operations
- `internal/pipes/todo/todo_test.go` -- Rewritten for memory-backed handlers

**New test coverage:**
- Kind-based CRUD with JSON data
- Supersession chain mechanics (create, supersede, query current)
- JSON filter queries (status, priority, external_id)
- FTS search scoped to a specific kind
- Data migration: todos table rows appear as memory entries after migration
- Context assembly with kind_filter requests
- End-to-end: add todo -> list -> done -> list shows completed -> context assembly includes todo history

## Risk Assessment

**Data migration.** Existing todos must be preserved. The migration (step 8) copies all todos to memories before the table is dropped (step 9). Goose migrations run in order. If migration 007 fails, 008 doesn't run, and the todos table remains intact. The down migration restores from memories. Risk is low but testing the migration on a copy of production data is recommended.

**Performance of json_extract queries.** SQLite's `json_extract` is not indexed. For a personal tool with hundreds of todos, this is negligible. If it ever matters, a generated column + index can be added: `ALTER TABLE memories ADD COLUMN data_status TEXT GENERATED ALWAYS AS (json_extract(data, '$.status'))`. This is a future optimization, not needed now.

**FTS noise.** The `content` field remains the human-readable text (todo title, not JSON). FTS quality is unchanged. The `data` column is not part of the FTS5 virtual table.

**Supersession chain depth.** Minor edits use in-place `UpdateData`, so chains only grow on semantic state transitions (pending -> done -> pending). A typical todo produces 1-3 entries. Old entries are prunable by the future summarization pipeline.

**External ID uniqueness.** The `todos` table had a UNIQUE constraint on `external_id`. In the memory model, uniqueness is enforced by the upsert logic in the pipe (`FindByKindAndDataField` + supersede-or-create), not by a database constraint. This is acceptable for a single-user system. If stricter guarantees are needed, a partial unique index can be added: `CREATE UNIQUE INDEX ... ON memories(json_extract(data, '$.external_id')) WHERE kind = 'todo' AND json_extract(data, '$.external_id') != ''`.

**Virgil-cloud.** Cloud imports `store.Todo`, `UpsertTodoByExternalID`, `ListTodosWithExternalIDPrefix`, etc. No backward-compatibility wrappers -- update cloud to use the new kind-based methods (`SaveKind`, `FindByKindAndDataField`, `QueryByKindFiltered`) at the same time as core. Both repos land together.

## Validation Commands

```bash
just build
just test
just lint
```

## Decisions (Resolved)

**1. Supersession vs. in-place mutation.** Supersession chains for semantic state transitions (pending -> done, done -> pending, removal). In-place `UpdateData` for minor edits (title, priority, due date, tags). Keeps chains short while preserving meaningful history.

**2. Metrics: JSONL for raw, memory for aggregates.** Raw metrics stay as JSONL files -- high write volume, low individual retrieval value. Aggregate summaries (daily/weekly) become memory entries with kind=summary. Avoids SQLite write amplification.

**3. Reminder polling: separate spec.** The `QueryByKindFiltered` method from this refactor provides the query primitive. The runtime polling loop (`kind=reminder WHERE data.fire_at <= now AND data.status = 'scheduled'`) is new infrastructure -- spec separately as `feat-reminder-kind`.

**4. Virgil-cloud: update in lockstep.** No backward-compatibility wrappers. Cloud switches to `SaveKind`, `FindByKindAndDataField`, `QueryByKindFiltered` at the same time as core. Both repos land together.

**5. Content field on supersession: copy exactly.** Semantic meaning lives in `data.status`, not content text. Keeps FTS clean -- both original and completion match the same searches. Pipes distinguish by parsing data.

## Sub-Tasks

This spec decomposes naturally into stages that can be implemented and merged independently:

1. **Schema + generic methods** (steps 1-4): Add data column, kind constants, generic CRUD. No behavioral change. ~1 session.
2. **Todo pipe rewrite** (steps 5-7): Rewrite handlers to use memory. Both storage paths work. ~1 session.
3. **Data migration + cleanup** (steps 8-10): Migrate data, drop table, remove old methods. ~1 session.
4. **Context assembly + scoring** (steps 11-12): Kind-filtered retrieval. ~1 session.
5. **Tests** (step 13): Can run in parallel with steps 1-4. ~1 session.
6. **Miss log migration** (step 14): Optional follow-up. ~1 session.
