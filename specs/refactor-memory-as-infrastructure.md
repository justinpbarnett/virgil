# Refactor: Elevate Memory from Pipe to Infrastructure

## Metadata

type: `refactor`
task_id: `memory-as-infrastructure`
prompt: `Restructure memory from a pipe-only capability to runtime infrastructure. The runtime automatically saves every invocation and injects relevant memory context before each pipe runs — same pattern as logging (decision #13). The memory pipe survives but shrinks to handle only explicit operations (deliberate store, retrieve, forget). Pipes declare their memory needs in pipe.yaml; the runtime satisfies them. This mirrors the cognitive science model: automatic encoding, context-triggered retrieval, and deliberate recall as the minority case.`

## Refactor Description

Memory is currently a pipe — an atomic capability chained into plans like `memory.retrieve | draft`. But nearly every pipe benefits from memory context (educate needs learning history, draft needs style preferences, chat needs recent conversation), which means memory is infrastructure, not a composable unit. When almost everything needs something, that something belongs in the runtime.

The architecture already acknowledges this in decisions #24-26 (context assembly in the planner, automatic working-state saves), but the implementation treats memory as a pipe that other pipes must be explicitly chained with. There's a gap between the philosophy and the mechanics.

The refactor follows the same split as logging (decision #13): the runtime handles automatic read/write transparently, and a pipe handles explicit operations. Three distinct memory modes, mapped from cognitive science:

1. **Automatic encoding** — the runtime saves signal, plan, and output after every invocation. No pipe opts in. Hierarchical summarization handles the forgetting curve.
2. **Context-triggered retrieval** — the runtime pre-loads relevant memory into the envelope before each pipe runs. Pipes declare what kinds of memory they need in `pipe.yaml`. The runtime satisfies the declaration.
3. **Deliberate recall** — the memory pipe handles explicit "remember X," "what did I say about Y," and "forget Z" signals. This is the minority case.

## Current State

**Runtime** (`internal/runtime/runtime.go`): Executes plans step-by-step. Has no awareness of memory. No pre- or post-invocation hooks beyond logging via the observer pattern.

**Memory pipe** (`internal/pipes/memory/`): Handles all memory operations — store, retrieve, and working-state CRUD. Other pipes get memory by being chained after `memory.retrieve` in templates:
```yaml
# educate pipe template example
- requires: [verb, source, topic]
  plan:
    - pipe: "{source}"    # memory.retrieve
      flags: { action: retrieve, topic: "{topic}", limit: "5" }
    - pipe: "{verb}"      # educate
      flags: { topic: "{topic}" }
```

**Store** (`internal/store/store.go`): SQLite with FTS5. Has `entries` table (append-only, full-text indexed) and `working_state` table (keyed mutable state). Provides `Save`, `Search`, `PutState`, `GetState`, `DeleteState`, `ListState`.

**Envelope** (`internal/envelope/envelope.go`): No field for memory context. Pipes that need context get it from the previous step's output via chaining.

**Config** (`internal/config/config.go`): `PipeConfig` has no memory declaration. The config loader parses `pipe.yaml` into `PipeConfig` with triggers, flags, prompts, vocabulary, templates, and format.

**Planner** (`internal/planner/planner.go`): Builds plans from templates. Currently responsible for inserting `memory.retrieve` steps when templates require a `{source}`. Has no concept of automatic memory injection.

## Target State

**Runtime** gains two infrastructure responsibilities:

1. **Pre-invocation context injection** — Before each pipe runs, the runtime queries the store based on the pipe's memory declaration and injects the results into a new `Memory` field on the envelope. Every pipe receives relevant memory without asking for it.

2. **Post-invocation automatic save** — After every terminal pipe execution (the last step in a plan), the runtime saves the signal, routed pipe, and output to the entries table. No pipe opts in. The hierarchical summarization pipeline (future) handles consolidation.

**Envelope** gains a `Memory` field:
```go
type Envelope struct {
    // ... existing fields ...
    Memory  []MemoryEntry `json:"memory,omitempty"`
}

type MemoryEntry struct {
    Type    string `json:"type"`    // "topic_history", "user_preferences", "working_state"
    Content string `json:"content"`
}
```

**PipeConfig** gains a `memory` section:
```yaml
memory:
  context:
    - type: working_state
    - type: user_preferences
    - type: topic_history
      depth: 7d
  budget: 2000  # max tokens of memory context
```

Pipes that declare nothing get a sensible default (recent working state, small budget). Pipes can declare `memory: none` to opt out entirely (e.g., calendar, which has no use for memory context).

**Memory pipe** shrinks to three explicit operations: deliberate store, deliberate retrieve, deliberate forget. Working-state operations remain (they're explicit by nature). The pipe no longer carries the burden of being the primary way other pipes get context.

**Planner** no longer needs to insert `memory.retrieve` steps. Templates that previously chained through `{source}` for memory access can simplify — the runtime handles context injection before the pipe runs.

## Relevant Files

- `internal/runtime/runtime.go` — Add pre-invocation memory injection and post-invocation save to `runStep` and `Execute`/`ExecuteStream`
- `internal/runtime/runtime_test.go` — Tests for memory injection and auto-save behavior
- `internal/envelope/envelope.go` — Add `Memory` field and `MemoryEntry` type
- `internal/envelope/envelope_test.go` — Validation tests for new field
- `internal/config/config.go` — Add `MemoryConfig` to `PipeConfig`, parse `memory` section from `pipe.yaml`
- `internal/config/config_test.go` — Tests for memory config parsing
- `internal/store/store.go` — Add `SaveInvocation` method for structured auto-save, add `RetrieveContext` method for typed memory retrieval
- `internal/store/store_test.go` — Tests for new store methods
- `internal/pipes/memory/memory.go` — Remove chaining-oriented patterns; keep explicit store/retrieve/forget/working-state
- `internal/pipes/memory/pipe.yaml` — Update triggers and description to reflect explicit-only role
- `internal/pipes/educate/pipe.yaml` — Add memory declaration, simplify templates
- `internal/pipes/draft/pipe.yaml` — Add memory declaration, simplify templates
- `internal/pipes/chat/pipe.yaml` — Add memory declaration

### New Files

- `internal/runtime/memory.go` — Memory injection and auto-save logic, extracted from runtime.go for clarity

## Migration Strategy

This refactor is incremental — each phase is independently testable and deployable.

**Phase 1 (Foundation):** Add the `Memory` field to the envelope and the `MemoryConfig` to `PipeConfig`. No behavioral change — the field exists but nothing populates or reads it. All existing tests pass.

**Phase 2 (Auto-save):** Add post-invocation save to the runtime. This is additive — invocations start being recorded automatically. The memory pipe's `store` action still works for explicit saves. No pipe changes needed.

**Phase 3 (Context injection):** Add pre-invocation memory injection to the runtime. Add memory declarations to pipes that benefit from it. Templates that previously chained `memory.retrieve` can be simplified but don't have to be — both paths work during transition.

**Phase 4 (Cleanup):** Remove `memory.retrieve` chaining from templates that now get context via injection. Simplify the memory pipe's description and triggers. Update architecture docs.

Backward compatibility: existing `pipe.yaml` files without a `memory` section get the default behavior (small working-state context). No pipe breaks. The memory pipe's existing actions all continue to work.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add `MemoryEntry` type and `Memory` field to the envelope

In `internal/envelope/envelope.go`:

- Add `MemoryEntry` struct with `Type` and `Content` string fields
- Add `Memory []MemoryEntry` field to `Envelope` with `json:"memory,omitempty"` tag
- Update `envelope_test.go` validation to accept the new field

### 2. Add `MemoryConfig` to pipe config

In `internal/config/config.go`:

- Define `MemoryContextEntry` struct: `Type string`, `Depth string` (optional duration like "7d")
- Define `MemoryConfig` struct: `Context []MemoryContextEntry`, `Budget int`, `Disabled bool`
- Add `Memory MemoryConfig` field to `PipeConfig` with `yaml:"memory"` tag
- Add `DefaultMemoryConfig()` function returning sensible defaults (working_state context, budget 1000)
- Update `config_test.go` to verify parsing of memory sections

### 3. Add `SaveInvocation` to the store

In `internal/store/store.go`:

- Add `SaveInvocation(pipe string, signal string, output string, tags []string) error` method
- This wraps `Save` but structures the content as `[pipe] signal → output` for better FTS retrieval
- Add `invocations` table in `migrate()` with columns: `id`, `pipe`, `signal`, `output`, `created_at`
- Add FTS5 virtual table `invocations_fts` covering `signal` and `output` columns
- Add `SearchInvocations(query string, pipe string, limit int, since time.Time) ([]InvocationEntry, error)` method
- Test in `store_test.go`

### 4. Add `RetrieveContext` to the store

In `internal/store/store.go`:

- Add `RetrieveContext(query string, types []MemoryContextEntry, budget int) ([]MemoryEntry, error)` — or accept the config type directly
- For `topic_history`: FTS search on invocations with optional `since` from depth
- For `working_state`: get the current working state for the active namespace
- For `user_preferences`: FTS search on entries table for preference-tagged content
- Each type contributes content up to its share of the budget
- Test in `store_test.go`

### 5. Add memory injection to the runtime

In `internal/runtime/memory.go` (new file):

- Define `MemoryInjector` interface: `InjectContext(env envelope.Envelope, cfg MemoryConfig) envelope.Envelope`
- Implement `StoreMemoryInjector` backed by `*store.Store`
- The injector calls `RetrieveContext` using the signal content as the query and the pipe's memory config
- Populates `env.Memory` with the results
- In `runtime.go`, add `injector MemoryInjector` field to `Runtime`
- In `runStep`, call `injector.InjectContext` before executing the handler (if injector is non-nil)
- Nil injector means no injection (backward compatible — existing tests pass without changes)

### 6. Add auto-save to the runtime

In `internal/runtime/memory.go`:

- Define `MemorySaver` interface: `SaveInvocation(pipe string, signal string, output string) error`
- Implement `StoreMemorySaver` backed by `*store.Store`
- In `runtime.go`, add `saver MemorySaver` field to `Runtime`
- In `Execute` and `ExecuteStream`, after the final step completes successfully, call `saver.SaveInvocation` with the seed signal content and the terminal output content
- Nil saver means no auto-save (backward compatible)
- Auto-save failures are logged but do not fail the invocation

### 7. Add memory declarations to pipes

Update `pipe.yaml` for pipes that benefit from memory context:

**educate/pipe.yaml:**
```yaml
memory:
  context:
    - type: topic_history
      depth: 30d
    - type: user_preferences
  budget: 2000
```

**draft/pipe.yaml:**
```yaml
memory:
  context:
    - type: user_preferences
    - type: topic_history
      depth: 7d
  budget: 1500
```

**chat/pipe.yaml:**
```yaml
memory:
  context:
    - type: working_state
    - type: topic_history
      depth: 1d
  budget: 1000
```

**calendar/pipe.yaml:**
```yaml
memory:
  disabled: true
```

### 8. Simplify memory-chaining templates

Update templates in pipe.yaml files that previously required `{source}` to chain `memory.retrieve`:

- In `educate/pipe.yaml`: The `[verb, source, topic]` template that chains `memory.retrieve | educate` can be removed — educate gets topic history from the runtime's injection. Keep the simpler `[verb, topic]` and `[verb]` templates.
- In `draft/pipe.yaml`: The `[verb, type, source]` and `[verb, source, modifier]` templates still make sense when the source is a non-memory pipe (e.g., `calendar`). Only remove templates where `{source}` always resolves to `memory`.

### 9. Update the memory pipe

In `internal/pipes/memory/pipe.yaml`:

- Update description to reflect its narrowed role: explicit store, retrieve, and forget operations
- Add a `forget` trigger keyword
- Keep working-state operations unchanged (they're explicit by nature)

In `internal/pipes/memory/memory.go`:

- No functional changes needed — store/retrieve/working-state handlers remain as-is
- The pipe still works for explicit "remember X" and "what do I know about Y" signals

### 10. Wire memory infrastructure into server startup

In `cmd/virgil/main.go` (or wherever the runtime is constructed):

- Create the `StoreMemoryInjector` and `StoreMemorySaver` backed by the existing `*store.Store`
- Pass them to the runtime constructor
- This is the only wiring change — everything else is structural

### 11. Write integration tests

In `internal/runtime/memory_test.go`:

- Test that a pipe with a memory declaration receives populated `env.Memory`
- Test that a pipe with `memory: disabled` receives empty `env.Memory`
- Test that a pipe with no memory declaration gets the default context
- Test that auto-save records the invocation after successful execution
- Test that auto-save does not record on fatal error
- Test that auto-save failure does not propagate to the invocation result

## Testing Strategy

**how to verify behavior is unchanged — existing tests that must pass, new tests for coverage gaps:**

All existing tests must pass without modification after each phase. The refactor is additive — existing behavior is preserved through nil-checks on the injector and saver interfaces.

New tests:
- Envelope: validation accepts `Memory` field; `MemoryEntry` serializes correctly
- Config: `MemoryConfig` parses from YAML; defaults applied when absent; `disabled: true` works
- Store: `SaveInvocation` writes to invocations table; `SearchInvocations` returns correct results with pipe/time filters; `RetrieveContext` assembles entries within budget
- Runtime: injection populates `env.Memory`; auto-save fires after terminal step; nil injector/saver are no-ops

## Risk Assessment

- **No existing behavior breaks.** The injector and saver are nil by default. Existing runtime constructors (`New`, `NewWithLevel`, `NewWithFormats`) continue to work. Pipes that don't declare memory get defaults that include minimal context.
- **Envelope size growth.** The `Memory` field adds data to every envelope. Budget caps prevent unbounded growth, but pipes with large budgets could slow down JSON serialization in the subprocess protocol. Monitor envelope sizes after deployment.
- **Store write volume.** Auto-saving every invocation increases SQLite write load. For a personal tool with low QPS this is negligible, but the invocations table should have a retention/pruning strategy from the start (e.g., raw invocations older than 30 days are deleted by the summarization pipeline).
- **Memory retrieval latency.** Pre-invocation FTS queries add latency before every pipe execution. For pipes with `memory: disabled`, this is zero. For others, FTS queries on SQLite are typically sub-millisecond for small datasets. Worth monitoring as the invocations table grows.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
go test ./internal/envelope/... -v -count=1
go test ./internal/config/... -v -count=1
go test ./internal/store/... -v -count=1
go test ./internal/runtime/... -v -count=1
go test ./internal/pipes/memory/... -v -count=1
go build ./cmd/virgil
```

## Open Questions (Unresolved)

**1. Encoding strength / salience.** Should all invocations be saved with equal weight, or should some signal types get stronger encoding (explicit instructions → long-term facts, calendar lookups → minimal encoding)? The conversation suggested this as a refinement. **Recommendation:** Start with uniform encoding. Add a `salience` field to `SaveInvocation` in a follow-up once patterns emerge from real usage data.

**2. Memory context in subprocess protocol.** The `Memory` field will be serialized into the JSON envelope passed to subprocess pipes. Subprocess pipes in other languages will need to handle this field. **Recommendation:** Since `json:"memory,omitempty"` means it's absent when empty, existing subprocess pipes that don't read it are unaffected. Document the field in the subprocess protocol spec.

**3. Token budget accounting.** The budget is declared in "tokens" but the store deals in characters/bytes. How to approximate? **Recommendation:** Use a simple heuristic (4 characters ≈ 1 token) for budget enforcement. Exact token counting requires a tokenizer dependency that isn't worth adding for budget caps.

**4. Default memory config values.** What should the defaults be for pipes that don't declare a memory section? **Recommendation:** `working_state` context only, budget 500. Conservative — pipes that need more declare it. This keeps injection cheap for pipes that don't care about memory.

## Sub-Tasks

This spec is large but the phases are sequential and each is independently testable. Decomposition into sub-tasks is recommended if implementing in parallel:

1. **Foundation** (steps 1-2): Envelope + config changes. No behavioral change.
2. **Store layer** (steps 3-4): New tables and retrieval methods. Pure data layer.
3. **Runtime infrastructure** (steps 5-6): Injection and auto-save. Core behavioral change.
4. **Pipe declarations** (steps 7-9): Memory configs and template simplification.
5. **Wiring + integration** (steps 10-11): Connect everything, end-to-end tests.
