# Feature: Relational Memory Graph

## Metadata

type: `feat`
task_id: `relational-memory-graph`
prompt: `Implement relational memory graphs alongside existing FTS5 search. Set up graph data model and automatic edge creation. Prepare schema for future embeddings but do not implement embedding yet.`

## Feature Description

The current memory infrastructure retrieves context via three independent channels — topic history (FTS5/BM25), working state (keyed lookup), and user preferences (FTS5). These channels operate in isolation: a BM25 search for "OAuth spec" finds memories containing those keywords, but cannot surface the session token preference that informed the spec or the earlier version it was refined from.

A relational layer adds typed edges between memory entries. After BM25 finds anchor points, the graph is traversed one hop outward to surface structurally connected memories that share no keywords with the query. This gives the runtime two composable retrieval primitives now — keyword (FTS5) and relational (graph walk) — with a third (semantic/embeddings) designed for but not implemented.

This spec also unifies the three existing tables (`entries`, `working_state`, `invocations`) into a single `memories` table. The project is early with minimal data, making this the cheapest migration window. Every future feature — summarization, embeddings, decay — benefits from a uniform memory model with a single FTS5 index and simple foreign keys for edges.

## User Story

As the Virgil runtime
I want to track structural relationships between memory entries
So that context retrieval surfaces connected knowledge beyond keyword matches

## Relevant Files

- `internal/store/store.go` — current SQLite schema (entries, working_state, invocations tables), FTS5 setup, all store methods
- `internal/store/store_test.go` — store tests using `tempDB(t)` helper
- `internal/runtime/memory.go` — MemoryInjector/MemorySaver interfaces and Store-backed implementations
- `internal/runtime/memory_test.go` — tests for injection and auto-save with stub implementations
- `internal/runtime/runtime.go` — Execute/ExecuteStream with injectMemory/autoSave hooks
- `internal/config/config.go` — MemoryConfig, MemoryContextEntry, DefaultMemoryConfig()
- `internal/config/config_test.go` — config parsing tests
- `internal/envelope/envelope.go` — Envelope struct with Memory []MemoryEntry, ContentToText helper
- `cmd/virgil/main.go` — WithMemory wiring in runServer()
- `internal/pipes/chat/pipe.yaml` — memory declarations (working_state + topic_history)
- `internal/pipes/draft/pipe.yaml` — memory declarations (user_preferences + topic_history)
- `internal/pipes/educate/pipe.yaml` — memory declarations (topic_history + user_preferences)

### New Files

- `internal/store/graph.go` — memory_edges table creation, edge CRUD, graph traversal queries
- `internal/store/graph_test.go` — tests for edge creation, traversal, deduplication, strength increment

## Implementation Plan

### Phase 1: Schema + ID Foundation

Unify the three existing tables into a single `memories` table with only the columns the current system uses, plus a nullable `embedding` column for future RAG. Add a `memory_edges` table. Migrate existing data. Rewrite store methods against the new schema. Add `ID` field to `MemoryEntry` so the runtime can track which memories were loaded as context — this is the prerequisite for all edge creation.

### Phase 2: Edge Operations

Add edge CRUD and one-hop traversal to the store. Expand the `MemorySaver` interface to accept context IDs. Wire automatic edge creation into the auto-save flow in `memory.go`. Three edge types: `co_occurred`, `produced_by`, `refined_from`.

### Phase 3: Relational Retrieval

Add a `relational` context type to `RetrieveContext` that uses BM25 anchor IDs to walk the graph. Merge is simple: BM25 results first, then graph-walked results not already in the set (sorted by strength), budget-trimmed. Update config and pipe.yaml declarations.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add ID field to MemoryEntry

In `internal/envelope/envelope.go`, add an `ID` field to `MemoryEntry`:

```go
type MemoryEntry struct {
    ID      string `json:"id,omitempty"`
    Type    string `json:"type"`
    Content string `json:"content"`
}
```

This is the foundation — without IDs flowing through injection → execution → auto-save, no edge creation is possible.

### 2. Define the unified memories table schema

In `internal/store/store.go`, replace the `entries`, `working_state`, and `invocations` table creation in `migrate()` with a single `memories` table. Only include columns the current system uses:

```sql
CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    kind TEXT NOT NULL,
    source_pipe TEXT,
    signal TEXT,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    embedding BLOB
);

CREATE INDEX IF NOT EXISTS idx_memories_kind ON memories(kind);
CREATE INDEX IF NOT EXISTS idx_memories_source_pipe ON memories(source_pipe);
CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at);
```

Column mapping from existing tables:
- `kind` values: `working_state` (from working_state table), `explicit` (from entries table), `invocation` (from invocations table)
- `id`: UUID for explicit/invocation, `namespace/key` for working_state
- `source_pipe`: from invocations.pipe (null for other kinds)
- `signal`: from invocations.signal (null for other kinds)
- `content`: from entries.content, working_state.content, or invocations.output
- `tags`: from entries.tags (empty string for other kinds)
- `embedding`: nullable BLOB, never written in this spec — reserved for future RAG

### 3. Define the FTS5 virtual table for memories

Replace `entries_fts` and `invocations_fts` with a single FTS5 table:

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    signal,
    content='memories',
    content_rowid='rowid',
    tokenize='porter unicode61'
);
```

Add triggers for insert, delete, and update to keep the FTS index in sync (same pattern as existing triggers). The porter tokenizer gives stemming for free — "building", "built", "builds" all match "build".

Note: The `memories` table uses TEXT primary key (`id`), so FTS `content_rowid` maps to the implicit `rowid`. All FTS joins use `memories.rowid = memories_fts.rowid`.

### 4. Define the memory_edges table

Create `internal/store/graph.go`. Define the edge table creation SQL (called from the store's `migrate` function):

```sql
CREATE TABLE IF NOT EXISTS memory_edges (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation TEXT NOT NULL,
    strength REAL NOT NULL DEFAULT 1.0,
    created_at INTEGER NOT NULL,
    context TEXT,
    UNIQUE(source_id, target_id, relation)
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON memory_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_edges_target ON memory_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_edges_relation ON memory_edges(relation);
```

Three `relation` values for this spec: `co_occurred`, `produced_by`, `refined_from`. The column is TEXT — adding types later costs nothing.

`ON DELETE CASCADE` ensures edges are cleaned up when a memory is deleted.

### 5. Define the Memory and Edge structs

In `internal/store/store.go`, add a `Memory` struct:

```go
type Memory struct {
    ID         string
    CreatedAt  time.Time
    UpdatedAt  time.Time
    Kind       string
    SourcePipe string
    Signal     string
    Content    string
    Tags       []string
}
```

In `internal/store/graph.go`, define an `Edge` struct:

```go
type Edge struct {
    ID        string
    SourceID  string
    TargetID  string
    Relation  string
    Strength  float64
    CreatedAt time.Time
    Context   string
}
```

Keep the existing `Entry`, `StateEntry`, and `InvocationEntry` types as-is. Store methods continue returning these types to avoid cascading changes across the codebase — they map from `Memory` internally.

### 6. Implement schema migration

In the store's `Open` function, detect whether the old schema exists (check for `entries` table via `sqlite_master`). If old tables exist:

1. Create the new `memories` table, `memories_fts`, and `memory_edges`
2. Migrate `entries` rows → `memories` with `kind='explicit'`, generate UUID for id
3. Migrate `invocations` rows → `memories` with `kind='invocation'`, generate UUID for id, map `pipe`→`source_pipe`, `signal`→`signal`, `output`→`content`
4. Migrate `working_state` rows → `memories` with `kind='working_state'`, id=`namespace/key`
5. Rebuild the FTS index
6. Drop old tables, FTS tables, and triggers

If old tables don't exist (fresh database), create the new schema directly.

Wrap the entire migration in a transaction.

### 7. Rewrite store methods against unified schema

Update every store method to operate on the `memories` table:

- `Save(content, tags)` → insert with `kind='explicit'`, generate UUID
- `Search(query, limit, sort)` → FTS5 query against `memories_fts`, map results to `[]Entry`
- `PutState(namespace, key, content)` → upsert with `kind='working_state'`, id=`namespace/key`. Before upserting, check if the key already exists and return the previous memory ID (needed for `refined_from` edges later — store it as a return value or capture it internally)
- `GetState(namespace, key)` → lookup by id=`namespace/key` where `kind='working_state'`
- `DeleteState(namespace, key)` → delete by id
- `ListState(namespace)` → query where `kind='working_state'` and id starts with `namespace/`
- `SaveInvocation(pipe, signal, output)` → insert with `kind='invocation'`, generate UUID, `source_pipe=pipe`. Return the new memory ID as `(string, error)`.
- `SearchInvocations(query, pipe, limit, since)` → FTS5 query filtered by `kind='invocation'`
- `RetrieveContext(query, requests, budget)` → update to populate `MemoryEntry.ID` on each returned entry. For `topic_history`, set ID from the invocation memory's id. For `working_state`, set ID from the `namespace/key` composite.

### 8. Implement edge CRUD in graph.go

In `internal/store/graph.go`, add methods to `Store`:

- `CreateEdge(edge Edge) error` — INSERT with `ON CONFLICT(source_id, target_id, relation) DO UPDATE SET strength = strength + 1`. Handles deduplication and strength increment for `co_occurred` edges.
- `CreateEdges(edges []Edge) error` — batch insert in a single transaction. Used for `co_occurred` pairs where O(n²) individual inserts would be slow.
- `TraverseFrom(anchorIDs []string, relations []string, limit int) ([]Memory, error)` — one-hop graph traversal from anchor memory IDs. Queries both directions (source→target and target→source). Returns connected memories (not the anchors), deduplicated, sorted by edge strength descending.

### 9. Expand MemorySaver interface and wire edge creation

In `internal/runtime/memory.go`:

Expand the `MemorySaver` interface:

```go
type MemorySaver interface {
    SaveInvocation(pipe, signal, output string, contextIDs []string) error
}
```

Update `StoreMemorySaver` implementation to:

1. Call `store.SaveInvocation(pipe, signal, output)` → get the new memory ID
2. Create `produced_by` edges from the output memory to each context memory ID
3. Create `co_occurred` edges between all context memory ID pairs (canonical ordering — lexicographic sort, smaller ID is always source, halves storage)
4. Batch all edge inserts in one `CreateEdges` call

For `refined_from`: when `PutState` detects an existing key, capture the previous memory ID. Thread this through so the saver can create a `refined_from` edge from new → old. The simplest approach: `PutState` returns the previous ID (if any) as `(string, error)`, and the caller creates the edge. Alternatively, the store creates the edge internally during `PutState` if it detects a prior value — keeping the edge creation close to the data. **Use the internal approach**: `PutState` calls `CreateEdge` for `refined_from` when it overwrites an existing key. This keeps the edge creation colocated with the mutation that triggers it.

Update `runtime.go`'s `autoSave` to collect context IDs from the envelope's `Memory` field and pass them to the saver.

### 10. Add relational retrieval to RetrieveContext

Extend `ContextRequest`:

```go
type ContextRequest struct {
    Type      string   // "topic_history", "working_state", "user_preferences", "relational"
    Depth     string
    Relations []string // for relational: which edge types to traverse
}
```

When processing a `relational` request in `RetrieveContext`:

1. Collect memory IDs from all previously retrieved results (BM25 anchors)
2. Call `TraverseFrom(anchorIDs, relations, limit)`
3. Deduplicate against already-retrieved memories by ID
4. Format results and add to output within budget

`relational` requests must be processed last since they depend on anchor IDs from prior retrievals. If no anchors were found (empty BM25 results), relational retrieval returns nothing.

### 11. Update config for relational context

In `internal/config/config.go`, extend `MemoryContextEntry`:

```go
type MemoryContextEntry struct {
    Type      string   `yaml:"type"`
    Depth     string   `yaml:"depth"`
    Relations []string `yaml:"relations"`
}
```

### 12. Update pipe.yaml declarations

Add `relational` context entries to pipes that benefit from graph traversal. Only use the three implemented edge types:

- `chat/pipe.yaml` — add `relational` with `[produced_by, refined_from, co_occurred]`
- `draft/pipe.yaml` — add `relational` with `[produced_by, refined_from, co_occurred]`
- `educate/pipe.yaml` — add `relational` with `[produced_by, refined_from, co_occurred]`
- Do NOT add to `calendar/pipe.yaml` (memory is disabled)
- Do NOT add to `memory/pipe.yaml`, `code/pipe.yaml`, `shell/pipe.yaml` (no memory declarations)

### 13. Write store tests

In `internal/store/store_test.go`:

- Test migration from old schema → new schema (create old tables with test data, re-open store, verify data accessible via existing methods)
- Test `Save` / `Search` against new schema
- Test `PutState` / `GetState` / `DeleteState` / `ListState` against new schema
- Test `SaveInvocation` returns a memory ID
- Test `SearchInvocations` against new schema
- Test `RetrieveContext` populates `MemoryEntry.ID` for all context types
- Test `RetrieveContext` with `relational` type (end-to-end: save memories, create edges, verify graph-walked results appear in context)

### 14. Write graph tests

In `internal/store/graph_test.go`:

- Test `CreateEdge` — basic insert, uniqueness constraint, strength increment on duplicate
- Test `CreateEdges` — batch insert, verify all edges created in one transaction
- Test `TraverseFrom` — one-hop traversal both directions, deduplication, strength ordering
- Test `TraverseFrom` with no edges (empty result, no error)
- Test `TraverseFrom` with anchor that has no connections (empty result)
- Test ON DELETE CASCADE — delete a memory, verify its edges are removed

### 15. Update runtime tests

In `internal/runtime/memory_test.go`:

- Update `MemorySaver` stub to accept `contextIDs` parameter
- Test that auto-save passes context IDs from envelope's Memory field
- Verify `MemoryEntry.ID` is populated during injection

## Testing Strategy

### Unit Tests

- **Schema migration**: Create old-format tables, insert test data, open store (triggers migration), verify all data accessible via existing method signatures
- **Edge CRUD**: Insert edges, verify uniqueness constraint, verify strength increment on re-insert
- **Graph traversal**: Create a small graph (5-10 memories, 10-15 edges), verify one-hop returns correct connected memories sorted by strength
- **Relational retrieval**: End-to-end through `RetrieveContext` with `relational` type — verify graph-walked results appear after BM25 results, deduplicated, within budget
- **refined_from**: PutState on an existing key creates a `refined_from` edge automatically

### Edge Cases

- **Empty graph**: Relational retrieval with no edges returns only BM25 results
- **Self-referencing**: Prevent edges where source_id == target_id
- **Zero budget**: Relational retrieval with budget=0 still applies default (500 tokens)
- **Missing anchors**: If BM25 returns no results, relational retrieval has no anchors and returns empty
- **ON DELETE CASCADE**: Deleting a memory cleans up all its edges
- **co_occurred batch size**: Budget of 15 context memories → 105 pairs. Verify batch insert completes in reasonable time.

## Risk Assessment

**Schema migration is the highest risk.** The unified table replaces three existing tables. If migration has bugs, existing memory data could be lost. Mitigations:
- Wrap migration in a transaction (rollback on failure)
- Test migration with representative data patterns
- The project is early with minimal accumulated data, limiting blast radius

**MemorySaver interface change.** Adding `contextIDs` parameter breaks all implementations (real + test stubs). This is acceptable in an early-stage project — the interface should be correct, not stable.

**Performance of co_occurred edge creation.** O(n²) pairs with n=15 → 105 edges per invocation. Mitigated by batching all inserts in a single transaction. Runs in the async auto-save goroutine so it doesn't block the response.

**FTS5 tokenizer change.** Switching to `porter unicode61` adds stemming. This is strictly better for search quality but is a behavior change. Existing queries should work the same or better.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go build ./...
go vet ./...
go test ./internal/store/... -v
go test ./internal/runtime/... -v
go test ./internal/config/... -v
go test ./... -count=1
```

## Open Questions (Unresolved)

1. **Should strength have a cap or use diminishing returns?** If two memories co-occur in every invocation for months, strength grows without bound. Options: (a) no cap, (b) cap at a fixed value, (c) diminishing returns (log scale). **Recommendation: Start with no cap. Revisit if retrieval ranking becomes distorted. The simple merge (BM25 first, graph second) means strength only affects ordering within the graph-walked set, limiting the impact of unbounded growth.**

## Resolved Decisions

- **co_occurred edge ordering**: Use canonical ordering (lexicographic sort on IDs, smaller is always source). Halves storage, `TraverseFrom` queries both directions.
- **refined_from detection**: Use the working state key as heuristic. `PutState` on an existing key triggers `refined_from` edge creation internally. No coupling to router classification.
- **Retroactive edges for migrated data**: No. Migrate data only. The graph builds organically from new invocations.
- **Edge types**: Start with 3 (`co_occurred`, `produced_by`, `refined_from`). The TEXT column makes adding types later a one-line change.
- **Scoring system**: Skip weighted merge. Simple composition: BM25 results first, graph-walked results second (deduplicated, sorted by strength), budget-trimmed. The abstraction will emerge from real usage patterns.
- **Speculative columns** (`content_type`, `summary_level`, `summarizes`, `tags` on FTS): Omitted. Add with the features that use them. `embedding BLOB` included because it's zero marginal cost during schema migration.

## Sub-Tasks

1. **Schema + ID Foundation** (Steps 1-7, 13) — MemoryEntry.ID, unified table, FTS5, migration, rewritten store methods, store tests
2. **Edge Operations** (Steps 8-9, 14-15) — Edge CRUD, traversal, MemorySaver expansion, auto-edge creation in memory.go, graph + runtime tests
3. **Relational Retrieval** (Steps 10-12) — RetrieveContext extension, config changes, pipe.yaml updates

Sub-task 2 depends on 1. Sub-task 3 depends on 1 and 2.
