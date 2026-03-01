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

#### What the runtime tracks

When an envelope transitions between pipes, the runtime records an **outcome signal** — how the downstream consumer received the output. These signals accumulate into per-pipe KPIs.

The runtime captures three categories of signal:

**Downstream pipe signals** — automatic, no configuration required:
- Did the next pipe in the chain succeed or fail?
- Did a verify/review pipe accept or reject the output?
- How many loop iterations were needed before acceptance?
- Did the output require transformation by an intermediate pipe before the next pipe could use it?

**User signals** — captured from explicit user actions:
- Did the user modify the output before accepting? (modification rate)
- Did the user reject the output entirely? (rejection rate)
- Did the user accept without changes? (clean acceptance rate)
- Did the user override a flag or provider for this pipe? (configuration miss)

**System signals** — derived from execution metadata already in the envelope:
- Execution duration (per-pipe and trending over time)
- Error rate
- Provider latency and cost (for non-deterministic pipes)
- Retry depth within loops

#### How KPIs are defined

Every pipe declares its KPIs in configuration. The runtime provides defaults that cover common cases — most pipes care about acceptance rate, error rate, and duration. Pipes can override or extend with domain-specific KPIs.

```yaml
# Default KPIs applied to all pipes unless overridden
defaults:
  metrics:
    - name: acceptance_rate
      signal: downstream_accept / downstream_total
      window: 7d
      threshold:
        warn: 0.85
        degrade: 0.70

    - name: error_rate
      signal: error_count / invocation_count
      window: 7d
      threshold:
        warn: 0.05
        degrade: 0.15

    - name: duration_p95
      signal: duration.p95
      window: 7d
      # No threshold — informational by default, pipe overrides if latency matters

# Per-pipe overrides
pipes:
  builder:
    metrics:
      - name: first_pass_verify_rate
        signal: verify_pass_first_attempt / verify_total
        window: 7d
        threshold:
          warn: 0.60
          degrade: 0.40

      - name: human_modification_rate
        signal: user_modified / user_reviewed
        window: 7d
        threshold:
          warn: 0.40    # user changes output >40% of the time
          degrade: 0.60

      - name: fix_loop_depth
        signal: avg(loop_iterations)
        window: 7d
        threshold:
          warn: 3
          degrade: 5

  draft:
    metrics:
      - name: edit_rate
        signal: user_modified / user_reviewed
        window: 7d
        threshold:
          warn: 0.30
          degrade: 0.50

  router:
    metrics:
      - name: miss_rate
        signal: ai_fallback_count / total_routes
        window: 7d
        threshold:
          warn: 0.20
          degrade: 0.35
```

#### The improvement loop

When a KPI crosses a threshold, the system generates a signal — the same kind of signal that drives every other action in Virgil. The improvement is itself a pipeline:

```
Signal:   pipe.builder.first_pass_verify_rate < 0.60 (warn)
Intent:   self-improve(pipe=builder)
Plan:     metrics.retrieve(pipe=builder, window=7d)
          | analyze(type=failure_patterns, group_by=task_category)
          | improve.propose(target=builder.config)
Output:   proposed configuration change + summary
```

The analyze step examines the failure envelopes — the actual content of what went wrong. It clusters failures by pattern. "Builder consistently mishandles database migration files" is a concrete finding that produces a concrete prompt amendment: add migration-specific context to the builder's prompt when the task involves schema changes.

Proposed improvements fall into two categories:

**Auto-apply** — changes that are safe, narrow, and reversible:
- Adding context to a prompt template
- Adjusting a flag default
- Adding a keyword to the router index
- Updating a trigger phrase

These are applied automatically and logged. The next improvement cycle measures whether the KPI improved. If it didn't, or if it degraded another KPI, the change is rolled back.

**Advisory** — changes that require judgment:
- Switching a pipe's AI provider
- Restructuring a pipeline's execution order
- Creating a new pipe to handle a class of tasks the existing pipe struggles with
- Adjusting trust boundaries (auto-merge thresholds, review requirements)

These are surfaced as proposals. The user reviews and approves or dismisses.

The boundary between auto-apply and advisory is itself configurable — the trust gradient applied to self-improvement. A user who wants full control sets everything to advisory. A user who trusts the system loosens the leash on specific categories.

#### Rollback

Every auto-applied change is versioned. The system tracks which configuration was active when each KPI measurement was taken. If a KPI degrades after a change, the system can correlate the degradation with the specific change and propose a rollback — or auto-rollback if configured to do so.

```
Change applied:  builder prompt amendment (migration context)  — v47
KPI before:      first_pass_verify_rate = 0.58
KPI after (3d):  first_pass_verify_rate = 0.62  ✓ improvement
KPI after (3d):  duration_p95 = +400ms           ⚠ regression (prompt is longer)
```

The system sees both effects. The improvement worked but introduced a latency cost. It surfaces both findings. The user decides whether the tradeoff is acceptable.

#### Goodhart's law protection

A pipe optimizing toward a single metric can degrade in ways the metric doesn't capture. The primary defense is the multi-KPI design — a pipe is never evaluated on one number. The builder doesn't just track first-pass verify rate. It also tracks human modification rate, fix loop depth, and duration. A change that games the verify step (writing overly defensive code that passes tests but is unmaintainable) will show up as an increase in human modification rate when the user starts editing the output more.

The secondary defense is the advisory boundary. Structural changes — the kind most likely to produce Goodhart effects — require human approval. Only narrow, additive changes (prompt amendments, keyword additions) are auto-applied.

The tertiary defense is periodic user review. The system can generate a metrics summary on request or on schedule. "Here's how each pipe is performing this week. Here's what changed. Here's what I'd propose next." The user stays in the loop without needing to monitor continuously.

#### Hierarchical summarization

The metrics storage strategy is hierarchical summarization — a compression strategy. The raw events are the leaves, and each level up the tree trades granularity for searchability.

The raw JSONL stays as-is — append-only, one line per event. But periodically, a summarization step runs and produces a higher-level record that captures the patterns without the individual events.

```
Raw events (JSONL)
    │  every hour
    ▼
Hourly summaries
    │  every day
    ▼
Daily summaries
    │  every week
    ▼
Weekly summaries
    │  every month
    ▼
Monthly summaries
```

An hourly summary for the builder pipe might look like:

```jsonl
{"level":"hour","pipe":"builder","window":"2026-03-01T14:00:00Z/PT1H","invocations":3,"verify_first_pass":2,"verify_failures":1,"avg_fix_loops":1.3,"user_accepted":2,"user_modified":1,"categories":["camp-registration","payment-flow"],"notable":"payment-flow task failed verify 3x on stripe webhook handling"}
```

The `notable` field is the key insight. At the raw level, you have dozens of individual events. The hourly summary collapses them into counts and rates. But it also captures *what was interesting* — the outliers, the repeated failures, the patterns that a flat aggregation would hide. That notable field is what the improvement pipeline's analyze step actually cares about. It doesn't need to see that the builder ran 3 times. It needs to know that Stripe webhook handling failed repeatedly.

Each level up compresses further. The daily summary aggregates the hourly summaries. The weekly summary aggregates the dailies. By the time you're at the monthly level, you have something like:

```jsonl
{"level":"month","pipe":"builder","window":"2026-03-01T00:00:00Z/P1M","invocations":847,"first_pass_rate":0.72,"modification_rate":0.18,"trending":"improving","notable":["stripe integration remains weakest category","migration tasks improved after prompt amendment v52","provider swap on mar-15 improved code quality but added ~200ms latency"]}
```

That monthly summary is a paragraph's worth of structured insight about the builder's performance across hundreds of invocations. An AI analyzing it doesn't need to scan 847 raw events. It reads one record and has the full picture.

The tree structure is what makes this searchable at scale. When the improvement pipeline fires, it doesn't start at the raw events. It starts at the highest relevant level:

"How's the builder doing?" → read the latest weekly summary. Done.

"The builder's verify rate dropped this week — what happened?" → read this week's daily summaries. Find the day it dropped. Read that day's hourly summaries. Find the hour. Now, and only then, read the raw events for that hour.

It's the same layered resolution strategy as the router. Start broad, narrow only when needed. Most improvement analysis never touches the raw events. It works from summaries. Only when it needs to understand a specific failure pattern does it drill down.

The summarization step itself is a pipeline — and here's where it gets interesting. The hourly and daily summaries can be purely deterministic. They're counts, rates, percentiles — just math over the raw events. No AI needed. But the `notable` field at higher levels — weekly, monthly — benefits from AI. Detecting that "Stripe integration is the weakest category" from a week of daily summaries is a synthesis task. So the summarization pipeline uses deterministic aggregation for the numbers and an AI call for the narrative. The AI surface area is small and bounded — one call per summary level per period.

And the summaries are themselves JSONL. Same format, same tooling, same rotation policy. You just have multiple files:

```
metrics/
  raw/
    2026-03-01.jsonl
    2026-03-02.jsonl
  hourly/
    2026-03-01.jsonl    # 24 summary lines
  daily/
    2026-03.jsonl       # ~30 lines for the month
  weekly/
    2026.jsonl          # ~52 lines for the year
  monthly/
    all.jsonl           # one line per month, forever
```

Raw files rotate out after the retention window — 30 days, 90 days, whatever. But the summaries are tiny and can stay forever. The entire system's performance history for a year fits in a few hundred kilobytes of summary JSONL. The raw events are ephemeral. The summaries are the institutional memory.

This also solves the Goodhart problem more elegantly. When the improvement pipeline analyzes a KPI degradation, it doesn't just see the number drop. It reads the summary tree and gets narrative context about *what changed around the time it dropped*. "First pass verify rate declined from 0.74 to 0.61 during the week of March 8. Coincides with prompt amendment v52 being applied on March 7. The amendment targeted migration tasks specifically, which improved, but non-migration tasks showed increased failure rates." That's a rollback recommendation with reasoning, derived entirely from the summary tree.

The retention strategy is naturally proportional too. Maximum detail for the recent past (where you're most likely to need it for debugging), moderate detail for the medium term (where you need it for trend detection), and compressed insight for the long term (where you need it for strategic assessment of how the system evolves). Exactly how a human memory works — vivid detail about yesterday, general patterns about last month, broad strokes about last year.

---

### 19. Nightly upgrade cycle for external intelligence

**Decision:** Virgil runs a scheduled pipeline that monitors external sources (provider changelogs, model releases, relevant research) and produces a summary of findings with actionable proposals. Configuration-level changes (provider versions, API adjustments) can be auto-applied. Architectural proposals are advisory only.

**Rationale:** The internal self-improvement loop (decision #18) optimizes how Virgil uses its current capabilities. The external upgrade cycle tracks whether better capabilities exist. These are complementary but distinct: one tightens the feedback loops, the other expands the frontier.

#### What it monitors

Sources are configured, not hardcoded. The default set covers:

- **Provider changelogs** — Anthropic, OpenAI, Google, and any other configured providers. New model releases, API changes, deprecation notices.
- **Benchmark results** — public model comparisons relevant to tasks Virgil performs (code generation, summarization, research).
- **Dependency updates** — Go modules, CLI tool versions, API client libraries.

Additional sources (specific researchers, arxiv feeds, blog aggregators) are user-configured.

#### What it does with findings

Findings are classified by actionability:

**Auto-apply:**
- A configured CLI tool released a new version with no breaking changes → update and verify
- A provider deprecated an API endpoint Virgil uses → update the endpoint in configuration
- A new model is available from an already-configured provider → benchmark against current default on a representative task suite, propose swap if it outperforms

**Advisory:**
- A new model significantly outperforms the current default on a task category → "Anthropic released Claude X. It benchmarks 30% better on code tasks. Want me to run a comparison against your builder pipe's current provider?"
- A research paper describes a technique relevant to Virgil's architecture → summary + assessment of where it applies + what integration would look like
- A breaking API change requires code-level updates → notification with details

#### Schedule and notification

The upgrade pipeline runs on a configurable schedule (default: nightly). Results are stored and surfaced in the morning summary or on request:

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

---

### G. Metrics storage and retention

**Context:** Per-pipe KPIs need historical data to compute trends, detect degradation, and correlate changes with outcomes. The volume depends on how many pipes exist and how granular the tracking is.

**Options:**

1. **Extend the existing storage choice (see question B)** — metrics go wherever memory goes. If that's SQLite, metrics are a table. Simple, one storage system to manage.
2. **Separate time-series storage** — metrics have different access patterns than memory (append-heavy, range queries, aggregations). A purpose-built store (even just a separate SQLite database) might be cleaner.
3. **Flat log files with periodic aggregation** — raw signals go to append-only logs. A periodic job aggregates them into KPI summaries. Simple to start, but querying raw logs for correlation is slow.

**Considerations:** The volume is low in early stages (few pipes, few invocations per day). Any of these options work at first. The question is which one will still work when there are 50+ pipes running hundreds of invocations daily with 90 days of history.

---

### H. KPI definition language

**Context:** The `signal` field in KPI definitions (e.g., `downstream_accept / downstream_total`) needs a small expression language. It doesn't need to be Turing-complete — it needs arithmetic over named counters, basic aggregations (avg, p95, count), and time windowing.

**Options:**

1. **Custom DSL** — minimal expression parser. Clean syntax, easy to validate, but another thing to build and maintain.
2. **Go expressions evaluated at runtime** — powerful but potentially dangerous and hard to sandbox.
3. **Predefined KPI types with parameters** — instead of a language, offer a fixed set of KPI computations (`ratio`, `average`, `percentile`, `count`) with configurable inputs. Less flexible but zero parsing.

**Leaning:** Option 3 for v0.1.0. The KPI patterns are predictable enough that parameterized types cover the common cases. A DSL can come later if the fixed types prove too limiting.
