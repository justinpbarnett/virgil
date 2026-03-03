# Feature: Memory Pipe — Working State Storage

## Metadata

type: `feat`
task_id: `memory-working-state`
prompt: `Extend the memory pipe and backing store with keyed working-state storage. Add put/get/delete operations on a separate working_state table, keyed by namespace and key. The memory pipe gains a new action value (working-state) and key/namespace flags. This enables interactive pipelines like spec to persist and retrieve evolving documents across invocations.`

## Feature Description

The memory pipe currently supports two operations: `store` (append to full-text index) and `retrieve` (FTS5 search). Both treat memory as an append-only, content-addressed log — good for "remember X" and "what do I know about Y," but wrong for working state.

Working state is different. It's a mutable document keyed by identity, not content. "The current spec for OAuth login" is one document that evolves across invocations. It needs put (create or replace), get (by key), and delete (clean up when done). It does not need full-text search — it needs exact-key lookup.

This is a new table in SQLite, new methods on `Store`, and a new action on the memory pipe. The existing store/retrieve actions are untouched.

## User Story

As a Virgil pipeline
I want to persist and retrieve mutable documents by key
So that interactive pipelines can maintain evolving state across invocations without polluting the full-text memory index

## Relevant Files

- `internal/store/store.go` — add `working_state` table, `PutState`/`GetState`/`DeleteState` methods
- `internal/store/store_test.go` — tests for new store methods (may need to be created)
- `internal/pipes/memory/pipe.yaml` — add `working-state` action, `key` and `namespace` flags
- `internal/pipes/memory/memory.go` — add `handleWorkingState` with put/get/delete sub-actions

### New Files

None.

## Implementation Plan

### Phase 1: Store Layer

Add the `working_state` table and CRUD methods to `Store`.

### Phase 2: Memory Pipe

Add the `working-state` action and supporting flags to the memory pipe.

### Phase 3: Tests

Unit tests for store methods and pipe handler.

## Step by Step Tasks

### 1. Add `working_state` table to `internal/store/store.go`

Extend the `migrate` function to create the new table:

```sql
CREATE TABLE IF NOT EXISTS working_state (
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (namespace, key)
);
```

The primary key is `(namespace, key)`. Namespace scopes keys to avoid collisions — the spec pipeline uses `"spec"` as namespace, with keys like `"oauth-login"` or `"caching-layer"`. Other pipelines use their own namespaces.

### 2. Add `PutState` method to `Store`

```go
func (s *Store) PutState(namespace, key, content string) error
```

Upsert: if `(namespace, key)` exists, update `content` and `updated_at`. If not, insert. Use SQLite `INSERT ... ON CONFLICT ... DO UPDATE`.

```go
func (s *Store) PutState(namespace, key, content string) error {
    now := time.Now()
    _, err := s.db.Exec(`
        INSERT INTO working_state (namespace, key, content, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(namespace, key) DO UPDATE SET
            content = excluded.content,
            updated_at = excluded.updated_at
    `, namespace, key, content, now, now)
    return err
}
```

### 3. Add `GetState` method to `Store`

```go
func (s *Store) GetState(namespace, key string) (string, bool, error)
```

Returns content, whether it was found, and any error. Returns `("", false, nil)` if no row exists.

```go
func (s *Store) GetState(namespace, key string) (string, bool, error) {
    var content string
    err := s.db.QueryRow(
        "SELECT content FROM working_state WHERE namespace = ? AND key = ?",
        namespace, key,
    ).Scan(&content)
    if err == sql.ErrNoRows {
        return "", false, nil
    }
    if err != nil {
        return "", false, err
    }
    return content, true, nil
}
```

### 4. Add `DeleteState` method to `Store`

```go
func (s *Store) DeleteState(namespace, key string) error
```

Deletes the row. No error if it doesn't exist.

```go
func (s *Store) DeleteState(namespace, key string) error {
    _, err := s.db.Exec(
        "DELETE FROM working_state WHERE namespace = ? AND key = ?",
        namespace, key,
    )
    return err
}
```

### 5. Add `ListState` method to `Store`

```go
func (s *Store) ListState(namespace string) ([]StateEntry, error)
```

Lists all keys in a namespace. Used for "which specs are in progress?" queries.

```go
type StateEntry struct {
    Namespace string    `json:"namespace"`
    Key       string    `json:"key"`
    Content   string    `json:"content"`
    UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListState(namespace string) ([]StateEntry, error) {
    rows, err := s.db.Query(
        "SELECT namespace, key, content, updated_at FROM working_state WHERE namespace = ? ORDER BY updated_at DESC",
        namespace,
    )
    // scan rows into []StateEntry
}
```

### 6. Update `internal/pipes/memory/pipe.yaml`

Add `working-state` to the action flag and new flags for key and namespace:

```yaml
flags:
  action:
    description: What to do.
    values: [store, retrieve, working-state]
    default: retrieve
  key:
    description: Key for working state operations.
    default: ""
  namespace:
    description: Namespace for working state scoping.
    default: ""
  op:
    description: Working state sub-operation.
    values: [put, get, delete, list]
    default: get
  # query, limit, sort remain unchanged
```

### 7. Add `handleWorkingState` to `internal/pipes/memory/memory.go`

New function dispatching on `flags["op"]`:

```go
func handleWorkingState(s *store.Store, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
    out := envelope.New("memory", "working-state")
    out.Args = flags

    namespace := flags["namespace"]
    key := flags["key"]
    op := flags["op"]
    if op == "" {
        op = "get"
    }

    switch op {
    case "put":
        // require namespace and key
        // content from input envelope via ContentToText
        // call s.PutState(namespace, key, content)
        // return confirmation text

    case "get":
        // require namespace and key
        // call s.GetState(namespace, key)
        // if not found, return empty text with no error
        // if found, return content as text

    case "delete":
        // require namespace and key
        // call s.DeleteState(namespace, key)
        // return confirmation text

    case "list":
        // require namespace
        // call s.ListState(namespace)
        // return list of StateEntry
    }
}
```

Add the routing in `NewHandler`:

```go
case "working-state":
    return handleWorkingState(s, input, flags, logger)
```

### 8. Write store tests

In `internal/store/store_test.go` (create if needed):

- `TestPutState_Insert` — put a new key, verify get returns it.
- `TestPutState_Update` — put same key twice, verify get returns the latest content and `updated_at` changed.
- `TestGetState_NotFound` — get a nonexistent key, verify `(_, false, nil)`.
- `TestDeleteState` — put then delete, verify get returns not found.
- `TestDeleteState_NotFound` — delete nonexistent key, no error.
- `TestListState` — put multiple keys, list returns all in updated_at DESC order.
- `TestListState_Empty` — list on empty namespace returns empty slice.
- `TestNamespaceIsolation` — put same key in two namespaces, verify they don't interfere.

### 9. Write memory pipe tests

In `internal/pipes/memory/memory_test.go` (create or extend):

- `TestHandleWorkingState_Put` — action=working-state, op=put, namespace and key set, content in envelope. Verify stored.
- `TestHandleWorkingState_Get` — action=working-state, op=get, verify content returned.
- `TestHandleWorkingState_GetNotFound` — verify empty text, no error.
- `TestHandleWorkingState_Delete` — verify deletion, subsequent get returns not found.
- `TestHandleWorkingState_PutNoKey` — missing key flag returns fatal error.
- `TestHandleWorkingState_PutNoNamespace` — missing namespace flag returns fatal error.
- `TestHandleWorkingState_PutNoContent` — empty content returns fatal error.
- `TestHandleWorkingState_List` — verify list returns entries.

## Testing Strategy

### Unit Tests

- Store layer: CRUD operations, namespace isolation, upsert behavior
- Pipe layer: flag routing, error handling, content extraction

### Edge Cases

- Very large content (full spec document, potentially 10KB+) — SQLite TEXT handles this fine
- Empty content on put — should be rejected (fatal error)
- Key with special characters — SQLite parameterized queries handle this
- Concurrent puts to the same key — SQLite serializes writes, last write wins

## Risk Assessment

- **No risk to existing behavior.** The `store` and `retrieve` actions are untouched. The new `working-state` action is entirely additive. The migration adds a table — no existing tables are modified.
- **Schema migration is idempotent.** `CREATE TABLE IF NOT EXISTS` means it's safe to run on existing databases.

## Validation Commands

```bash
go test ./internal/store/... -v -count=1
go test ./internal/pipes/memory/... -v -count=1
go build ./internal/pipes/memory/cmd/
```
