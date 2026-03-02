# Feature: Virgil v0.1.0

## Metadata

type: `feat`
task_id: `v0.1.0`
prompt: `Build the narrowest slice that proves the architecture is sound — signal in, router, planner, pipe execution, output — end-to-end with real functionality.`

## Feature Description

v0.1.0 is the first working build of Virgil. It validates the core architecture by forcing all four layers — signal intake, routing, planning, and pipe execution — to work end-to-end with real functionality. No mocks in the critical path.

The scope is deliberately narrow: 4 pipes, all 4 router layers (Layer 4 as stub fallback instead of AI), inline templates only, no pipelines, no voice, no self-healing intelligence. The goal is to prove that the envelope contract works, that deterministic routing is fast, that pipes compose through sequential chaining, and that the system degrades gracefully when it doesn't understand a signal.

The architecture is validated when six scenarios work cleanly without feeling fragile:

1. `virgil "what's on my calendar today"` — routes to `calendar`, returns events, feels instant
2. `virgil "remember that OAuth uses short-lived tokens"` — routes to `memory`, stores, confirms
3. `virgil "what do I know about OAuth"` — routes to `memory`, retrieves, returns
4. `virgil "draft a blog post about my notes on OAuth"` — routes to `memory.retrieve | draft(type=blog)`, produces a real draft
5. `virgil "something completely unrecognized"` — hits stub fallback, returns gracefully, logs the miss
6. The miss log has enough structure that a future process could read it to propose new keywords

## User Story

As a developer using Virgil locally
I want to issue natural language commands that route to the correct pipe and return useful output
So that I can validate the core architecture before building pipelines, voice, and self-healing on top of it

## Relevant Files

- `specs/virgil.md` — system philosophy, signal model, router layers, pipe composition, pipeline execution, self-healing, voice, interaction model
- `specs/ARCHITECTURE.md` — 26 architectural decisions with rationale, 8 open questions
- `specs/pipe.md` — pipe contract: definition, handler, envelope, triggers, flags, prompts, metrics, testing
- `specs/pipeline.md` — pipeline contract (deferred for v0.1.0, referenced for future compatibility)

### New Files

**Go module and build**

- `go.mod` — Go module definition
- `cmd/virgil/main.go` — single binary entry point (server + CLI)
- `justfile` — task runner for build, test, run, lint

**Envelope and pipe interface**

- `internal/envelope/envelope.go` — envelope struct, constructors, JSON serialization
- `internal/pipe/pipe.go` — pipe interface definition (`Definition`, `Handler`)
- `internal/pipe/registry.go` — pipe registry (register, lookup by name)

**Runtime**

- `internal/runtime/runtime.go` — sequential pipe execution engine, envelope passing
- `internal/runtime/logging.go` — automatic envelope transition observer

**Router, parser, planner**

- `internal/router/router.go` — layered router (exact → keyword → category → stub fallback)
- `internal/router/misslog.go` — structured miss log (append-only JSON lines)
- `internal/parser/parser.go` — rule-based component extraction (verb, type, source, modifier, topic)
- `internal/parser/vocabulary.go` — configuration-driven vocabulary
- `internal/planner/planner.go` — inline template matching, execution plan construction

**Pipes**

- `internal/pipes/memory/memory.go` — store/retrieve pipe backed by SQLite FTS5
- `internal/pipes/calendar/calendar.go` — read-only calendar pipe (Google Calendar API)
- `internal/pipes/draft/draft.go` — AI-powered text generation with type flag
- `internal/pipes/chat/chat.go` — conversational fallback pipe

**AI bridge**

- `internal/bridge/bridge.go` — provider interface (abstract), provider factory
- `internal/bridge/claude.go` — Claude Code CLI provider implementation (`claude -p`), the v0.1.0 default

**Memory storage**

- `internal/store/store.go` — SQLite FTS5 memory store

**Server**

- `internal/server/server.go` — HTTP server, localhost only, graceful shutdown
- `internal/server/api.go` — API endpoints (send signal, get status)

**TUI client**

- `internal/tui/tui.go` — bubbletea interactive session mode
- `internal/tui/oneshot.go` — one-shot command mode

**Configuration**

- `internal/config/config.go` — configuration loading and types
- `config/pipes/` — per-pipe YAML definitions (triggers, flags, prompts)
- `config/vocabulary.yaml` — parser vocabulary (verbs, types, sources, modifiers)
- `config/templates.yaml` — inline plan templates
- `config/virgil.yaml` — global config (provider model, claude binary path, log level, server port, database path)

**Tests**

- `internal/envelope/envelope_test.go`
- `internal/pipe/registry_test.go`
- `internal/runtime/runtime_test.go`
- `internal/router/router_test.go`
- `internal/parser/parser_test.go`
- `internal/planner/planner_test.go`
- `internal/pipes/memory/memory_test.go`
- `internal/pipes/calendar/calendar_test.go`
- `internal/pipes/draft/draft_test.go`
- `internal/pipes/chat/chat_test.go`
- `internal/store/store_test.go`
- `internal/bridge/bridge_test.go`

## Implementation Plan

### Phase 1: Foundation

Go module, envelope types, pipe interface, configuration loading. Everything downstream depends on these types being right.

**Envelope** — the Go struct that flows between pipes. All 8 fields from the spec. JSON tags for serialization. Constructor functions. `content` is `any` (the spec says intentionally unstructured). `error` is a struct with `message`, `severity` (warn/error/fatal), and `retryable` bool. Null on success.

**Pipe interface** — a `Definition` struct (loaded from YAML config) and a `Handler` function type (`func(input Envelope, flags map[string]string) Envelope`). Each pipe package exposes a factory function (e.g. `NewHandler(store *store.Store) Handler` or `NewHandler(provider bridge.Provider) Handler`) that captures dependencies via closure and returns a `Handler`. Non-deterministic pipes capture a `bridge.Provider` at construction time — the handler signature stays uniform, but the closure holds the provider. A `Registry` maps pipe names to handlers. All pipes are registered explicitly during server startup (not via `init()`). The runtime resolves each pipe's provider: if the pipe definition declares a provider override, use that; otherwise use the global default. v0.1.0 only implements the global default path, but the plumbing for per-pipe override is in the `Definition` struct and the runtime's resolution logic.

**Configuration** — load YAML files from a config directory. Pipe definitions, vocabulary, templates, and global settings. The config path is `~/.config/virgil/` with a fallback to `./config/` for development.

### Phase 2: Core Engine

The runtime that executes pipes sequentially and the logging infrastructure that observes envelope transitions. This is the riskiest piece — build it before the router exists so it can be tested directly.

**Runtime** — takes an execution plan (a list of pipe name + flags pairs) and runs them in sequence. Each pipe receives the previous pipe's output envelope. The first pipe receives a seed envelope constructed from the original signal. The runtime returns the final envelope.

**Logging** — the runtime automatically logs every envelope transition at configurable verbosity. At `info`: pipe name, action, duration, success/failure. At `debug`: additionally the full envelope content between steps. Logging is a function the runtime calls at each transition — not a pipe, not middleware. Global log level set in config.

### Phase 3: Pipes and Dependencies

The four pipes plus their dependencies (SQLite store, AI bridge via Claude Code CLI). Each pipe implements the handler interface and registers itself.

**memory pipe** — deterministic. Two actions: `store` and `retrieve`. `store` writes to SQLite with FTS5 indexing. `retrieve` queries FTS5 and returns matching entries as a list. Flags: `action` (store/retrieve), `query` (search terms), `limit` (max results, default 10), `sort` (recent/relevant, default relevant). Content type on retrieve is `list`. Content type on store is `text` (confirmation message).

**SQLite store** — a single `.db` file at a configurable path (default `~/.local/share/virgil/memory.db`). Two tables: `entries` (id, content, tags, created_at, updated_at) and `entries_fts` (FTS5 virtual table on content). The store exposes `Store(content string, tags []string)` and `Search(query string, limit int) []Entry`. FTS5 handles relevance ranking.

**calendar pipe** — deterministic, read-only. Calls Google Calendar API. Flags: `range` (today/tomorrow/this-week, default today), `calendar` (which calendar, default primary). Returns events as a structured list. Each event has title, start, end, location. Requires OAuth2 credentials — pipe validates credentials exist before calling API, returns fatal error if missing.

**draft pipe** — non-deterministic. Takes input content (from upstream envelope) and a `type` flag (blog, email, pr, memo). Resolves the matching prompt template, injects content and flags, calls the AI bridge. Returns generated text. When receiving `content_type: "list"` from an upstream pipe (e.g. memory.retrieve), the content is serialized to text before template injection using the envelope package's `ContentToText()` utility (see below).

**AI bridge** — an abstract `Provider` interface with a single method: `Complete(ctx context.Context, system string, user string) (string, error)`. Pipes receive a `Provider` — they don't know or care which implementation is behind it. A global default provider is configured at the project level. In the future, individual pipes can override with a different provider in their definition (per ARCHITECTURE.md decision #6), but v0.1.0 implements only the global default.

The v0.1.0 default provider is **Claude Code CLI**: pipes the user prompt via stdin to `claude -p --system-prompt "system" --output-format json --model sonnet --no-session-persistence --max-turns 1` and parses the JSON response. The user prompt is passed via stdin (not as an argument to `-p`) to avoid shell escaping issues with long or complex prompts. Uses Claude Code's built-in authentication (no API key management needed). Model configurable in global config (default: `sonnet`). The `--max-turns 1` flag ensures a single completion with no tool use — the pipe is asking for text generation, not agentic work. The `--no-session-persistence` flag keeps invocations stateless.

Adding a new provider (direct API, Gemini, local model, etc.) means implementing the `Provider` interface and registering it in the factory — no pipe code changes.

**Content serialization** — a `ContentToText(content any, contentType string) string` utility in the envelope package. Used by pipes (and the template engine) when they need to inject upstream content into a prompt or display it as text. Conversion rules: `text` → return as-is. `list` → render each item as a numbered line; if items are structs/maps, render each field as `key: value`. `structured` → render as `key: value` pairs, one per line. This is the bridge between the unstructured `content` field and text-based consumers like the draft pipe's prompt templates. The draft pipe calls `ContentToText()` when building its prompt — it never inspects `content_type` directly.

**chat pipe** — non-deterministic. The conversational fallback. No type flag. Takes the raw signal as content and sends it to the AI provider with a simple conversational system prompt. Exists to handle anything the router can classify as "general conversation" but that doesn't match a more specific pipe.

### Phase 4: Router, Parser, Planner

The signal processing pipeline: parse the signal into components, route it through deterministic layers, build an execution plan.

**Vocabulary** — loaded from YAML config. Maps of known words to their roles. Verbs map to **pipe references**: either a bare pipe name (`draft`, `calendar`) or a `pipe.action` pair (`memory.store`, `memory.retrieve`). When a verb maps to `pipe.action`, the parser splits on `.` and stores the pipe name in the `Verb` field and the action in a new `Action` field on `ParsedSignal`. Types, sources, and modifiers map to canonical values. Small initial set — just enough for the 4 pipes: verbs (`draft`, `write`, `compose`, `remember`, `recall`, `know`, `check`, `show`, `what's`), types (`blog`, `email`, `pr`, `memo`), sources (`notes`, `calendar`, `memory`), modifiers (`recent`, `today`, `tomorrow`, `this week`).

**Parser** — scans the signal string against the vocabulary. Extracts components by greedy matching: first match wins. Produces a `ParsedSignal` struct with fields for `Verb` (pipe name), `Action` (extracted from verb mapping if it contained a dot, empty otherwise), `Type`, `Source`, `Modifier`, `Topic` (whatever remains after extraction and stop word removal), and `Raw` (original signal). Stop words stripped before topic extraction: `a`, `an`, `the`, `my`, `on`, `in`, `at`, `to`, `for`, `of`, `is`, `that`, `about`, `do`, `i`, `it`, `me`, `and`, `or`, `but`, `with`, `from`, `post`. No connectors in v0.1.0.

**Router layers:**

- **Layer 1: Exact match** — hash map lookup of the full signal string against exact triggers from all pipe definitions. O(1). Returns immediately if found.
- **Layer 2: Keyword index** — inverted index mapping individual words to pipe names. Built at startup from pipe keyword triggers. Score each pipe by how many of its keywords appear in the signal. Return the highest-scoring pipe if score ≥ threshold (configurable, default 0.6 = at least 60% of a pipe's keywords appeared). Note: this threshold naturally favors pipes with smaller, focused keyword sets — a pipe with 3 keywords where 2 match (67%) beats a pipe with 20 keywords where 5 match (25%). This is intentional: focused matches are higher confidence. With only 4 pipes in v0.1.0, Layer 2 picks the specific pipe directly rather than only identifying a category (the category optimization matters at 50+ pipes).
- **Layer 3: Category narrowing** — if Layer 2 had keyword hits but no pipe scored above threshold, it identifies the category of the best-scoring pipe and passes it to Layer 3. Layer 3 scores the signal against only the pipes in that category using the parsed components and trigger pattern matching. Returns the best match if confidence is sufficient.
- **Layer 4: Stub fallback** — does not classify. Logs the miss (signal text, keywords found, keywords not found, timestamp) to a structured JSON lines file. Returns the `chat` pipe as a fallback. The miss log is the foundation for future self-healing.

**Planner** — takes the router's chosen pipe and the parsed signal, matches against inline templates, and produces an execution plan. Template matching is **component-presence-based**, not string-pattern-based: the template `{verb} {type} based on {source}` matches when the parsed signal has non-empty verb, type, AND source fields. The text "based on" in the template is documentation of the natural language pattern, not a literal string to match. Templates are checked in order; first match wins. Templates are configuration-driven:

```
has verb + type + source  →  source(action=retrieve) | verb(type={type})
has verb + type + topic   →  verb(type={type}, topic={topic})
has verb + source         →  source(action=retrieve)
has verb only             →  verb()
```

The planner resolves template variables to actual pipe names and flags. The `Verb` field maps to a pipe name (already resolved by the parser via vocabulary). The `Source` field maps to a pipe name via the vocabulary's source map (e.g. `notes` → `memory`). When a template step needs an action (like `retrieve`), the planner sets it as a flag (`action=retrieve`) in the step's flags map. The output is an ordered list of `(pipe_name, flags)` pairs — the execution plan the runtime consumes. The `action` flag is how the planner communicates the intended operation to multi-action pipes like memory — it flows through the same flags map as all other flags.

If no template matches, the plan is a single-pipe invocation of whatever the router selected, with the parsed action (if any) set as the `action` flag.

### Phase 5: Server and Client

HTTP server, API layer, TUI client. The server runs the full processing loop. The client sends signals and displays output.

**HTTP server** — `net/http` on `localhost:7890` (configurable). Two endpoints:

- `POST /signal` — accepts `{"text": "..."}`, runs the full loop (parse → route → plan → execute), returns the final envelope as JSON.
- `GET /health` — returns server status.

Graceful shutdown on SIGINT/SIGTERM. PID file at `~/.local/share/virgil/virgil.pid`. Server logs to stderr.

**TUI client (bubbletea)** — two modes:

- **One-shot**: `virgil "what's on my calendar"` — sends the signal to the server, prints the response content, exits. If the server isn't running, starts it as a background process first.
- **Session**: `virgil` with no args — opens an interactive session. Input prompt at the bottom, responses stream above. Simple scrolling view. No detail panel in v0.1.0 (that's a pipeline feature). Ctrl-C to exit.

**Server auto-start** — the CLI checks for the PID file and whether the process is alive. If not running, starts the server as a background process, waits for it to be healthy (poll `/health`), then proceeds.

### Phase 6: Integration and Validation

Wire everything together. Run the six validation scenarios. Fix what breaks.

The startup sequence:

1. Load configuration from config directory
2. Initialize SQLite store
3. Build vocabulary, parser, router (layers 1-3 from pipe definitions)
4. Initialize AI bridge (check if `claude` CLI is available and authenticated — warn if not, but continue startup)
5. Register all pipe handlers
6. Start HTTP server

The signal processing loop (per request):

1. Receive signal text from API
2. Parse signal into components
3. Route through layers 1 → 2 → 3 → 4 (stub)
4. Build execution plan from inline templates
5. Execute plan through runtime (sequential pipe chain)
6. Return final envelope to client

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Initialize Go Module and Project Structure

- Run `go mod init github.com/justinpbarnett/virgil`
- Create directory structure: `cmd/virgil/`, `internal/envelope/`, `internal/pipe/`, `internal/runtime/`, `internal/router/`, `internal/parser/`, `internal/planner/`, `internal/pipes/memory/`, `internal/pipes/calendar/`, `internal/pipes/draft/`, `internal/pipes/chat/`, `internal/bridge/`, `internal/store/`, `internal/server/`, `internal/tui/`, `internal/config/`, `config/`
- Create `cmd/virgil/main.go` with a minimal `main()` that prints "virgil v0.1.0"
- Create `justfile` with targets: `build`, `test`, `run`, `lint`
- Verify: `just build` produces a binary, `just test` runs (no tests yet, should pass)

### 2. Define Envelope Type

- Create `internal/envelope/envelope.go`:
  - `Envelope` struct with all 8 fields: `Pipe` (string), `Action` (string), `Args` (map[string]string), `Timestamp` (time.Time), `Duration` (time.Duration), `Content` (any), `ContentType` (string: "text", "list", "structured", "binary"), `Error` (\*EnvelopeError or nil)
  - `EnvelopeError` struct: `Message` (string), `Severity` (string: "warn", "error", "fatal"), `Retryable` (bool)
  - `New(pipe, action string) Envelope` constructor — sets timestamp to now, error to nil
  - JSON marshaling/unmarshaling with `encoding/json` tags
  - `ContentToText(content any, contentType string) string` — serializes envelope content to text for use in prompts and display. Rules: `text` → return string as-is. `list` → number each item on its own line; if items are structs/maps, render fields as `key: value`. `structured` → render as `key: value` pairs. `binary` → return `"[binary content]"`. Unknown/nil → return empty string.
- Create `internal/envelope/envelope_test.go`:
  - Test construction, JSON round-trip, error field nil on success, error field populated on failure
  - Test ContentToText: text passthrough, list of strings, list of structs, structured map, nil content, binary placeholder

### 3. Define Pipe Interface and Registry

- Create `internal/pipe/pipe.go`:
  - `Handler` type: `func(input envelope.Envelope, flags map[string]string) envelope.Envelope`
  - Each pipe package exposes a factory function that takes dependencies and returns a `Handler`. For example: `memory.NewHandler(store *store.Store) Handler` captures the store in a closure. `draft.NewHandler(provider bridge.Provider) Handler` captures the provider. The `Handler` type stays uniform across all pipes — dependencies are injected at construction, not per-call.
  - `Definition` struct: `Name`, `Description`, `Category`, `Triggers` (exact []string, keywords []string, patterns []string), `Flags` map, `Provider` (*ProviderConfig, nil means use global default — the field exists now so per-pipe overrides work in the future without changing the struct)
- Create `internal/pipe/registry.go`:
  - `Registry` struct with a map of name → Handler
  - `Register(name string, handler Handler)`
  - `Get(name string) (Handler, bool)`
  - `Definitions() []Definition` — returns all registered definitions (for router startup)
- Create `internal/pipe/registry_test.go`:
  - Test register, get, get-missing, definitions listing

### 4. Build Configuration Loading

- Create `internal/config/config.go`:
  - `Config` struct: `Server` (host, port), `Provider` (model, binary path), `LogLevel` (string), `DatabasePath` (string), `ConfigDir` (string)
  - `PipeConfig` struct matching the YAML pipe definition schema from `pipe.md`
  - `VocabularyConfig` struct: maps of verbs, types, sources, modifiers
  - `TemplateConfig` struct: list of inline templates with pattern and plan
  - `Load(configDir string) (*Config, error)` — reads YAML files from the directory. Top-level files (`virgil.yaml`, `vocabulary.yaml`, `templates.yaml`) loaded directly. Pipe definitions loaded by scanning the `pipes/` subdirectory — every `.yaml` file in `pipes/` is parsed as a `PipeConfig`. This lets new pipes be added by dropping a YAML file.
- Create YAML config files in `config/`:
  - `config/virgil.yaml` — server port 7890, default provider claude (model: sonnet), log level info, database path `~/.local/share/virgil/memory.db`
  - `config/vocabulary.yaml` — initial vocabulary covering the 4 pipes
  - `config/templates.yaml` — inline templates for the plan stage
  - `config/pipes/memory.yaml` — memory pipe definition
  - `config/pipes/calendar.yaml` — calendar pipe definition
  - `config/pipes/draft.yaml` — draft pipe definition with prompt templates
  - `config/pipes/chat.yaml` — chat pipe definition with system prompt

### 5. Build Sequential Runtime and Logging

- Create `internal/runtime/runtime.go`:
  - `Plan` struct: ordered list of `Step` (pipe name + flags map). The flags map carries all per-step configuration including the `action` flag when the planner specifies one (e.g. `action=retrieve` for memory).
  - `Runtime` struct: holds a pipe registry reference
  - `Execute(plan Plan, seed envelope.Envelope) envelope.Envelope` — runs each step sequentially, passing the output envelope of step N as the input to step N+1. Returns the final envelope. Sets the runtime's duration on the final envelope.
  - On pipe error with severity "fatal": halt execution, return the error envelope immediately.
  - On pipe error with severity "warn": log the warning, continue with the envelope (content may be partial).
- Create `internal/runtime/logging.go`:
  - `Observer` that the runtime calls at each envelope transition
  - At `info` level: log pipe name, action, duration, success/failure — one line per pipe
  - At `debug` level: additionally log full envelope content (JSON-serialized)
  - Use `log/slog` for structured logging
- Create `internal/runtime/runtime_test.go`:
  - Test with mock pipes: 1-pipe plan, 2-pipe plan (verify envelope passes correctly), 3-pipe plan
  - Test error propagation: fatal error halts, warn continues
  - Test that the observer is called at each transition

### 6. Build SQLite Memory Store

- Create `internal/store/store.go`:
  - `Store` struct wrapping a `*sql.DB`
  - `Open(path string) (*Store, error)` — opens or creates the SQLite database, runs migrations (create tables + FTS5)
  - Schema: `entries` table (id INTEGER PRIMARY KEY, content TEXT, tags TEXT, created_at DATETIME, updated_at DATETIME), `entries_fts` FTS5 virtual table on `content`
  - `Save(content string, tags []string) error`
  - `Search(query string, limit int) ([]Entry, error)` — FTS5 MATCH query, ordered by rank
  - `Entry` struct: `ID`, `Content`, `Tags` ([]string), `CreatedAt`, `UpdatedAt`
  - `Close() error`
- Add `modernc.org/sqlite` dependency (pure Go, no CGO required)
- Create `internal/store/store_test.go`:
  - Test open/create, save single entry, save multiple entries, search by keyword, search returns ranked results, search with limit, empty search, close

### 7. Build AI Bridge

- Create `internal/bridge/bridge.go`:
  - `Provider` interface: `Complete(ctx context.Context, system string, user string) (string, error)`
  - `ProviderConfig` struct: `Name` (string, e.g. "claude"), `Model` (string), provider-specific fields via `Options map[string]string`
  - `NewProvider(config ProviderConfig) (Provider, error)` — factory function that dispatches on `config.Name`. v0.1.0 supports `"claude"` only; unknown names return a clear error.
  - The interface is the abstraction boundary. Pipes depend on `Provider`, never on a concrete implementation. Future providers (direct API, Gemini, local models) implement this interface and register in the factory.
- Create `internal/bridge/claude.go`:
  - `ClaudeProvider` struct implementing `Provider` using `os/exec`
  - Invocation: `claude -p --system-prompt "{system prompt}" --output-format json --model {model} --no-session-persistence --max-turns 1` with the user prompt piped via stdin. The `-p` flag puts Claude in non-interactive prompt mode; when no prompt argument follows `-p`, it reads from stdin. This avoids shell escaping issues with long or complex prompts.
  - Parse JSON response, extract `.result` field as the completion text
  - Model from config (default `sonnet`)
  - `Available() bool` — checks if the `claude` binary is on PATH and authenticated. Called at startup to warn (not block). Called again on each `Complete()` — if unavailable, return a clear error immediately without attempting the subprocess.
  - Handles errors: non-zero exit code → parse stderr for error message. Command not found (`claude` not on PATH) → fatal error with message to install. Timeout (use `context.Context` deadline) → retryable. Auth failure (exit code + error message inspection) → fatal with helpful message to run `claude auth login`.
  - No API key management — Claude Code handles its own auth via `claude auth login`
  - Config options: `binary` (path to claude binary, default `claude`)
- Create `internal/bridge/bridge_test.go`:
  - Test the `Provider` interface with a mock implementation: verify pipes can use any provider
  - Test `ClaudeProvider` with a mock script that mimics `claude` CLI output: successful completion, error exit, timeout
  - Test JSON response parsing: valid response, malformed response
  - Test stdin prompt passing: verify long prompts with special characters work

### 8. Build Memory Pipe

- Create `internal/pipes/memory/memory.go`:
  - `NewHandler(store *store.Store) pipe.Handler` — factory function that captures the store in a closure and returns a Handler
  - Handler dispatches on `action` flag:
    - `store`: extract content from input envelope, save to SQLite store, return confirmation envelope with `content_type: "text"`
    - `retrieve`: extract query from flags (or from input envelope content), search store, return results as envelope with `content_type: "list"`. Each item in the list is a `store.Entry` struct with `Content`, `Tags`, `CreatedAt` fields — the `ContentToText()` utility renders these as readable text when a downstream pipe needs text.
  - Validate required flags/input, return error envelope for missing data
  - Registered explicitly during server startup (not via `init()`)
- Create `internal/pipes/memory/memory_test.go`:
  - Test store action: saves content, returns confirmation
  - Test retrieve action: returns matching entries as list
  - Test retrieve with no results: returns empty list, no error
  - Test missing action flag: returns error envelope
  - Test empty content on store: returns error envelope

### 9. Build Calendar Pipe

- Create `internal/pipes/calendar/calendar.go`:
  - Handler function: calls Google Calendar API for the specified range
  - Flags: `range` (today/tomorrow/this-week, default today), `calendar` (default primary)
  - Returns events as `content_type: "list"` with each event containing title, start, end, location
  - Validates OAuth2 credentials exist, returns fatal error if missing
  - Uses `golang.org/x/oauth2` and Google Calendar API client
  - OAuth2 token stored at `~/.config/virgil/google-token.json`
- Create `SETUP.md` documenting Google Calendar API setup: creating a GCP project, enabling Calendar API, downloading OAuth2 credentials, running the token flow, expected file locations. This is referenced in Resolved Questions #3.
- Create `internal/pipes/calendar/calendar_test.go`:
  - Test with mocked HTTP responses: returns events correctly, handles empty calendar, handles API error, handles missing credentials

### 10. Build Draft Pipe

- Create `internal/pipes/draft/draft.go`:
  - `NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig) pipe.Handler` — factory function that captures the provider and prompt templates in a closure
  - Handler: resolves prompt template from `type` flag, converts input envelope content to text via `envelope.ContentToText(input.Content, input.ContentType)`, injects the text and flags into the template, calls AI bridge
  - Flags: `type` (blog/email/pr/memo), `tone` (formal/casual/technical), `length` (short/medium/long, default medium)
  - Template resolution: load templates from pipe config, substitute `{{content}}`, `{{topic}}`, `{{tone}}`, `{{length}}` — use `text/template` from stdlib
  - Returns generated text as `content_type: "text"`
  - If no matching template for the type flag, use system prompt alone with raw content
  - Error handling: provider timeout → retryable error, auth error → fatal, malformed response → fatal
- Create `internal/pipes/draft/draft_test.go`:
  - Test template resolution: correct template selected for each type
  - Test with mocked AI bridge: returns generated content
  - Test with no type flag: uses default behavior
  - Test with empty input content: returns error envelope
  - Test provider failure handling

### 11. Build Chat Pipe

- Create `internal/pipes/chat/chat.go`:
  - `NewHandler(provider bridge.Provider) pipe.Handler` — factory function that captures the provider in a closure
  - Handler: sends raw signal to AI bridge with a simple conversational system prompt
  - No type flag. Takes input envelope content directly.
  - System prompt from config: "You are Virgil, a personal assistant. Respond helpfully and concisely."
  - Returns response as `content_type: "text"`
- Create `internal/pipes/chat/chat_test.go`:
  - Test with mocked AI bridge: returns conversational response
  - Test with empty input: returns helpful "I didn't catch that" response
  - Test provider failure handling

### 12. Build Parser

- Create `internal/parser/parser.go`:
  - `Parser` struct holding the vocabulary
  - `Parse(signal string) ParsedSignal` — scans signal against vocabulary, extracts components
  - `ParsedSignal` struct: `Verb` (string — pipe name), `Action` (string — extracted from verb mapping if it contained a `.`, empty otherwise), `Type`, `Source`, `Modifier`, `Topic` (all strings, empty if not found), `Raw` (original signal)
  - Extraction order: verb first (action words), then type, then source, then modifier. Topic is whatever meaningful words remain after stop word removal.
  - When a verb maps to a `pipe.action` value (e.g. `memory.store`), the parser splits on `.` and stores pipe name in `Verb`, action in `Action`.
  - Case-insensitive matching.
  - Stop words (stripped before topic extraction): `a`, `an`, `the`, `my`, `on`, `in`, `at`, `to`, `for`, `of`, `is`, `that`, `about`, `do`, `i`, `it`, `me`, `and`, `or`, `but`, `with`, `from`, `post`, `what`, `how`, `when`, `where`, `can`, `does`, `will`.
- Create `internal/parser/vocabulary.go`:
  - `Vocabulary` struct: maps for verbs, types, sources, modifiers
  - `LoadVocabulary(config VocabularyConfig) *Vocabulary`
- Create `config/vocabulary.yaml`:
  - Verbs: `draft` → draft, `write` → draft, `compose` → draft, `remember` → memory.store, `recall` → memory.retrieve, `know` → memory.retrieve, `check` → calendar, `show` → calendar, `what's` → calendar
  - Types: `blog` → blog, `email` → email, `pr` → pr, `memo` → memo
  - Sources: `notes` → memory, `calendar` → calendar, `memory` → memory
  - Modifiers: `recent` → recent, `today` → today, `tomorrow` → tomorrow, `this week` → this-week
- Create `internal/parser/parser_test.go`:
  - Test "draft a blog post about OAuth" → verb=draft, action="", type=blog, topic=OAuth
  - Test "what's on my calendar today" → verb=calendar, action="", source=calendar, modifier=today, topic=""
  - Test "remember that OAuth uses short-lived tokens" → verb=memory, action=store, topic="OAuth uses short-lived tokens"
  - Test "what do I know about OAuth" → verb=memory, action=retrieve, topic=OAuth
  - Test unrecognized signal → all fields empty except topic (full signal after stop word removal)

### 13. Build Router

- Create `internal/router/router.go`:
  - `Router` struct holding: exact match map, keyword index (inverted index), category-to-pipes map, pipe definitions
  - `NewRouter(definitions []pipe.Definition) *Router` — builds all index structures at startup
  - `Route(signal string, parsed ParsedSignal) RouteResult`
  - `RouteResult` struct: `Pipe` (string), `Confidence` (float64), `Layer` (int, which layer resolved it). No flags in RouteResult — flag extraction is the planner's job.
  - Layer 1: exact match lookup — `O(1)`, return if found with confidence 1.0
  - Layer 2: tokenize signal, look up each token in inverted index, score pipes by keyword hit count. Score = keywords_matched / total_keywords_for_pipe. Return highest-scoring pipe if score ≥ threshold (configurable, default 0.6). This favors focused keyword sets — a pipe with 3 keywords and 2 matches (67%) beats a pipe with 20 keywords and 5 matches (25%), which is correct behavior (focused match = higher confidence).
  - Layer 3: if Layer 2 had keyword hits but no pipe above threshold, derive the category from the best-scoring pipe's category. Score the signal against only the pipes in that category using the parsed components and trigger pattern matching. Return the best match if confidence is sufficient.
  - Layer 4: stub — log the miss, return `chat` pipe with confidence 0.0
- Create `internal/router/misslog.go`:
  - `MissLog` struct wrapping a file handle
  - `Log(entry MissEntry)` — appends a JSON line to the miss log file
  - `MissEntry` struct: `Signal`, `KeywordsFound`, `KeywordsNotFound`, `Timestamp`, `FallbackPipe`
  - File path: `~/.local/share/virgil/misses.jsonl`
- Create `internal/router/router_test.go`:
  - Test Layer 1: exact match "check my calendar" → calendar pipe, confidence 1.0
  - Test Layer 2: "what's on my schedule today" → calendar pipe via keyword scoring
  - Test Layer 3: "recall my OAuth notes" → memory pipe via category + pattern
  - Test Layer 4: "xyzzy foobar" → chat pipe, confidence 0.0, miss logged
  - Test miss log: verify JSONL structure, verify fields populated

### 14. Build Planner

- Create `internal/planner/planner.go`:
  - `Planner` struct holding template configuration and source vocabulary (to resolve source names like `notes` → pipe `memory`)
  - `Plan(route RouteResult, parsed ParsedSignal) runtime.Plan`
  - Template matching is **component-presence-based**: each template declares which parsed fields must be non-empty. Templates are checked in order; first match wins. The template's string pattern (e.g. `{verb} {type} based on {source}`) is documentation — the matching logic checks `parsed.Verb != "" && parsed.Type != "" && parsed.Source != ""`.
  - Template resolution: `parsed.Verb` is already a pipe name (resolved by the parser). `parsed.Source` is resolved to a pipe name via the source vocabulary (e.g. `notes` → `memory`). `parsed.Action` (if set) becomes the `action` flag. `parsed.Type` becomes the `type` flag value. `parsed.Topic` becomes the `topic` flag value.
  - If no template matches, produce a single-step plan for the routed pipe with `parsed.Action` as the `action` flag (if non-empty).
- Create `config/templates.yaml`:

  ```yaml
  templates:
    # Matches when verb, type, and source are all present
    # e.g. "draft a blog post about my notes on OAuth"
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, sort: recent, limit: "10" }
        - pipe: "{verb}"
          flags: { type: "{type}" }

    # Matches when verb and type are present (with or without topic)
    # e.g. "draft a blog post about OAuth"
    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }

    # Matches when verb and source are present
    # e.g. "check calendar", "recall notes"
    - requires: [verb, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve }

    # Matches when only verb is present
    # e.g. "remember ...", "draft ..."
    - requires: [verb]
      plan:
        - pipe: "{verb}"
  ```

- Create `internal/planner/planner_test.go`:
  - Test parsed signal with verb=draft, type=blog, source=memory → 2-step plan: memory(action=retrieve, sort=recent, limit=10) then draft(type=blog)
  - Test parsed signal with verb=draft, type=blog, topic=OAuth → 1-step plan: draft(type=blog, topic=OAuth)
  - Test parsed signal with verb=calendar, source=calendar → 1-step plan: calendar(action=retrieve)
  - Test parsed signal with verb=memory, action=store → single-step plan: memory(action=store) — the action from the parser overrides
  - Test no template match (all fields empty) → single-step plan for the routed pipe
  - Test template variable resolution produces correct pipe names and flags

### 15. Build HTTP Server

- Create `internal/server/server.go`:
  - `Server` struct holding router, planner, runtime, config
  - `New(config, router, planner, runtime) *Server`
  - `Start() error` — starts HTTP server on configured host:port, writes PID file
  - `Shutdown(ctx context.Context) error` — graceful shutdown, removes PID file
  - Signal handling: SIGINT and SIGTERM trigger graceful shutdown
- Create `internal/server/api.go`:
  - `POST /signal` handler:
    - Parse JSON body: `{"text": "..."}`
    - Run the full loop: parse → route → plan → execute
    - Return the final envelope as JSON
  - `GET /health` handler: return `{"status": "ok"}`
- Create `internal/server/server_test.go`:
  - Test `/health` returns 200
  - Test `/signal` with a valid signal returns an envelope
  - Test `/signal` with empty body returns 400

### 16. Build TUI Client

- Create `internal/tui/oneshot.go`:
  - `RunOneShot(signal string, serverAddr string) error`
  - Sends POST to `/signal`, reads response, prints `content` field to stdout
  - Uses `envelope.ContentToText()` for display formatting — text prints as-is, lists render as numbered items, structured data renders as key-value pairs. The same utility used by pipes for prompt injection handles display formatting.
  - If error is non-nil, print error message to stderr
- Create `internal/tui/tui.go`:
  - Bubbletea model with: input text field at bottom, scrolling output area above
  - On submit: send signal to server, display response
  - Display format: `you > {signal}` then `{response content}` with a blank line between exchanges
  - Ctrl-C to quit
  - Minimal styling — functional, not pretty. Styling is a future concern.
- Add `github.com/charmbracelet/bubbletea` and `github.com/charmbracelet/bubbles` dependencies
- Create `internal/tui/autostart.go`:
  - `EnsureServer(config) error` — acquire a file lock (`~/.local/share/virgil/virgil.lock`) to prevent race conditions between concurrent CLI invocations, check PID file, check process alive, start server if needed, poll `/health` until ready (timeout 5s), release lock

### 17. Wire Main Entry Point

- Update `cmd/virgil/main.go`:
  - Parse CLI args: if args present, run one-shot mode. If no args, run session mode.
  - `--config` flag for config directory override
  - `--server` flag to run in server-only mode (no TUI)
  - Startup sequence:
    1. Load configuration
    2. If server mode: initialize everything (store, bridge, pipes, router, planner, runtime, server) and start
    3. If client mode: ensure server is running (auto-start if needed), then run TUI or one-shot
  - Register all 4 pipes with the registry during server initialization

### 18. Integration Testing and Validation

- Create `tests/integration_test.go` (or `internal/integration_test.go`):
  - Spin up the full server in-process
  - Run the six validation scenarios as test cases:
    1. Send "what's on my calendar today" → verify routes to calendar pipe, returns list content type
    2. Send "remember that OAuth uses short-lived tokens" → verify routes to memory, stores, returns confirmation
    3. Send "what do I know about OAuth" → verify routes to memory, retrieves, returns matching entries
    4. Send "draft a blog post about my notes on OAuth" → verify 2-step plan (memory.retrieve | draft), returns text content type
    5. Send "xyzzy foobar nonsense" → verify falls through to chat, miss is logged
    6. Read the miss log file → verify it's valid JSONL with expected fields
  - Integration tests use a test SQLite database (temp file) and mock the AI bridge and calendar API
- Run all tests: `just test`
- Run the binary manually and try the six scenarios interactively

## Testing Strategy

### Unit Tests

Every package gets unit tests. The test priority order:

1. **envelope** — the contract. JSON round-trip. Field population. This must be correct.
2. **runtime** — sequential execution with mock pipes. Envelope passing. Error propagation. This is the riskiest piece.
3. **store** — SQLite operations. FTS5 search. Ranking. Uses a temp database per test.
4. **parser** — component extraction against vocabulary. Deterministic, easy to test exhaustively.
5. **router** — layer resolution. Each layer independently, then the full cascade. Miss logging.
6. **planner** — template matching. Variable resolution. Plan construction.
7. **pipes** — each pipe with mocked dependencies (mock store for memory, mock HTTP for calendar, mock bridge for draft/chat).
8. **bridge** — Claude CLI invocation with mock script.
9. **server** — HTTP handler tests with `httptest`.

### Integration Tests

The six validation scenarios run as integration tests with a real server instance, real SQLite database, but mocked external services (calendar API, AI provider). This tests the full signal → route → plan → execute → response loop.

### Edge Cases

- Signal with no recognizable words (all stop words)
- Signal that matches multiple pipes equally (keyword score tie)
- Memory retrieve with no stored entries
- Memory store with empty content
- Calendar with no events in range
- Draft with no input content (first pipe in chain)
- AI bridge timeout during draft execution
- Two-pipe chain where first pipe errors — verify second pipe doesn't run
- Very long signal (thousands of characters)
- Unicode in signals and memory content
- Concurrent requests to the server

## Risk Assessment

**Highest risk: envelope passing in 2-pipe chains.** The `memory.retrieve | draft(type=blog)` scenario is where the architecture proves itself or breaks. Memory returns `content_type: "list"` and draft needs text for its prompt template. This is resolved by the `ContentToText()` utility in the envelope package — draft calls it to serialize list content into numbered text lines before template injection. The draft pipe never inspects `content_type` directly. Mitigated by building and testing `ContentToText()` as part of the envelope package (Phase 1), building the runtime (Phase 2) before any pipes exist, then testing the 2-pipe chain as the first integration test.

**Calendar API credentials.** Google OAuth2 requires a credentials file and a token flow. This is development friction that could slow down the integration phase. Mitigated by making the calendar pipe fully mockable and not blocking other work on credential setup.

**AI bridge reliability.** Non-deterministic pipes depend on shelling out to `claude` CLI, which in turn calls an external API. The subprocess adds latency vs a direct API call, but avoids API key management and gets Claude Code's built-in auth, retry logic, and model routing for free. Mitigated by the mock bridge for tests, and by making the chat/draft pipes handle all error types gracefully. Requires `claude` to be installed and authenticated (`claude auth login`) on the host.

**SQLite driver.** Using `modernc.org/sqlite` (pure Go). No CGO, no cross-compilation issues.

**Config file ergonomics.** If the YAML config structure is wrong, everything downstream breaks. Mitigated by loading config early and validating all required fields with clear error messages.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just lint      # golangci-lint run ./...
just test      # go test ./... -v -count=1
just build     # go build -o bin/virgil ./cmd/virgil
```

## Resolved Questions

**1. Go module path** — `github.com/justinpbarnett/virgil`.

**2. SQLite driver** — `modernc.org/sqlite` (pure Go).

**3. Google Calendar API setup** — manual steps documented in a SETUP.md. Setup command deferred.

**4. Claude Code dependency** — warn at startup if `claude` CLI is missing or unauthenticated, but do not block. The app starts normally and deterministic pipes (memory, calendar) work fine. When a non-deterministic pipe is invoked without a working provider, it returns an error envelope explaining the problem.

**5. Bubbletea session** — simple alternating input/response display with scroll-back. No history search, no detail panel.

## Design Decisions (resolved during spec review)

**6. Vocabulary mapping scheme** — Verbs map to pipe references, either a bare pipe name (`draft`, `calendar`) or a `pipe.action` pair (`memory.store`, `memory.retrieve`). The parser splits on `.` to populate separate `Verb` and `Action` fields on `ParsedSignal`. This keeps the vocabulary simple while supporting multi-action pipes like memory.

**7. List→text content conversion** — A shared `ContentToText(content any, contentType string) string` utility in the envelope package. Used by the draft pipe's template engine and the TUI's display formatter. Lists render as numbered items; structs render as key-value pairs. Pipes never inspect `content_type` to decide conversion — they call the utility and get text.

**8. Template matching** — Component-presence-based, not string-pattern-based. The template `{verb} {type} based on {source}` matches when the parsed signal has non-empty verb, type, AND source fields. The natural language in the template pattern is documentation only. Templates checked in order; first match wins.

**9. Action routing** — The `action` flag (e.g. `action=retrieve`, `action=store`) flows through the same flags map as all other flags. The planner sets it when a template step requires an action, or when the parser extracted an action from a `pipe.action` verb mapping. Multi-action pipes like memory dispatch on `flags["action"]`. The envelope's `Action` field is set by the pipe itself in its output to record what it did.

**10. Pipe handler DI pattern** — Each pipe package exposes a factory function (e.g. `memory.NewHandler(store) Handler`, `draft.NewHandler(provider, config) Handler`) that captures dependencies via closure and returns a uniform `Handler`. All pipes registered explicitly during server startup, not via Go `init()`.

**11. Parser false positive mitigation for `know`** — `know` is added as a single-word verb mapping to `memory.retrieve`. False positives (e.g. "let me know") are mitigated by the router's keyword scoring — without memory-related keywords, the signal won't route to the memory pipe even if the parser extracts `know` as a verb.

**12. Stop word list** — Defined explicitly: `a`, `an`, `the`, `my`, `on`, `in`, `at`, `to`, `for`, `of`, `is`, `that`, `about`, `do`, `i`, `it`, `me`, `and`, `or`, `but`, `with`, `from`, `post`, `what`, `how`, `when`, `where`, `can`, `does`, `will`. Stripped from remaining text before topic extraction.

**13. Server auto-start race condition** — Mitigated with a file lock (`~/.local/share/virgil/virgil.lock`) acquired before PID file check and released after server start confirmation.

## Sub-Tasks

This spec is large enough to warrant decomposition. Recommended sub-task boundaries:

1. **Foundation** (Steps 1-4): Go module, envelope, pipe interface, config loading. ~1 session.
2. **Core Engine** (Step 5): Runtime, logging. ~1 session.
3. **Storage and Bridge** (Steps 6-7): SQLite store, AI bridge. ~1 session.
4. **Pipes** (Steps 8-11): memory, calendar, draft, chat. ~1-2 sessions.
5. **Router + Parser + Planner** (Steps 12-14): The full signal processing pipeline. ~1-2 sessions.
6. **Server + Client** (Steps 15-17): HTTP server, TUI, CLI wiring. ~1-2 sessions.
7. **Integration** (Step 18): End-to-end validation. ~1 session.
