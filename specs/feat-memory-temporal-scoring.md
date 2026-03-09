# Feature: Memory Temporal Scoring & Supersession

## Metadata

type: `feat`
task_id: `memory-temporal-scoring`
prompt: `Add temporal validity, supersession via refined_from edges, composite retrieval scoring, and two-hop graph traversal to Virgil's memory system.`

## Feature Description

Virgil's memory system stores facts, working state, and invocation history but treats all memories as equally valid forever. It has no concept of expiration ("I'm on PTO next week" stays relevant after the week ends), no contradiction handling (storing "API key is ABC" then "API key is XYZ" produces two independent memories with no relationship), and no cross-source ranking (`RetrieveContext` concatenates results from each source independently).

The relational memory graph spec introduced `refined_from` edges but they're unused. Graph traversal is limited to one hop.

This feature adds four capabilities:

1. **Temporal validity** — memories can expire, excluded from retrieval after their `valid_until` timestamp
2. **Supersession** — the existing `refined_from` edge type becomes active; saving an explicit memory that overlaps an existing one automatically creates a supersession chain, halving the old memory's confidence
3. **Composite retrieval scoring** — a unified scoring function ranks all retrieved memories across sources before budget selection
4. **Two-hop graph traversal** — `TraverseFrom` expands to depth 2 for richer relational context

Confidence scoring is infrastructure that enables supersession decay and composite scoring — a `confidence REAL` column on the memories table, defaulting by kind (explicit=0.9, working_state=0.7, invocation=0.5), halved when superseded.

## User Story

As a Virgil user
I want my memory system to handle outdated, contradictory, and time-bounded facts gracefully
So that retrieved context is accurate, current, and ranked by actual relevance

## Relevant Files

- `internal/store/store.go` — Memory struct, Save, Search, RetrieveContext, prepared statements
- `internal/store/graph.go` — Edge struct, CreateEdge, CreateEdges, TraverseFrom, relation constants
- `internal/store/store_test.go` — store tests using `tempDB(t)` helper
- `internal/store/graph_test.go` — edge and traversal tests
- `internal/store/migrations/001_initial_schema.sql` — memories table, FTS5, triggers
- `internal/store/migrations/002_memory_edges.sql` — memory_edges table
- `internal/runtime/memory.go` — StoreMemoryInjector.InjectContext, StoreMemorySaver.SaveInvocation
- `internal/pipes/memory/memory.go` — handleStore, handleRetrieve
- `internal/pipes/memory/pipe.yaml` — flags and triggers for memory pipe
- `internal/config/config.go` — MemoryConfig, MemoryContextEntry, DefaultMemoryConfig
- `pkg/envelope/envelope.go` — MemoryEntry struct

### New Files

- `internal/store/migrations/005_memory_confidence_expiry.sql` — adds `confidence` and `valid_until` columns to memories table
- `internal/store/scoring.go` — composite scoring function and types

## Implementation Plan

### Phase 1: Schema & Expiry

Add `confidence REAL` and `valid_until INTEGER` columns to the memories table via goose migration. Update the `Memory` struct and all write paths to populate confidence defaults. Update all read paths (including prepared statements) to filter expired memories. Expose the `expires` flag in the memory pipe.

### Phase 2: Supersession

Activate `refined_from` edges in `Save()`. When saving an explicit memory, FTS-search for similar existing memories. If a strong match is found with different content, create a `refined_from` edge from new → old and halve the old memory's confidence.

### Phase 3: Two-Hop Traversal

Extend `TraverseFrom` to support `maxDepth=2` via variadic parameter. No return type change — caller tracks hop distance via set membership.

### Phase 4: Composite Scoring

Add post-gather scoring to `RetrieveContext`. After collecting candidates from all sources (existing gather logic preserved), score them with a unified function, sort by score, and trim to budget.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create migration 005_memory_confidence_expiry.sql

Create `internal/store/migrations/005_memory_confidence_expiry.sql`:

```sql
-- +goose Up
ALTER TABLE memories ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5;
ALTER TABLE memories ADD COLUMN valid_until INTEGER;

-- Set confidence defaults based on existing kind values
UPDATE memories SET confidence = 0.9 WHERE kind = 'explicit';
UPDATE memories SET confidence = 0.7 WHERE kind = 'working_state';
UPDATE memories SET confidence = 0.5 WHERE kind = 'invocation';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; goose down is best-effort.
```

The `valid_until` column is nullable — null means "never expires." The `confidence` column defaults to 0.5 (invocation-level) for any future rows where kind isn't set explicitly in code.

### 2. Update Memory struct and write paths

In `internal/store/store.go`:

Add `Confidence` and `ValidUntil` fields to the `Memory` struct:

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
	Confidence float64
	ValidUntil *time.Time // nil = never expires
}
```

Update `Save()` to accept `validUntil *time.Time` and set `confidence = 0.9`:

```go
func (s *Store) Save(content string, tags []string, validUntil *time.Time) error
```

This changes the signature. Update the one caller in `internal/pipes/memory/memory.go` (`handleStore`) and any callers in virgil-cloud.

Update `SaveInvocation()` to set `confidence = 0.5`.

Update `PutState()` to set `confidence = 0.7`.

### 3. Update all read paths to filter expired memories

Add `AND (m.valid_until IS NULL OR m.valid_until > ?)` with `time.Now().UnixNano()` to every query that retrieves memories. This includes both ad-hoc queries and prepared statements:

- `searchRankStmt` — add filter, bind timestamp at each call site
- `searchInvStmt` — same
- `searchInvSinceStmt` — same
- `listAllStateStmt` — same
- `Search()` recent-sort query — same
- `SearchInvocations()` dynamic query — same
- `RecentInvocations()` — same

Since `time.Now()` must be evaluated at query time (not prepare time), every prepared statement gains one additional parameter. Update `prepareStatements()` and all call sites accordingly.

`RetrieveContext()` gets filtering transitively through the above methods.

### 4. Update the memory pipe to accept expires flag

In `internal/pipes/memory/pipe.yaml`, add the `expires` flag:

```yaml
flags:
  # ... existing flags ...
  expires:
    description: When this memory expires (e.g., "tomorrow", "in 3 days", "next friday", "2026-03-15").
    default: ""
```

Add expiry-related patterns:

```yaml
  patterns:
    - "remember {topic}"
    - "recall {topic}"
    - "what do I know about {topic}"
    - "forget {topic}"
    - "remember {topic} until {modifier}"
    - "remember {topic} for {modifier}"
```

In `internal/pipes/memory/memory.go`, update `handleStore()`:

1. Read `flags["expires"]`
2. If non-empty, parse it into a `*time.Time` via `parseExpiry()`
3. Pass to `s.Save(content, tags, validUntil)`

Extract `parseExpiry(s string) (*time.Time, error)` in the memory pipe package. Handle:
- Relative: "tomorrow", "in N days", "in N hours", "in N weeks"
- Day names: "next monday", "next friday"
- ISO dates: "2026-03-15"

Return an error for unrecognized formats. This is a personal tool — handle common cases, not natural language.

### 5. Implement supersession detection in Save()

Update `Save()` in `internal/store/store.go`. After inserting the new explicit memory, detect and handle supersession:

1. If the new memory has tags, query for existing explicit memories with overlapping tags (at least one tag in common)
2. Additionally, FTS-search for the new content against existing explicit memories (limit 3, ranked by BM25)
3. For each candidate: if the candidate's content differs from the new content and shares at least one tag or has a high BM25 match, create a `refined_from` edge from new → old
4. Halve the old memory's confidence: `UPDATE memories SET confidence = max(confidence * 0.5, 0.01) WHERE id = ?`

Extract into a private method `detectSupersession(newID, content string, tags []string)`.

The heuristic is **same tags + different content = supersession**. This catches the common case ("remember API key is ABC" → "remember API key is XYZ") without LLM classification. FTS overlap catches cases without tags ("remember my meeting is at 2pm" → "remember my meeting is at 3pm").

Edge cases:
- No tags and no FTS match → skip (no signal to compare)
- Content identical to existing → skip (duplicate, not contradiction)
- At most 3 supersession edges per save (avoid runaway chains)
- Confidence floor of 0.01 (prevent float noise from repeated halving)

Also add a helper used by the scoring step:

```go
// supersededIDs returns the set of memory IDs (from the given list) that are
// targets of a refined_from edge (i.e., they have been superseded).
func (s *Store) supersededIDs(ids []string) (map[string]bool, error)
```

### 6. Extend TraverseFrom to support two-hop traversal

Add a variadic `maxDepth` parameter for backward compatibility (default 1):

```go
func (s *Store) TraverseFrom(anchorIDs []string, relations []string, limit int, maxDepth ...int) ([]Memory, error)
```

No return type change. Implementation:
1. First hop: existing query (unchanged)
2. If `maxDepth >= 2`: collect IDs from first-hop results, exclude original anchors, run the same traversal query again with first-hop IDs as new anchors
3. Append second-hop results after first-hop results (hop-ordered)
4. Deduplicate: a memory found at both distances keeps the closer position
5. Apply `limit` across both hops combined

The caller (`RetrieveContext`) determines hop distance via set membership: memories in the first-hop set get `HopDistance=1`, the rest get `HopDistance=2`. Update `RetrieveContext`'s relational request handling to pass `maxDepth=2`.

### 7. Create scoring.go with composite scoring function

Create `internal/store/scoring.go`:

```go
package store

import (
	"math"
	"time"
)

// ScoredMemory pairs a memory with its composite retrieval score.
type ScoredMemory struct {
	Memory
	Score       float64
	HopDistance int    // 0 = direct match, 1 = 1-hop, 2 = 2-hop
	SourceType  string // context request type that produced this result
}

// Scoring weights. Initial values — tune via real usage.
const (
	WeightRelevance      = 0.4
	WeightRecency        = 0.3
	WeightConfidence     = 0.2
	WeightGraphProximity = 0.1

	RecencyHalfLifeDays = 7.0
)

// Default relevance values by source type. Initial guesses — tune empirically.
const (
	RelevanceTopicHistory   = 1.0 // BM25-matched, overwritten with normalized rank when available
	RelevanceWorkingState   = 0.8
	RelevanceUserPrefs      = 0.7
	RelevanceRecentHistory  = 0.5
	RelevanceRelational     = 0.4
)

// ComputeScore calculates the composite retrieval score.
func (sm *ScoredMemory) ComputeScore(relevance float64) {
	recency := recencyScore(sm.CreatedAt)
	proximity := graphProximityScore(sm.HopDistance)
	sm.Score = (relevance * WeightRelevance) +
		(recency * WeightRecency) +
		(sm.Confidence * WeightConfidence) +
		(proximity * WeightGraphProximity)
}

// recencyScore returns an exponential decay value in [0, 1].
// 1.0 at now, 0.5 at halfLife days ago, approaching 0 for old memories.
func recencyScore(created time.Time) float64 {
	age := time.Since(created).Hours() / 24.0
	if age < 0 {
		age = 0
	}
	return math.Exp(-0.693 * age / RecencyHalfLifeDays) // ln(2) ≈ 0.693
}

// graphProximityScore maps hop distance to a proximity value.
func graphProximityScore(hops int) float64 {
	switch hops {
	case 0:
		return 1.0
	case 1:
		return 0.5
	case 2:
		return 0.25
	default:
		return 0.1
	}
}
```

Relevance is passed into `ComputeScore()` rather than stored as a field — it's source-dependent and only used during scoring.

### 8. Add post-gather composite scoring to RetrieveContext

Keep the existing gather logic in `RetrieveContext`. Add a scoring and re-selection pass after gathering:

1. **Gather phase** (existing logic, modified): Process all `ContextRequest` types as before, but remove per-type budget enforcement during gathering. Still respect per-source result limits (e.g., 5 invocations, 100 state entries). Collect results into a `[]ScoredMemory` candidate pool instead of the final `[]envelope.MemoryEntry` slice. Set `SourceType` and `HopDistance` on each candidate.

2. **Score phase**: Look up `supersededIDs()` for all candidate IDs. For superseded memories, halve their confidence before scoring. Call `ComputeScore(relevance)` on each candidate with the appropriate default relevance for its source type.

3. **Select phase**: Sort candidates by `Score` descending. Iterate, appending to results until budget is exhausted. Deduplicate by memory ID.

4. **Format phase**: Convert selected `ScoredMemory` entries to `[]envelope.MemoryEntry` (same output type as before — no API change).

This preserves the existing structure (each case branch in the switch still gathers the same way) but replaces per-type budget splitting with global score-based selection.

### 9. Write tests

**Temporal validity** — in `internal/store/store_test.go`:

- `TestSave_WithValidUntil` — save a memory with expiry, verify it appears in search before expiry
- `TestSearch_ExcludesExpiredMemories` — save two memories (one expired, one not), search, verify only the non-expired one returns
- `TestRetrieveContext_ExcludesExpiredMemories` — same pattern through RetrieveContext
- `TestListAllState_ExcludesExpiredState` — working state with expiry

**Supersession** — in `internal/store/store_test.go`:

- `TestSave_DetectsSupersession_SameTags` — save "API key is ABC" with tag "api", then "API key is XYZ" with tag "api", verify `refined_from` edge created
- `TestSave_DetectsSupersession_FTSMatch` — save similar content without tags, verify supersession via FTS
- `TestSave_NoSupersession_DifferentTopics` — unrelated memories, no edges
- `TestSave_NoSupersession_IdenticalContent` — same content twice, no supersession
- `TestSave_SupersessionHalvesConfidence` — old memory's confidence halved
- `TestSave_SupersessionLimitsEdges` — save 5 memories with same tag, at most 3 edges per save
- `TestRetrieveContext_SupersededDeprioritized` — new fact ranks above superseded old fact

**Composite scoring** — in `internal/store/scoring_test.go`:

- `TestRecencyScore` — now ≈ 1.0, 7 days ≈ 0.5, 30 days ≈ 0
- `TestGraphProximityScore` — 0-hop=1.0, 1-hop=0.5, 2-hop=0.25
- `TestComputeScore` — weighted sum is correct
- `TestRetrieveContext_CompositeScoring` — end-to-end: memories with different ages and confidence, retrieval order matches composite score

**Two-hop traversal** — in `internal/store/graph_test.go`:

- `TestTraverseFrom_TwoHop` — chain A→B→C, traverse from A with depth=2, C appears
- `TestTraverseFrom_TwoHop_Dedup` — A→B, A→C, B→C, C appears once at closer distance
- `TestTraverseFrom_DefaultDepth` — no maxDepth arg still gives depth=1
- `TestTraverseFrom_TwoHop_Limit` — total limit applies across both hops

## Testing Strategy

### Unit Tests

- **scoring.go**: Pure functions — recencyScore, graphProximityScore, ComputeScore. Test at boundary values (now, half-life, very old).
- **expiry filtering**: Save memories with past and future valid_until, verify search/retrieval excludes expired ones.
- **supersession**: Save overlapping explicit memories, verify refined_from edges and confidence halving.
- **two-hop**: Build a 3-node chain, verify TraverseFrom at depth 1 and 2 return correct nodes.
- **composite scoring end-to-end**: Save memories of varying age, confidence, and graph distance. Verify RetrieveContext returns them in composite-score order, not source-type order.

### Edge Cases

- **Nil validUntil**: Memories without expiry are never filtered (backward compatible)
- **Confidence floor**: Repeated supersession can't push confidence below 0.01
- **Empty tags on supersession**: FTS-only matching still works when no tags are present
- **Two-hop with no second-hop results**: First-hop results still returned normally
- **Budget exhaustion during scoring**: The select phase respects budget even when many high-scored candidates exist

## Risk Assessment

**Save() performance.** Supersession detection adds an FTS search on every explicit memory save. FTS5 is fast, and explicit saves are infrequent compared to invocations. Benchmark after implementation.

**Prepared statement complexity.** Adding the expiry filter parameter to all prepared statements means every query call now requires an additional `time.Now().UnixNano()` argument. Mechanical but touches many call sites.

**Migration on existing databases.** Adding columns with defaults via ALTER TABLE is safe in SQLite. The UPDATE statements to set per-kind confidence run once on existing data. No data loss risk.

**Composite scoring changes retrieval behavior.** The current system gives equal budget share to each source type. The new system selects by score, which may surface different context for the same query. This is the intended improvement, but it's a behavior change. Validate with real queries after deployment.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just build
just lint
just test
```

## Open Questions (Unresolved)

1. **Should the expiry parser handle natural language beyond the simple patterns?** E.g., "end of month", "after the conference", "when the project ships." **Recommendation: No. Stick to parseable patterns (relative days/hours/weeks, day names, ISO dates). Natural language expiry requires date context from the planner, which is a separate feature.**

2. **Should working_state entries support expiry?** The column supports it, but the spec only exposes `expires` for explicit memories via the memory pipe. **Recommendation: Don't add an `expires` parameter to `PutState()` now. Wait for a real use case.**

## Resolved Decisions

- **Confidence floor: 0.01.** Repeated supersession halves confidence each time (0.9 → 0.45 → 0.225 → ...). Floor of 0.01 prevents float noise. Memories below 0.01 are effectively invisible to scoring but not deleted.
- **Deprioritize superseded, don't exclude.** Superseded facts retain value as context ("we used to use Vendor X, then switched to Y"). Halved confidence makes them rank lower, not disappear.
- **TraverseFrom keeps `[]Memory` return type.** No new `TraversalResult` wrapper. Caller tracks hop distance via set membership. No breaking change, no pkg/store alias updates needed.
- **RetrieveContext: post-gather scoring, not full rewrite.** Existing gather logic preserved. Scoring added as a post-processing step. Less code churn, same outcome.
- **Relevance defaults are initial guesses.** The per-source-type relevance constants (working_state=0.8, recent_history=0.5, etc.) are starting values to be tuned empirically. Defined as named constants in scoring.go, not buried in RetrieveContext.

## Sub-Tasks

1. **Schema & Expiry** (Steps 1-4, 9 expiry tests) — migration, struct updates, expiry filtering on all read paths, memory pipe expires flag
2. **Supersession** (Steps 5, 9 supersession tests) — detection in Save(), confidence halving, supersededIDs helper
3. **Two-Hop Traversal** (Step 6, 9 traversal tests) — maxDepth parameter on TraverseFrom
4. **Composite Scoring** (Steps 7-8, 9 scoring tests) — scoring.go, post-gather scoring in RetrieveContext

Sub-tasks 2-3 depend on 1 (need confidence column). Sub-task 4 depends on all prior.
