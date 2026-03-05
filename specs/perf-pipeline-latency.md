# Perf: Pipeline Latency Reduction

## Metadata

type: `perf`
task_id: `pipeline-latency`
prompt: `Reduce end-to-end signal processing latency from 2-8s to sub-500ms for deterministic routes and near-instant perceived response for AI-backed pipes. Targets: eliminate AI classifier, persistent pipe processes, parallel memory+routing, optimistic streaming, SQLite optimization.`

## Performance Issue Description

Every signal sent to Virgil traverses a sequential pipeline: parse → route → plan → inject memory → execute pipe → format. Two stages dominate latency:

1. **Router Layer 4 AI classifier** — when Layers 1-3 miss, the router spawns a `claude` CLI subprocess to classify intent. This adds 2-4s for process spawn + API roundtrip, and the result is "chat" ~80% of the time anyway.

2. **Subprocess pipe execution** — every pipe invocation spawns a fresh Go binary, pays Go runtime init, JSON marshal/unmarshal, and IPC overhead. For pipes that run frequently (chat, calendar, memory), this adds 50-200ms per call.

Secondary contributors: synchronous memory injection blocks pipe execution (50-300ms), SSE streaming doesn't begin until after routing completes, and SQLite queries lack WAL mode and prepared statements.

The combined effect: worst-case latency is 4-8s (Layer 4 miss → chat pipe with AI), typical case is 2-4s (keyword hit → chat pipe), best case is ~150ms (exact match → simple pipe). The goal is conversational-speed response — like talking to a human.

## Baseline Metrics

- **Worst case (Layer 4 miss → chat)**: ~4-8s (classifier spawn: 2-4s + chat pipe spawn: 2-4s)
- **Typical case (Layer 2 hit → chat)**: ~2-4s (routing: <5ms + memory: 50-300ms + chat subprocess: 2-4s)
- **Best case (Layer 1 hit → calendar)**: ~150ms (routing: <1ms + memory: 50ms + subprocess: 100ms)
- **Subprocess spawn overhead**: ~50-200ms per pipe invocation (Go binary start + JSON IPC)
- **Memory injection**: ~50-300ms (SQLite FTS query + working state scan)
- **Time-to-first-token (SSE)**: equal to full routing + memory injection time before any content streams

## Target Metrics

- **Worst case (routing miss → chat)**: <200ms to first token (optimistic streaming)
- **Typical case (keyword hit → chat)**: <100ms to first token
- **Best case (exact match → simple pipe)**: <50ms end-to-end
- **Subprocess overhead**: eliminated for built-in pipes (persistent processes)
- **Memory injection**: <20ms (WAL mode + prepared statements + parallel execution)
- **Time-to-first-token (SSE)**: <100ms for chat-bound signals

## Relevant Files

### Modified Files

- `internal/router/router.go` — Remove Layer 4 AI classifier call, expand deterministic matching with stemming and synonym maps
- `internal/router/classifier.go` — Delete entirely (AI classifier removed)
- `internal/router/classifier_test.go` — Delete entirely
- `internal/pipe/subprocess.go` — Add persistent process mode alongside existing spawn-per-call mode
- `internal/pipe/registry.go` — Support registering persistent process handlers
- `internal/runtime/runtime.go` — Parallel memory injection, restructure Execute/ExecuteStream
- `internal/runtime/memory.go` — Accept context for cancellation, return channel-based results
- `internal/server/api.go` — Optimistic streaming: start chat stream before routing completes
- `internal/store/store.go` — WAL mode, prepared statements, connection pool tuning
- `cmd/virgil/main.go` — Remove classifier construction, start persistent pipe processes, wire new store options

### Reference Files

- `internal/bridge/claude.go` — Provider interface (unchanged, still used by chat pipe)
- `internal/parser/parser.go` — Parser (unchanged, already fast)
- `internal/planner/planner.go` — Planner (unchanged, already fast)
- `internal/pipes/*/pipe.yaml` — Trigger definitions (keywords/exact matches reviewed for expansion)
- `internal/server/server.go` — Server struct and lifecycle

### New Files

- `internal/router/stems.go` — Porter stemmer integration and synonym expansion for Layer 2
- `internal/router/stems_test.go` — Tests for stemming and synonym matching
- `internal/pipe/persistent.go` — Persistent subprocess handler (long-lived process with JSON-line protocol)
- `internal/pipe/persistent_test.go` — Tests for persistent process lifecycle and communication

## Optimization Strategy

Five independent optimizations executed in dependency order:

### Strategy 1: Eliminate AI Classifier, Expand Deterministic Routing

The classifier fires when Layers 1-3 miss and spawns `claude` CLI just to pick a pipe name. Analysis of the current trigger definitions shows 43 exact matches and ~110 keywords across 19 pipes. Signals miss because of:
- Morphological variants: "scheduling" doesn't match "schedule"
- Synonym gaps: "show me my agenda" has no keyword for "agenda"
- Question phrasing: wh-questions skip Layer 3 entirely and fall through to Layer 4

The fix: add stemming to Layer 2 (so "scheduling"→"schedul" matches "schedule"→"schedul"), add a synonym map for common paraphrases, and when all layers miss, default to chat immediately (which is what the classifier returns most of the time). Mine the existing miss log for patterns to promote.

This eliminates the 2-4s classifier latency entirely with zero reduction in routing accuracy for the common case.

### Strategy 2: Persistent Pipe Processes

Currently `SubprocessHandler` in `pipe/subprocess.go:155` spawns a new `exec.Cmd` for every call. Each spawn pays: OS process creation (~10-30ms), Go runtime init (~20-50ms), JSON marshal stdin + unmarshal stdout (~5-10ms).

Switch to long-lived pipe processes started at server boot. Communication uses the same JSON protocol but over a persistent stdin/stdout connection. The process stays alive between calls. A simple request-response framing (newline-delimited JSON) keeps the protocol compatible.

Fallback: if a persistent process crashes, respawn it on next call (degrade to spawn-per-call until restart succeeds).

### Strategy 3: Parallel Memory Injection

Memory injection (`runtime.go:178`) runs synchronously before each pipe step. But it only needs the signal text — it doesn't depend on the route result. For single-step plans (the common case), memory fetch can run concurrently with routing+planning.

For multi-step plans, the first step's memory can still be fetched during routing. Subsequent steps remain sequential (they depend on previous step output).

### Strategy 4: Optimistic Chat Streaming

Most signals route to chat. The SSE path currently waits for routing to complete before streaming any content. For chat-bound signals, this means the user stares at a blank screen for the full routing duration.

The optimization: when the SSE path is active, start a speculative chat stream immediately (before routing completes). If routing resolves to chat (the common case), the stream is already producing tokens. If routing resolves to a different pipe, cancel the speculative stream and execute the correct pipe.

This reduces perceived latency to near-zero for chat — tokens start flowing within ~100ms of the request.

### Strategy 5: SQLite Query Optimization

The store opens without WAL mode, without prepared statements, and without connection pool tuning. Each `RetrieveContext` call builds queries from scratch via `fmt.Sprintf`. Specific issues:

- No `PRAGMA journal_mode=WAL` — readers block writers and vice versa
- No `PRAGMA synchronous=NORMAL` — fsync on every write
- No prepared statements — query parsing overhead on every call
- `ListState` uses `LIKE ?` prefix match without a covering index
- `RetrieveContext` runs standard requests sequentially instead of concurrently
- Default `database/sql` connection pool may be suboptimal for single-writer/multi-reader SQLite

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. SQLite Performance Pragmas and Prepared Statements

This is foundational — every other optimization benefits from faster DB access.

- In `internal/store/store.go` `Open()`, after enabling foreign keys (line 90), add:
  ```go
  db.Exec("PRAGMA journal_mode=WAL")
  db.Exec("PRAGMA synchronous=NORMAL")
  db.Exec("PRAGMA cache_size=-8000")  // 8MB cache
  db.Exec("PRAGMA busy_timeout=5000")
  ```
- Set connection pool: `db.SetMaxOpenConns(1)` for writes (SQLite is single-writer), but allow read connections via `db.SetMaxIdleConns(4)`
- Add a composite index for `ListState` prefix queries:
  ```sql
  CREATE INDEX IF NOT EXISTS idx_memories_kind_id ON memories(kind, id);
  ```
  Add this to `newSchemaDDL` and handle in migration (CREATE INDEX IF NOT EXISTS is idempotent)
- Convert hot-path queries in `RetrieveContext` to prepared statements stored on the `Store` struct:
  - `searchInvocationsStmt` — the FTS join query from `SearchInvocations`
  - `listAllStateStmt` — the working state query from `listAllState`
  - `searchExplicitStmt` — the FTS join query from `Search`
- Close prepared statements in `Store.Close()`

### 2. Eliminate AI Classifier

- Create `internal/router/stems.go`:
  - Implement a minimal Porter stemmer (or use a lightweight Go package like `github.com/kljensen/snowball` — check if acceptable, otherwise implement the ~80-line Porter algorithm directly)
  - Define a synonym map: `var synonyms = map[string]string{ "agenda": "calendar", "meeting": "calendar", "appt": "appointment", "msg": "message", "compose": "write", ... }`
  - Export `func StemAndExpand(word string) []string` — returns the stemmed form plus any synonym expansions
- Modify `internal/router/router.go`:
  - In `NewRouter`, pre-stem all keywords when building `keywordIndex` and `pipeKeywords`. Store both original and stemmed forms. This way "schedule" and "scheduling" both index to the same stem
  - In `Route()` Layer 2, stem each signal word before looking up in `keywordIndex`
  - Remove the `classifier` field from the `Router` struct
  - Remove the `classifier *Classifier` parameter from `NewRouter`
  - In Layer 4 (lines 164-197): remove the `classifier.Classify()` call. Keep the miss log write. Default directly to `{Pipe: "chat", Confidence: 0.0, Layer: LayerFallback}`
  - Review the miss log file (`data/misses.jsonl` if it exists) for recurring patterns. Add any frequently-occurring signals as exact matches or new keywords to the relevant `pipe.yaml` files
- Create `internal/router/stems_test.go`:
  - Test stemming: "scheduling" → matches "schedule" keyword
  - Test synonyms: "agenda" → routes to calendar
  - Test that removing the classifier doesn't break existing Layer 1-3 routing (existing tests should still pass with `nil` classifier removed from signature)
- Delete `internal/router/classifier.go` and `internal/router/classifier_test.go`
- Update `cmd/virgil/main.go`: remove classifier provider construction and classifier creation. `NewRouter` no longer takes a classifier
- Update any tests that construct a `NewRouter` with a classifier argument

### 3. Persistent Pipe Processes

- Create `internal/pipe/persistent.go`:
  - Define `PersistentProcess` struct:
    ```go
    type PersistentProcess struct {
        mu      sync.Mutex
        cmd     *exec.Cmd
        stdin   io.WriteCloser
        scanner *bufio.Scanner
        cfg     SubprocessConfig
        logger  *slog.Logger
    }
    ```
  - `func NewPersistentProcess(cfg SubprocessConfig) *PersistentProcess` — creates but doesn't start
  - `func (p *PersistentProcess) Start() error` — starts the subprocess, sets up stdin writer and stdout scanner
  - `func (p *PersistentProcess) Stop() error` — closes stdin (signals EOF to child), waits for exit
  - `func (p *PersistentProcess) Handler() Handler` — returns a Handler func that:
    1. Acquires mutex (serializes calls to this process)
    2. Writes JSON request + newline to stdin
    3. Reads one JSON line from stdout (the envelope response)
    4. Returns the envelope
    5. If write/read fails, attempts restart once, then returns fatal error
  - `func (p *PersistentProcess) StreamHandler() StreamHandler` — similar but reads chunk lines until envelope line
  - The protocol is identical to `SubprocessRequest`/`SubprocessChunk` — just sent over a persistent connection instead of per-process
- Create `internal/pipe/persistent_test.go`:
  - Test start/stop lifecycle
  - Test handler sends request, receives response
  - Test auto-restart on process crash
  - Test concurrent call serialization
- Modify `internal/pipe/registry.go`:
  - Add `persistentProcesses []*PersistentProcess` field to `Registry`
  - Add `func (r *Registry) RegisterPersistent(name string, def Definition, cfg SubprocessConfig)` — creates a `PersistentProcess`, starts it, registers its handler
  - Add `func (r *Registry) Shutdown()` — stops all persistent processes (called on server shutdown)
- Modify `cmd/virgil/main.go`:
  - In the pipe registration loop, use `RegisterPersistent` instead of `Register` with `SubprocessHandler` for all built-in pipes
  - Call `registry.Shutdown()` in the server shutdown path
- Modify `internal/server/server.go` or `cmd/virgil/main.go` shutdown handler to call `registry.Shutdown()`

### 4. Parallel Memory Injection

- Modify `internal/runtime/memory.go`:
  - Add `func (m *StoreMemoryInjector) InjectContextAsync(env envelope.Envelope, cfg config.MemoryConfig) <-chan envelope.Envelope` — runs `InjectContext` in a goroutine, returns result on channel
- Modify `internal/runtime/runtime.go`:
  - In `Execute()` (line 161): for single-step plans, start memory injection before the step loop begins (can be kicked off as soon as the seed envelope is available)
  - For multi-step plans: first step's memory can be prefetched, but subsequent steps depend on prior output so they remain sequential
  - In `ExecuteStream()` (line 198): same optimization — prefetch memory for the terminal step while earlier steps execute
- Modify `internal/server/api.go`:
  - In `handleSignal()` and `handleSSE()`: kick off memory prefetch concurrently with routing:
    ```go
    // Start memory prefetch in parallel with routing
    memCh := runtime.PrefetchMemory(seed, defaultMemConfig)

    route := s.router.Route(ctx, req.Text, parsed)
    plan := s.planner.Plan(route, parsed)

    // Memory result ready by now (routing took longer than the DB query)
    ```
  - This requires exposing a `PrefetchMemory` function from runtime or having the server orchestrate the parallelism directly
- Add tests verifying that parallel memory injection produces identical results to sequential injection

### 5. Optimistic Chat Streaming

- Modify `internal/server/api.go` `handleSSE()`:
  - After flushing headers (line 82), before routing:
    1. Start a speculative chat stream in a goroutine — build a chat-only plan, begin `ExecuteStream` with the chat pipe
    2. Concurrently, run routing (`s.router.Route(...)`)
    3. When routing completes:
       - If route resolved to "chat": let the speculative stream continue (it's already producing tokens)
       - If route resolved to a different pipe: cancel the speculative stream's context, execute the correct pipe, stream its output
    4. The speculative stream should write to a buffer that's flushed to the client only once routing confirms chat
  - This is the most architecturally complex change. Key constraint: the speculative stream must be cancellable without leaking goroutines or leaving the client in a broken SSE state
  - Implementation approach:
    ```go
    specCtx, specCancel := context.WithCancel(r.Context())
    var specBuf bytes.Buffer
    specDone := make(chan envelope.Envelope, 1)

    go func() {
        chatPlan := runtime.Plan{Steps: []runtime.Step{{Pipe: "chat"}}}
        result := s.runtime.ExecuteStream(specCtx, chatPlan, seed, func(ev runtime.StreamEvent) {
            specBuf.WriteString(ev.Data) // buffer, don't flush yet
        })
        specDone <- result
    }()

    route := s.router.Route(r.Context(), req.Text, parsed)

    if route.Pipe == "chat" {
        // Flush buffered speculative output, then continue streaming
        w.Write(specBuf.Bytes())
        flusher.Flush()
        result := <-specDone
        // ... send done event
    } else {
        specCancel() // cancel speculative chat
        // Execute correct pipe normally
    }
    ```
  - Edge case: if routing is very fast (Layer 1 exact match, <1ms), the speculative stream may not have started yet. That's fine — just execute the correct pipe directly
- Add tests:
  - Test that chat-route signals produce streaming output with low latency
  - Test that non-chat signals cancel speculative stream and execute correct pipe
  - Test that goroutines don't leak on cancellation

## Benchmarking Plan

### Automated Benchmarks

Add `internal/router/router_bench_test.go`:
```go
func BenchmarkRouteExactMatch(b *testing.B)   { /* Layer 1 */ }
func BenchmarkRouteKeyword(b *testing.B)      { /* Layer 2 with stemming */ }
func BenchmarkRouteCategory(b *testing.B)     { /* Layer 3 */ }
func BenchmarkRouteMiss(b *testing.B)         { /* Layer 4 fallback to chat */ }
```

Add `internal/store/store_bench_test.go`:
```go
func BenchmarkRetrieveContext(b *testing.B)   { /* Full context retrieval */ }
func BenchmarkSearchInvocations(b *testing.B) { /* FTS search */ }
func BenchmarkListAllState(b *testing.B)      { /* Working state scan */ }
```

Add `internal/pipe/persistent_bench_test.go`:
```go
func BenchmarkPersistentHandler(b *testing.B)  { /* Persistent process call */ }
func BenchmarkSubprocessHandler(b *testing.B)  { /* Spawn-per-call baseline */ }
```

### Manual End-to-End Timing

After implementation, measure with:
```bash
# Time a signal end-to-end (non-streaming)
time curl -s -X POST http://localhost:7890/signal \
  -H 'Content-Type: application/json' \
  -d '{"text":"check my calendar"}'

# Time a signal with streaming (measure time-to-first-byte)
curl -s -X POST http://localhost:7890/signal \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d '{"text":"hello"}' \
  --trace-time 2>&1 | head -20

# Verify Layer 4 no longer spawns classifier
VIRGIL_LOG_LEVEL=debug virgil "some random unusual phrase" 2>&1 | grep -i classifier
# Should produce no output
```

### Before/After Comparison

Run each benchmark before starting implementation to establish baselines. Record:
- `go test -bench=. -benchmem ./internal/router/`
- `go test -bench=. -benchmem ./internal/store/`
- `go test -bench=. -benchmem ./internal/pipe/`
- End-to-end curl timings for Layer 1, Layer 2, and Layer 4 signals

## Risk Assessment

### Strategy 1 (Remove Classifier)
- **Risk**: Some signals that the AI classifier correctly routed will now fall to chat
- **Mitigation**: Mine miss log before removing classifier. Add those patterns as keywords. The classifier returns "chat" ~80% of the time — the false-positive rate is already high
- **Rollback**: Re-add classifier behind a config flag if routing accuracy drops noticeably

### Strategy 2 (Persistent Processes)
- **Risk**: Long-lived processes can leak memory, accumulate state, or crash silently
- **Mitigation**: Health check on each call (verify process is alive before writing). Auto-restart with backoff. Log restarts as warnings
- **Risk**: Mutex serialization means one slow call blocks subsequent calls to the same pipe
- **Mitigation**: For pipes that need concurrency, spawn a pool of N persistent processes and round-robin

### Strategy 3 (Parallel Memory)
- **Risk**: Race condition if memory injection modifies shared state
- **Mitigation**: `InjectContext` is read-only on the envelope (creates a new copy). No shared mutation
- **Risk**: If routing is faster than memory fetch, we block on memory anyway
- **Mitigation**: That's fine — we're no worse than sequential. The win is when routing takes longer than memory (Layer 2+ signals)

### Strategy 4 (Optimistic Streaming)
- **Risk**: Speculative chat stream produces output that's discarded if routing resolves elsewhere — wasted compute
- **Mitigation**: The speculative stream only runs for the duration of routing (~1-5ms for Layers 1-3). Wasted work is minimal. For Layer 4 (now just defaulting to chat), there's zero waste
- **Risk**: Goroutine leak if cancellation isn't handled properly
- **Mitigation**: Use `context.WithCancel`, ensure `<-specDone` is always drained even after cancel

### Strategy 5 (SQLite Optimization)
- **Risk**: WAL mode changes durability guarantees — a crash could lose the last few transactions
- **Mitigation**: `synchronous=NORMAL` is safe for WAL mode (data is durable after checkpoint). Only `synchronous=OFF` risks data loss. Auto-save (invocations) is best-effort anyway
- **Risk**: Prepared statements tied to a single connection may not work well with connection pooling
- **Mitigation**: `database/sql` manages prepared statements per-connection internally. `SetMaxOpenConns(1)` ensures a single connection for writes

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test    # all unit tests pass
just build   # compiles cleanly
just lint    # no lint errors
```

## Open Questions (Resolved)

1. **Stemmer dependency**: Implement inline (~80 lines of Go). No external dependency.
2. **Persistent process pool size**: One process per pipe. No pooling needed.
3. **Optimistic streaming buffer vs direct flush**: Buffer until routing confirms, then flush. Never send speculative tokens before confirmation.
4. **Miss log promotion workflow**: Manual review during implementation. Automated tooling deferred to a follow-up task.

## Sub-Tasks

The five strategies are independent and can be implemented in any order, though the recommended execution order (above) ensures each step builds on a stable foundation:

1. **SQLite optimization** — foundational, benefits everything
2. **Eliminate classifier** — biggest single latency win
3. **Persistent processes** — reduces per-call overhead
4. **Parallel memory injection** — overlaps I/O with routing
5. **Optimistic streaming** — most complex, biggest perceptual win
