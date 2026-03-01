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

### 18. Metrics and self-improvement are infrastructure, not a pipe

**Decision:** The runtime tracks per-pipe performance metrics automatically, the same way it tracks logging. Every pipe has KPIs derived from how its output is received by downstream pipes and by the user. The self-healing pattern — already established for the router — generalizes to all pipes. Improvement proposals are configuration changes: prompt amendments, flag defaults, provider swaps, threshold adjustments.

**Rationale:** The router self-healing loop (decision #9) works because the feedback signal is tight and the improvements land in configuration. The same architecture applies to any pipe. A builder pipe that consistently fails verification on a certain class of task has a measurable problem with a concrete improvement path — a prompt amendment, a context addition, a provider change. Making metrics infrastructure means every pipe gets this loop for free, without opting in.

**What the runtime tracks:**

The runtime captures three categories of signal at every envelope transition:

- **Downstream pipe signals** (automatic) — did the next pipe succeed or fail? Did a verify/review pipe accept or reject? How many loop iterations before acceptance?
- **User signals** (from explicit actions) — did the user modify, reject, or accept without changes? Did they override a flag or provider?
- **System signals** (from envelope metadata) — execution duration, error rate, provider latency and cost, retry depth within loops.

**KPI configuration:**

Every pipe declares its KPIs in configuration. The runtime provides defaults (acceptance rate, error rate, duration). Pipes can override or extend with domain-specific KPIs. Each KPI specifies a measurement, a time window, and thresholds for warning and degradation.

**The improvement loop:**

When a KPI crosses a threshold, it generates a signal — the same kind that drives every other action in Virgil:

```
Signal:   pipe.builder.first_pass_verify_rate < 0.60 (warn)
Intent:   self-improve(pipe=builder)
Plan:     metrics.retrieve(pipe=builder, window=7d)
          | analyze(type=failure_patterns, group_by=task_category)
          | improve.propose(target=builder.config)
Output:   proposed configuration change + summary
```

Proposed improvements fall into two categories:

- **Auto-apply** — safe, narrow, reversible changes: prompt context additions, flag defaults, keyword additions, trigger phrases. Applied automatically, measured, rolled back if KPIs degrade.
- **Advisory** — changes requiring judgment: provider swaps, pipeline restructuring, new pipe creation, trust boundary adjustments. Surfaced as proposals for user review.

The boundary between auto-apply and advisory is configurable — the trust gradient the user applies to self-improvement.

**Rollback:**

Every auto-applied change is versioned. The system tracks which configuration was active when each KPI measurement was taken. If a KPI degrades after a change, the system correlates the degradation with the specific change and proposes (or auto-executes) a rollback.

**Goodhart's law protection:**

Multi-KPI design is the primary defense — a pipe is never evaluated on one number. A builder that games the verify step by writing overly defensive code will show up as increased human modification rate. Structural changes require human approval (advisory boundary). Periodic metrics summaries keep the user in the loop without requiring continuous monitoring.

---

### 19. Nightly upgrade cycle for external intelligence

**Decision:** Virgil runs a scheduled pipeline that monitors external sources (provider changelogs, model releases, relevant research) and produces a summary of findings with actionable proposals. Configuration-level changes (provider versions, API adjustments) can be auto-applied. Architectural proposals are advisory only.

**Rationale:** The internal self-improvement loop (decision #18) optimizes how Virgil uses its current capabilities. The external upgrade cycle tracks whether better capabilities exist. These are complementary but distinct: one tightens the feedback loops, the other expands the frontier.

**What it monitors** (configured, not hardcoded):

- Provider changelogs — new model releases, API changes, deprecation notices
- Benchmark results — public model comparisons relevant to tasks Virgil performs
- Dependency updates — CLI tool versions, API client libraries
- User-configured feeds — specific researchers, arxiv feeds, blog aggregators

**Findings are classified by actionability:**

- **Auto-apply** — CLI tool update with no breaking changes, deprecated API endpoint replacement, new model benchmarked and outperforming current default
- **Advisory** — significant model improvements worth evaluating, relevant research techniques, breaking changes requiring code-level updates

**Schedule:** Configurable, default nightly. Results surfaced in morning summary or on request:

```
"Overnight: applied 2 dependency updates, both passing. Anthropic
 shipped a new model — benchmarks suggest it's stronger for code
 review. Want me to run a comparison?"
```

The pipeline is the same system all the way down:

```
Signal:   schedule.nightly (ambient signal)
Intent:   self-upgrade
Plan:     sources.fetch(configured_feeds)
          | analyze(type=relevance, context=current_config)
          | parallel:
              auto_apply(safe_changes)
              propose(advisory_changes)
          | summarize(type=voice)
Output:   applied changes log + advisory proposals + voice summary
```

---

### 20. Metrics storage uses append-only logs with hierarchical summarization

**Decision:** Raw metric events are stored as append-only log files, one event per line, rotated daily. A summarization pipeline periodically compresses raw events into higher-level summaries — hourly, daily, weekly, monthly — forming a tree that trades granularity for searchability at each level.

**Rationale:** Metrics are append-heavy and read-infrequently. An append-only log is the simplest possible storage — no database, no schema, no connection overhead. Standard Unix tools work on the files directly. The problem with flat logs at scale is querying. Hierarchical summarization solves this without introducing a database.

**The summary tree:**

```
Raw events (append-only, one per envelope transition)
    │  every hour → deterministic aggregation
    ▼
Hourly summaries
    │  every day → deterministic aggregation
    ▼
Daily summaries
    │  every week → deterministic aggregation + AI for narrative
    ▼
Weekly summaries
    │  every month → deterministic aggregation + AI for narrative
    ▼
Monthly summaries
```

Hourly and daily summaries are purely deterministic — counts, rates, percentiles. No AI needed. Weekly and monthly summaries add a small AI call to synthesize narrative insight ("Stripe integration remains the weakest category", "provider swap on March 15 improved quality but added latency"). The AI surface area is bounded — one call per summary level per period.

**Layered resolution** (same pattern as the router):

1. "How's the builder doing?" → read the latest weekly summary. Done.
2. "The verify rate dropped — what happened?" → read daily summaries. Find the day.
3. "What went wrong Tuesday?" → read Tuesday's hourly summaries. Find the hour.
4. "Show me the failures." → now, and only then, read the raw events.

Most analysis never touches raw events. It works from summaries.

**Retention:**

- Raw events: 30 days (configurable). Maximum detail for the recent past.
- Hourly summaries: 90 days.
- Daily and above: kept indefinitely. A year of daily summaries for all pipes fits in kilobytes.

The system remembers its own performance history at the daily level forever, while keeping storage bounded. Old raw files are deleted on rotation. Summaries persist.

**Summarization is a pipeline** — expressed in the standard signal → classify → plan → execute loop. The hourly, daily, weekly, and monthly pipelines each read from the level below. Not a special case.

---

### 21. Single continuous stream UX, stateless invocations behind the scenes

**Decision:** The user sees one continuous conversation — a single scrolling stream of inputs and responses. Behind the scenes, every message is a fresh, stateless invocation. There is no conversation state passed between messages. Continuity comes from memory retrieval, not from chat history.

**Rationale:** The stream UX feels like talking to one person. No "chats" to create, no sessions to manage, no context windows to worry about. But making each invocation stateless keeps the architecture clean — the server doesn't maintain conversation threads, doesn't manage session state, doesn't deal with stale context. Memory is the single source of truth for continuity. This also means context isn't bound to a client: close the TUI, open Virgil on your phone, pick up where you left off.

When the user says "make it shorter," the router classifies it as a refinement signal, retrieves working context from memory, and produces a plan like `memory.retrieve(working_context) | draft(type=revise, instruction="shorter")`. Fresh invocation with full context.

---

### 22. Unified memory with hierarchical summarization, no separate working memory

**Decision:** Memory is one system. There is no distinct "working memory" and "long-term memory." Recent interactions are stored in full detail. Over time, hierarchical summarization condenses older context into higher-level summaries. The system can drill back into raw history when needed, but most retrievals work from summaries.

**Rationale:** A separate working memory concept would require managing lifecycle (when does working memory expire? who decides?), synchronization (what if working memory contradicts long-term memory?), and a routing decision (which memory do I check?). A single memory system with natural decay via summarization handles the full spectrum: what you said thirty seconds ago, the spec you've been refining this week, and the fact that you prefer TDD. Summarization is already established infrastructure (decision #20). Applying it to interaction memory is the same pattern.

---

### 23. Stream and detail panel for pipeline visibility

**Decision:** The primary interface has two regions. The stream is the main conversation — all inputs, responses, and pipeline start/finish notifications appear here in order. The detail panel shows the internals of a running or completed pipeline — individual stages, loop iterations, review findings. Multiple parallel pipelines are switchable in the detail panel.

**Rationale:** The stream must stay conversational. The user should keep talking to Virgil while background pipelines run. But complex pipelines have rich internal state the user may want to inspect or intervene in. The detail panel provides depth without cluttering the conversation. It's optional for simple interactions and essential for complex ones.

Pipeline progress appears in the stream as brief interleaved status lines — enough for awareness, not enough to drown the conversation. Full stage-by-stage detail lives in the panel.

---

### 24. Context is assembled per-invocation, not accumulated

**Decision:** Each invocation starts with an empty context window and fills it with exactly what it needs from memory. There is no conversation buffer, no chat history passed between messages, and nothing to compact. A fixed context budget acts as a ceiling. The planner fills it according to a retrieval strategy determined by the signal type.

**Rationale:** The compaction problem in traditional chat UIs exists because context accumulates until it overflows, then gets lossily compressed. The user feels the degradation — the AI forgets details, loses nuance. Virgil avoids this entirely by pulling context fresh each time. You never accumulate irrelevant context. You never hit a wall. The budget is a ceiling, not a constraint you grow into.

---

### 25. Hybrid context assembly — planner fills base, pipes can pull more

**Decision:** The planner assembles a base context using a strategy appropriate to the signal type. The router classification determines the retrieval pattern:

- **One-shot factual** → signal only, maybe user prefs. Minimal retrieval.
- **Refinement** → signal + working state. That's it.
- **Creative/generative** → signal + topical memory + user prefs.
- **Complex pipeline** → signal + working state + topical memory. Heavy retrieval.
- **Ambient signal** → time/location context + calendar state. No working state.

If a pipe needs something the planner didn't include, it can query memory directly, drawing from remaining budget headroom.

**Rationale:** Four approaches were evaluated. Fixed priority layers (approach A) are too rigid — the same order for every signal type is a bad fit. Pure pipe-driven retrieval (approach C) pushes too much responsibility onto individual pipes. Router-driven strategies (approach B) adapt to signal type but can't handle edge cases within a pipe's execution. The hybrid (approach D) handles 90% of context needs through the planner's upfront assembly and gives pipes an escape hatch for the other 10%.

Context strategy misclassification is a failure mode. If the router picks the wrong strategy, the pipe gets the wrong context. The self-healing loop covers this — mismatches between strategy and outcome are a measurable signal that can improve the strategy selection over time.

---

### 26. Memory writes are hybrid — automatic for working state, explicit for long-term facts

**Decision:** The runtime automatically saves working state after every invocation that produces or modifies an artifact. The signal, plan, and output are recorded as infrastructure. Long-term facts ("I prefer session tokens over JWTs") are saved only when the user or a pipe explicitly requests it.

**Rationale:** Working state is high-churn — many entries, rapidly updating, mostly relevant only during the current task. Auto-saving ensures continuity between invocations without requiring the user to think about persistence. Explicit saves for long-term facts prevent memory from filling with noise. The distinction also affects retention: automatic working state can be pruned more aggressively since the raw interaction history is still available within its retention window.

Memory uses the same hierarchical summarization as metrics (decision #20): raw entries for recent detail, daily/weekly/monthly summaries for older context. Most retrieval reads from summaries. Specific detail drills into raw entries only when the signal demands it.

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

**Considerations:** Filesystem markdown is the simplest possible start and is human-readable. But it caps out quickly on search. SQLite FTS5 is barely more complex to implement and dramatically better at retrieval. The question is whether the filesystem stage has enough value to justify the later migration. Context assembly (decisions #24-26) relies on fast, relevance-based retrieval — memory needs to return the right entries quickly based on topic, recency, and type.

---

### H. Working state and long-term memory — same store or separate?

**Context:** Decision #26 establishes that working state (auto-saved, high-churn) and long-term facts (explicit, low-churn) have different write patterns and retention characteristics. The question is whether they share storage or live in separate stores.

**Options:**

1. **One store** — simplest. Tags or metadata distinguish working state from long-term facts. One query interface, one retention system, one storage engine.
2. **Separate stores, unified retrieval** — different retention rules and storage characteristics, but the planner queries both through a single interface. Working state could be faster/more volatile storage. Long-term facts could be more durable.
3. **Decide when storage is chosen** — the answer depends on what storage engine is picked (open question B). If it's SQLite, separate tables in the same database. If it's filesystem, separate directories. Defer until then.

**Leaning:** Option 3. The decision is coupled to the storage engine choice.

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

---

### G. KPI definition language

**Context:** KPI definitions need a way to express measurements (e.g., `downstream_accept / downstream_total`). This needs arithmetic over named counters, basic aggregations (avg, p95, count), and time windowing. It doesn't need to be Turing-complete.

**Options:**

1. **Custom DSL** — minimal expression parser. Clean syntax, easy to validate, but another thing to build and maintain.
2. **Predefined KPI types with parameters** — instead of a language, offer a fixed set of KPI computations (`ratio`, `average`, `percentile`, `count`) with configurable inputs. Less flexible but zero parsing.
3. **Decide later** — hardcode the common KPIs, add configurability when the patterns stabilize.

**Leaning:** Option 2 for v0.1.0. The KPI patterns are predictable enough that parameterized types cover the common cases. A DSL can come later if the fixed types prove too limiting.
