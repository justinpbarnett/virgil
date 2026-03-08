

I need to understand the existing pipe infrastructure before speccing a research pipe. Let me examine the codebase.

```md
type: `feat`
task_id: `research-pipe`
prompt: Add a research pipe that retrieves, synthesizes, and caches external information for use in Virgil's processing pipeline.
```

---

## Feature Description

A research pipe that accepts a query, searches for relevant information across configured sources, synthesizes findings into a structured result, and caches responses to avoid redundant lookups. It operates as a standard pipe — one envelope in, one envelope out — composable with any other pipe in the system.

The research pipe is the system's interface to external knowledge. When the router determines that a query requires information Virgil doesn't have in memory or local store, the research pipe fetches it.

## User Story

As a **Virgil user**, I want to ask questions that require external research so that I get synthesized answers without leaving the TUI.

As a **pipeline author**, I want a composable research pipe so that I can include information retrieval in any workflow.

## Relevant Files

### Existing Files — Modify

| File | Why |
|---|---|
| `cmd/virgil/main.go` | Register the research pipe alongside the existing study pipe in the execution pipeline setup |
| `internal/pipe/` | Understand the pipe interface contract — research pipe must conform to it |
| `internal/pipehost/` | Register research pipe in the host so it's discoverable by the router and planner |
| `internal/router/` | Add routing rules so research-intent signals dispatch to the research pipe |
| `internal/planner/` | Planner needs to know when to include research steps in execution plans |
| `internal/store/` | Research cache reads/writes go through the store interface |

### New Files — Create

| File | Purpose |
|---|---|
| `internal/pipe/research/research.go` | Pipe handler: orchestrates search → filter → synthesize flow |
| `internal/pipe/research/research_test.go` | Unit tests |
| `internal/pipe/research/source.go` | Source interface and built-in source implementations |
| `internal/pipe/research/source_test.go` | Source adapter tests |
| `internal/pipe/research/cache.go` | Cache layer — TTL-based, keyed by normalized query |
| `internal/pipe/research/cache_test.go` | Cache tests |
| `internal/pipe/research/synthesizer.go` | Takes raw source results, produces a single coherent answer |
| `internal/pipe/research/synthesizer_test.go` | Synthesizer tests |
| `internal/pipe/research/pipe.toml` | [?] Pipe definition file — depends on existing pipe definition schema |

## Implementation Plan

### Phase 1 — Foundation

Define the research pipe's types, interfaces, and cache layer. No external calls yet. Everything is testable with stubs.

### Phase 2 — Core

Implement source adapters (starting with one concrete source), the synthesizer, and the main pipe handler that wires them together.

### Phase 3 — Integration

Register in pipehost, wire routing rules, connect to store for persistent cache, verify end-to-end through TUI.

## Step by Step Tasks

### Phase 1 — Foundation

**1. Define core types**
File: `internal/pipe/research/research.go`

```go
// Query is the research pipe's input, extracted from the envelope.
type Query struct {
    Text       string
    MaxSources int
    MaxAge     time.Duration // accept cached results up to this old
}

// Finding represents one piece of retrieved information.
type Finding struct {
    Source    string
    Content  string
    URL      string
    Retrieved time.Time
    Confidence float64 // 0.0–1.0, source-determined
}

// Result is the research pipe's output, written back to the envelope.
type Result struct {
    Query     string
    Synthesis string
    Findings  []Finding
    Cached    bool
    Duration  time.Duration
}
```

Conform to the existing pipe handler contract. [?] Need to verify the exact handler signature — likely `func(ctx context.Context, env *envelope.Envelope) (*envelope.Envelope, error)` or similar.

**2. Define the Source interface**
File: `internal/pipe/research/source.go`

```go
// Source retrieves findings for a query from a single external provider.
type Source interface {
    Name() string
    Search(ctx context.Context, query string, limit int) ([]Finding, error)
}
```

Implement a `StubSource` in test files for Phase 1 testing.

**3. Implement the cache layer**
File: `internal/pipe/research/cache.go`

- Key: normalized query string (lowercase, trimmed, collapsed whitespace)
- Value: `Result` with TTL
- Backend: in-memory `sync.Map` with lazy expiration, backed by store for persistence
- Methods:
  - `Get(query string, maxAge time.Duration) (*Result, bool)`
  - `Put(query string, result *Result)`
  - `Invalidate(query string)`
  - `Clear()`

**4. Write cache tests**
File: `internal/pipe/research/cache_test.go`

- Cache miss returns `nil, false`
- Cache hit within TTL returns result
- Cache hit beyond TTL returns `nil, false`
- `Invalidate` removes entry
- `Clear` empties all entries
- Concurrent read/write safety

### Phase 2 — Core

**5. Implement the synthesizer**
File: `internal/pipe/research/synthesizer.go`

```go
type Synthesizer struct {
    // Uses the bridge package for AI synthesis when multiple findings need merging.
    // Falls back to returning the highest-confidence finding verbatim when:
    //   - only one finding exists
    //   - all findings come from the same source
}

func (s *Synthesizer) Synthesize(ctx context.Context, query string, findings []Finding) (string, error)
```

Deterministic-first pattern: if one finding with confidence ≥ 0.9, return it directly. Multiple findings or ambiguity → use bridge for AI synthesis. Log when AI is invoked so the pattern can be analyzed.

**6. Implement first concrete source adapter**
File: `internal/pipe/research/source.go`

[?] Which source first? Options:
- **Web search API** (Brave Search, SearXNG, Tavily) — broadest utility
- **Local file search** — searches the store/memory for relevant past interactions
- **Documentation scraper** — fetches and parses a URL

Recommendation: Start with a **local memory source** (searches the store) and a **web source interface** with one concrete implementation. Local-first matches the project philosophy.

```go
// MemorySource searches the local store for relevant past results.
type MemorySource struct {
    store store.Store
}

// WebSource searches an external API.
type WebSource struct {
    client  *http.Client
    baseURL string
    apiKey  string
}
```

**7. Implement the pipe handler**
File: `internal/pipe/research/research.go`

```go
func New(sources []Source, cache *Cache, synth *Synthesizer) *Pipe

// Handle is the pipe contract method.
func (p *Pipe) Handle(ctx context.Context, env *envelope.Envelope) (*envelope.Envelope, error) {
    // 1. Extract Query from envelope
    // 2. Check cache → return early if hit
    // 3. Fan out to sources (parallel, context-bounded)
    // 4. Collect findings, sort by confidence
    // 5. Synthesize
    // 6. Cache result
    // 7. Write Result to envelope, return
}
```

Fan-out uses `errgroup` with the envelope's context. Individual source failures are logged but non-fatal — the pipe succeeds if at least one source returns results. All sources failing is an error.

**8. Write pipe handler tests**
File: `internal/pipe/research/research_test.go`

- Cache hit skips sources entirely
- Single source, single finding — no synthesizer call
- Multiple sources — findings merged, synthesizer called
- Source timeout — other sources still return
- All sources fail — returns error
- Result is cached after successful research
- Envelope in/out contract is maintained

### Phase 3 — Integration

**9. Register the research pipe in pipehost**
File: modify `internal/pipehost/` (exact file TBD after reading pipehost structure)

Register `research` as a known pipe. Inject dependencies: store (for MemorySource + cache persistence), bridge (for synthesizer), configured web source credentials.

**10. Add routing rules**
File: modify `internal/router/`

Research intent signals:
- Explicit: "research", "look up", "find out", "what is", "search for"
- Contextual: queries about facts, current events, or topics not in memory

The router should check memory first. Research pipe fires when memory has insufficient results. [?] Does the router currently support "try X, fall back to Y" patterns, or does this need to be a pipeline (memory pipe → conditional → research pipe)?

**11. Create pipe definition**
File: `internal/pipe/research/pipe.toml` [?]

```toml
[pipe]
name = "research"
description = "Retrieves and synthesizes information from configured sources"
input = "query"
output = "research-result"

[config]
max_sources = 3
cache_ttl = "1h"
source_timeout = "10s"
```

Schema depends on existing pipe definition format — verify against existing pipes (study pipe).

**12. Wire into main.go**
File: `cmd/virgil/main.go`

Add research pipe to the pipe initialization block alongside the study pipe. Pass config, store, and bridge dependencies.

## Testing Strategy

### Unit Tests

| Test | Verifies |
|---|---|
| Cache get/put/expire/invalidate | TTL logic, concurrent safety |
| Source interface with stubs | Contract compliance |
| Synthesizer with single finding | Deterministic passthrough |
| Synthesizer with multiple findings | AI bridge invocation |
| Handler cache hit path | Sources not called |
| Handler all-sources-fail path | Error propagation |
| Handler partial-source-fail path | Graceful degradation |
| Query normalization | Consistent cache keys |

### Integration Tests

- End-to-end: envelope in → research pipe → envelope out with MemorySource backed by test store
- Pipeline composition: research pipe chained with another pipe

### Edge Cases

- Empty query → clear error, not a source call
- Query that matches cache key but different MaxAge → respects caller's freshness requirement
- Source returns zero findings → not an error, but synthesizer gets nothing → result says "no findings"
- Context cancellation mid-research → clean shutdown, partial results not cached

## Risk Assessment

| Risk | Impact | Mitigation |
|---|---|---|
| External API rate limits / costs | Web source becomes unavailable or expensive | Cache aggressively, local source first, configurable TTL |
| Slow sources block the pipe | TUI feels unresponsive | Per-source timeout via context, progressive results if pipeline supports it |
| Cache grows unbounded | Memory pressure | LRU eviction or max-size cap on in-memory cache, persist to store |
| AI synthesizer hallucination | Incorrect research results | Include raw findings in Result so the user or downstream pipe can verify |
| Envelope contract mismatch | Pipe won't compose | [?] Need to read existing pipe/envelope code to confirm exact contract |

## Validation Commands

```bash
# Unit tests
go test ./internal/pipe/research/... -v

# Race detector (concurrent cache access)
go test ./internal/pipe/research/... -race

# Build verification
go build ./cmd/virgil/...

# Vet
go vet ./...

# Full test suite (if make/just target exists)
just check  # or make check
```

## Open Questions

| # | Question | Why It Matters | Recommended Answer |
|---|---|---|---|
| 1 | What is the exact pipe handler interface signature? | Research pipe must conform to it | Read `internal/pipe/` before implementation — do not guess |
| 2 | How does the existing envelope carry typed data? | Determines how Query/Result are serialized into the envelope | Likely a `map[string]any` or typed field — follow the study pipe's pattern |
| 3 | Which web search API to integrate first? | Affects dependencies, API key management, cost | Brave Search or SearXNG (self-hosted) — both have simple REST APIs. SearXNG if zero-cost is priority. |
| 4 | Does the router support conditional fallback (try memory → research)? | Determines whether this is a single pipe or a mini-pipeline | If not, build as a pipeline: memory check → conditional branch → research. The pipe itself shouldn't own routing logic. |
| 5 | Does `pipe.toml` exist as a convention? | Affects how the pipe is defined and discovered | Follow whatever the study pipe uses. If no definition file exists, skip this and register programmatically. |
| 6 | Should research results feed back into memory/store permanently? | Affects whether Virgil "learns" from research | Yes — store research results so the same question is answered from memory next time. This is the "deterministic layers learn from AI misses" pattern from the README. |