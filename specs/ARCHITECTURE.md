# Architecture Decisions

This document records architectural decisions for Virgil, with context and rationale. It does not cover implementation detail — that belongs in code and per-component docs.

For the philosophy and conceptual model (what signals are, what pipes are, how self-healing works), see `virgil.md`.

---

## Decided

### 1. Go for the server

**Decision:** The server is written in Go.

**Rationale:** Single binary output. Strong concurrency primitives for managing parallel pipe execution and multiple client connections. Fast startup. No runtime dependency. The bubbletea ecosystem means the TUI client and server can share a language and build toolchain.

---

### 2. Client-server architecture

**Decision:** Virgil runs as a local server. Clients connect to it. The server owns all logic — routing, planning, pipe execution, memory, voice generation. Clients are thin renderers.

**Rationale:** Decoupling the intelligence from the interface means any client (terminal, browser, phone, desktop) gets the same capabilities without reimplementing anything. Memory is consistent across access points. A CLI session and a web session see the same data because they're talking to the same server.

The server runs locally by default. Remote access is a future concern, not an architectural one — the same server can be exposed over a tunnel or VPN without changes.

---

### 3. TUI as the primary client

**Decision:** The primary client is a terminal UI built with bubbletea. It supports three modes: interactive TUI, one-shot commands, and pipe mode (stdin/stdout).

**Rationale:** The terminal is always available. It composes with existing Unix tools. Pipe mode means Virgil can participate in shell pipelines natively. The TUI provides a rich interactive experience for longer sessions. One-shot mode handles quick queries.

The CLI client auto-starts the server if it's not running. The user never thinks about server lifecycle.

---

### 4. Web client is deferred

**Decision:** A web client is desirable but has no timeline or priority. It is not part of v0.1.0.

**Rationale:** The TUI covers the primary use case. Building a web client before the server, router, and pipes are solid would split focus. The client-server architecture means a web client can be added later without rearchitecting anything. When it happens, it connects to the same server via the same API.

---

### 5. Pipes are the atomic unit

**Decision:** Every capability is expressed as a pipe. A pipe does one thing, accepts an envelope as input, produces an envelope as output, and composes with other pipes.

**Rationale:** This is the Unix philosophy applied to a personal assistant. Composition replaces monolithic features. "Draft a blog post from my recent notes" is not a feature — it's `memory.retrieve | draft(type=blog)`. New capabilities emerge from combining existing pipes, not from writing new integrated features.

Some pipes are deterministic (API calls, database queries, calculations). Some are non-deterministic (AI model invocations). The distinction is an implementation detail hidden behind the same interface. A "draft" pipe with `--type=blog` and "draft" with `--type=email` are the same pipe with different flags.

---

### 6. AI bridge supports multiple providers

**Decision:** Non-deterministic pipes can invoke any AI model. A global default is configured at the project level. Individual pipes can override with a specific provider.

**Rationale:** Different tasks have different strengths across models. Code review may work best with one model, grounded web research with another, creative writing with a third. Locking to a single provider would force compromises on every task that isn't that provider's strength.

---

### 7. AI invocation supports both CLI and API

**Decision:** Pipes can invoke AI via CLI subprocess (e.g., `claude -p`, `gemini`, `llm`) or via direct HTTP API calls. The pipe's configuration declares which method to use.

**Rationale:** CLI subprocesses are simpler to implement and leverage existing tool authentication. Direct API calls are faster and offer more control (streaming, token limits, structured output). Some providers have good CLIs, some don't. Supporting both means each pipe uses whatever works best for its provider, and the approach can evolve per-pipe without a global migration.

---

### 8. Layered deterministic router with AI fallback

**Decision:** The router is a layered search engine, not an AI. Layers resolve in order: exact match → keyword index → category narrowing → AI fallback. Each layer is faster and cheaper than the next.

**Rationale:** Routing is the hottest path in the system. Every interaction goes through it. Making it deterministic means the common case resolves in microseconds with no network call, no model inference, no cost. AI is reserved for genuinely ambiguous queries — and when it fires, it logs the miss so the deterministic layers can learn.

Target: 80%+ of queries resolve in layers 1-2. The user never perceives routing latency on common requests.

---

### 9. Router self-heals from its own misses

**Decision:** Every AI fallback logs the query, the keywords that were (and weren't) found, the AI's decision, and its confidence. When misses accumulate past a threshold — or on explicit request — Virgil analyzes the log and proposes configuration changes: new keywords, new triggers, new categories.

**Rationale:** The AI fallback is expensive and slow. Every miss that can be converted into a deterministic route makes the system permanently faster. The self-healing loop means the system adapts to how you actually talk to it, without retraining or code changes. Improvements land in configuration files — inspectable, editable, reversible.

The self-healing process is itself expressed as the same signal → classify → plan → execute loop. It's not a special case.

---

### 10. Query parsing uses rule-based NLP, not model-based NLP

**Decision:** The parse stage extracts structured components (verb, type, source, modifier, topic, connector) by scanning for known vocabulary in configuration. No language models, no statistical parsing, no dependency trees.

**Rationale:** Dictionary-based extraction is deterministic, fast, and testable. It covers the patterns people actually use when talking to an assistant. The vocabulary grows over time through the self-healing process. For the rare query that doesn't match any vocabulary, the AI fallback handles it and logs the miss.

---

### 11. Templates map parsed components to execution plans

**Decision:** The plan stage matches parsed components against templates that produce execution plans. There are two kinds: inline templates that build a short chain of pipes on the fly, and pipeline templates that route to a named pipeline definition.

**Inline:** `{verb} {type} based on {source}` → `source.retrieve | verb(type)` — built at plan time, 2-3 pipes.

**Pipeline:** `add {feature} to {target}` → `dev-feature(spec=feature, target=target)` — references a pre-defined pipeline with its own graph, loops, and parallel branches.

**Rationale:** Simple queries don't need the overhead of a named pipeline definition. Complex workflows don't fit in a linear chain. Supporting both means the planner picks the right tool: inline templates for common queries, pipeline templates for workflows. Adding a new composition pattern means adding a template, not writing code.

---

### 12. Envelope defines a contract, not a format

**Decision:** The envelope specifies which fields every pipe must produce. Serialization format (JSON, YAML frontmatter, binary, etc.) is owned by the runtime and chosen based on context.

**Fields:**

| Field          | Purpose                                                                     |
| -------------- | --------------------------------------------------------------------------- |
| `pipe`         | Which pipe produced this                                                    |
| `action`       | What operation was performed                                                |
| `args`         | What flags were passed                                                      |
| `timestamp`    | When the pipe ran                                                           |
| `duration`     | How long it took                                                            |
| `content`      | The actual output — text, structured data, list, whatever the pipe produces |
| `content_type` | What kind of content (text, list, structured, binary)                       |
| `error`        | Null on success, error info on failure                                      |

**Rationale:** The envelope's job is interoperability between pipes. That's a contract question, not a serialization question. Deciding on JSON vs YAML vs protobuf now would constrain decisions that don't need to be made yet. The runtime can serialize to JSON for API transport, YAML frontmatter for file storage, or a Go struct for in-process pipe chaining — all from the same contract.

Pipes return a struct with these fields. The runtime decides how to move it.

---

### 13. Logging is infrastructure, not a pipe

**Decision:** The runtime observes every envelope transition automatically. Logging verbosity is configured globally with per-pipe overrides. Levels: silent, error, info, debug, trace.

**Rationale:** If logging were a pipe, you'd have to explicitly chain it into every pipeline. Miss one and you're blind. Logging is a cross-cutting concern that the runtime provides transparently. No pipe opts in. No pipeline includes a logging step. The runtime watches the envelope move between steps and records what it sees.

The router miss log is separate from general logging. General logging is observability. The miss log is a learning signal that feeds self-healing.

---

### 14. Voice output is one sentence or shorter

**Decision:** Voice is an output modality, not a feature. Any pipe's output can be spoken. Voice summaries are capped at one sentence, under ten words when possible.

**Rationale:** Voice is ambient awareness. It tells you the conclusion without requiring you to look at a screen. If the answer requires more than a sentence, voice gives the summary and the full output goes to the screen. This keeps voice useful and non-intrusive.

---

### 15. Categories are auto-generated and user-overridable

**Decision:** Pipe categories are generated from pipe metadata at server startup. The user can override any category assignment, add keywords, or create new categories in configuration.

**Rationale:** Auto-generation means adding a new pipe doesn't require manually updating the router. User overrides mean misclassifications can be fixed without waiting for a code change. The self-healing process can also propose category changes.

---

### 16. Pipelines are composable graphs of pipes

**Decision:** A pipeline is a named graph of pipes with execution semantics: sequential, parallel, loop, and cycle. Pipelines can contain other pipelines. From the outside, a pipeline looks like a pipe — it takes an envelope in and produces an envelope out. The router routes to pipelines the same way it routes to pipes.

**Rationale:** Pipes are atoms. Pipelines are molecules. Simple queries produce inline chains (2-3 pipes). Complex workflows — like a full feature development cycle with build/verify/review loops — need parallel branches, retry logic, and conditional cycles. Making pipelines a first-class concept with their own configuration means users define workflows declaratively. The same pipes can be arranged into different pipelines for different workflows without writing code.

This is the same relationship as Unix programs and shell scripts. A shell script is a composition of programs. It can call other shell scripts. It doesn't stop being composable just because it has internals.

---

### 17. Pipeline execution model supports parallel, loop, and cycle

**Decision:** The plan data model supports four execution patterns: sequential, parallel, loop (retry until condition), and cycle (later stage routes back to earlier stage with accumulated context). The runtime implements all four.

**Execution patterns:**

- **Sequential** — default. Pipes run one after another.
- **Parallel** — multiple pipes run concurrently, all envelopes available to next stage.
- **Loop** — a pipe runs, a condition is checked, retries up to N times with failure context carried forward.
- **Cycle** — a later stage feeds findings back to an earlier stage. Outer retry up to N full cycles.

**Rationale:** A feature development workflow requires all four: parallel setup (worktree + codebase study), inner retry loop (build → verify → fix), and outer cycle (review failure feeds back to build). Designing for sequential only would require rearchitecting when complex workflows arrive. The patterns are well-understood concurrency primitives.

---

## Open Questions

### A. Distribution model

**Options:**

1. **Single binary** — server, CLI client, and (eventually) embedded web client in one binary. Simplest for users. One install, everything works.
2. **Separate packages** — server and clients are independent binaries. More flexible for development and packaging. Users install what they need.
3. **Hybrid** — single binary for v0.1.0 (server + CLI), separate packages later when more clients exist.

**Leaning:** No strong leaning yet.

---

### B. Memory storage

**Options:**

1. **Filesystem markdown → SQLite FTS5 → RAG** — start simple (markdown files, substring search), migrate to SQLite when the store hits ~1000 entries, add embeddings later for semantic search.
2. **SQLite from day one** — skip the filesystem stage. SQLite is still a single file, still local, still simple. FTS5 gives real search immediately.
3. **Something else** — a different storage model entirely.

**Considerations:** Filesystem markdown is the simplest possible start and is human-readable. But it caps out quickly on search. SQLite FTS5 is barely more complex to implement and dramatically better at retrieval. The question is whether the filesystem stage has enough value to justify the later migration.

---

### C. Envelope serialization

**Decision:** The contract is defined (see #12). The serialization format is explicitly deferred.

**When this needs to be decided:** When the first cross-process pipe communication is implemented. In-process Go pipes can pass structs. The moment an envelope crosses a process boundary (CLI pipe mode, API transport, file storage), a format must be chosen.

**Likely candidates:** JSON for API/wire, YAML frontmatter for file storage and human readability. But this should be driven by what feels right during implementation, not decided in advance.

---

### D. CLI tool invocation specifics

**Context:** Non-deterministic pipes may invoke AI via CLI subprocess. Each CLI tool has its own flags, prompt passing conventions, and authentication. Unknown what each supports (system prompt flags, context file flags, streaming behavior).

**Resolution:** Spike each CLI tool before building the bridge for it. The bridge abstraction should normalize these differences so pipes don't care which provider or invocation method is being used.

---

### E. Server lifecycle management

**Context:** The CLI auto-starts the server if it's not running. But what about: graceful shutdown, server updates while pipes are executing, multiple users on the same machine, crash recovery.

**Options:**

1. **Simple daemon** — CLI starts it, pidfile tracks it, `virgil stop` kills it. Crash = restart.
2. **Supervised process** — systemd/launchd manages lifecycle. Auto-restart on crash. Log rotation handled by OS.
3. **Decide later** — start with the simple daemon, add supervision when stability matters.

---

### F. Authentication and remote access

**Context:** v0.1.0 is local only. The server listens on localhost. No authentication needed when only local processes connect.

**When this matters:** The moment the server is accessible from another machine. Tailscale was discussed as an option — it provides encrypted tunnels with identity, effectively solving auth and transport in one layer.

**Decision deferred until remote access becomes a priority.**
