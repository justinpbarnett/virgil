# Virgil

## What It Is

Named for Dante's guide through the _Divine Comedy_ — not the hero, but the one who knows the terrain, speaks the language of every circle, and makes sure you get where you're going. Virgil doesn't do the work. Virgil knows who does, and gets you there.

Virgil is a personal intent compiler. It takes raw signal — text, voice, ambient data — classifies what you want, decomposes it into a plan, and chains solved problems together to produce the outcome.

For specific technology choices, rationale, and open questions, see `ARCHITECTURE.md`.

Every interaction follows the same loop:

```
signal → classify → plan → execute → output
```

A person saying "draft a blog post based on my recent notes" and Virgil noticing it's Sunday morning at church are the same thing. Both are signals. Both get classified into intent. Both produce a plan. Both chain atomic operations together. The system that processes your requests and the system that improves itself are the same system.

---

## Signals

A signal is anything that carries intent. Virgil doesn't just respond to typed commands. It interprets signals from any source and decides if action is needed.

**Active signals** — the person explicitly asks for something:

- Typed text
- Voice input (transcribed)
- Slash commands

**Ambient signals** — Virgil observes context and infers intent:

- Location (arriving at church, leaving home, entering the office)
- Time (Sunday morning, end of workday, 15 minutes before a meeting)
- Biometrics (heart rate spike, sleep score, activity level)
- Calendar state (upcoming event, gap in schedule, double-booking)
- System state (router miss count, memory store size, error rates)

An active signal always produces a response. An ambient signal may or may not — it depends on whether Virgil can classify a meaningful intent from it. If it can't, it stays silent. Virgil should never be noisy.

---

## The Router

The router is the most important component. It takes a signal and decides what to do with it. Every interaction passes through the router. If the router is slow, everything feels slow. If it's wrong, nothing works.

### Deterministic First

The router is not an AI. It is a layered search engine. Deterministic code handles the common case. AI is a fallback for the rare ambiguous query. The goal is for AI to fire less and less over time as the deterministic layers learn.

```
Signal arrives
    │
    ▼
Layer 1: Exact Match
    Pattern-match against known commands, aliases, signal patterns.
    Microseconds. No computation.
    │
    ▼ (no match)
Layer 2: Keyword Index
    Look up known words against an inverted index.
    Map lookups. Microseconds.
    Identifies which category of intent this likely is.
    │
    ▼ (category identified)
Layer 3: Category Narrowing
    Score the signal against only the pipes in the matched category.
    Instead of evaluating 50+ pipes, evaluate 5-10.
    Fast string scoring. Low milliseconds.
    │
    ▼ (low confidence across all layers)
Layer 4: AI Fallback
    Send a compact summary of all available pipes to a fast model.
    Classify intent. Hundreds of milliseconds.
    Log the miss. This is how the system learns.
```

The speed target: 80%+ of queries should never leave layers 1-2. The user should never feel routing latency on common requests.

### Categories

Pipes are organized into categories. Categories exist to narrow the search space — instead of scoring against every pipe, the router identifies the category first (one or two keyword lookups), then scores within that category.

Categories are defined in a configuration file. They are auto-generated from pipe metadata at startup. The user can override to fix misclassifications, add keywords, or reorganize.

```
time/       — calendar, reminders, scheduling
memory/     — storing and recalling information
dev/        — code review, deployment, testing
comms/      — email, messaging, writing
research/   — search, analysis, fact-checking
general/    — conversation, brainstorming
```

The category tree is not fixed. New categories emerge as new pipes are added. The self-healing process can propose new categories when it detects clusters of misses that don't fit existing ones.

### The Parse Stage

When the router has classified the general intent, it parses the signal into structured components. This is rule-based NLP — scanning for known words in a configuration-driven dictionary. No language models, no dependency parsing, no statistical inference. Just map lookups against known terms:

- **Verbs** — what action to take (draft, summarize, review, deploy, recall)
- **Types** — what kind of output (blog post, email, PR description, summary)
- **Sources** — where input data comes from (notes, calendar, codebase, web)
- **Modifiers** — constraints on the data (recent, today, all, tagged)
- **Topics** — the subject matter (whatever remains after extracting the above)
- **Connectors** — structural words that indicate how components relate ("based on", "then", "and")

The vocabularies are defined in configuration. They grow over time through the self-healing process.

### The Plan Stage

With structured components extracted, the router matches against templates. There are two kinds:

**Inline templates** build a short chain of pipes on the fly:

```
{verb} {type} based on {source}  →  source.retrieve | verb(type)
{verb} and {verb₂}               →  verb.execute | verb₂.execute
{verb} {type} about {topic}      →  verb.execute(type, topic)
{verb} {source}                  →  source.retrieve | verb.execute
{verb}                           →  verb.execute
```

**Pipeline templates** route to a pre-defined pipeline — a named graph of pipes with its own execution logic:

```
add {feature} to {target}        →  dev-feature(spec=feature, target=target)
deploy {target} to {env}         →  deploy-pipeline(target=target, env=env)
```

The plan stage picks whichever template matches. Simple queries get an inline chain. Complex queries get routed to a full pipeline. The output in both cases is an execution plan — the runtime doesn't care how it was produced.

---

## Pipes

A pipe is an atomic unit of capability. It does one thing. It accepts input, produces output, and composes with other pipes.

Some pipes are deterministic — they call an API, query a database, perform a calculation. No AI involved. These are fast and predictable.

Some pipes are non-deterministic — they invoke an AI model with a specific prompt and context. The prompt defines what the pipe does. A "draft" pipe with a `--type=blog` flag is the same pipe as "draft" with `--type=email` — it's the same capability with a different prompt configuration.

Non-deterministic pipes are model-agnostic. A global default provider is configured at the project level — most pipes use it. Individual pipes can override with a different provider when the task warrants it. Code review might use one model, grounded web research another, creative writing a third. The pipe declares what it needs; the runtime resolves how to call it, whether that's a CLI subprocess or a direct API call.

The distinction between deterministic and non-deterministic pipes is an implementation detail. From the router's perspective, from the planner's perspective, and from the user's perspective, they are all just pipes. They all accept input, produce output, and compose.

### Pipe Interface

Every pipe has:

- **A name** — what it's called (`memory`, `calendar`, `draft`, `research`)
- **Triggers** — words and phrases that indicate this pipe should handle the signal
- **A category** — which category it belongs to
- **Input** — what it accepts (the envelope from the previous pipe, or the original signal)
- **Output** — what it produces (an envelope for the next pipe, or the final response)
- **Flags** — configuration that changes behavior without changing identity (`--type=blog`, `--sort=recent`, `--limit=10`)

### Examples

`memory` — a deterministic pipe. Stores and retrieves information from a local data store. Flags control whether it's storing or retrieving, how to sort, how many results.

`calendar` — a deterministic pipe. Reads and writes events from a calendar API. Flags control the time range, which calendar, what operation.

`draft` — a non-deterministic pipe. Takes input context and produces written content. The `--type` flag selects which prompt template to use: blog, email, PR description, memo. The AI model and prompt are implementation details inside the pipe.

`research` — a non-deterministic pipe. Takes a query, searches for information, synthesizes findings. Configured to use a provider with strong grounded search capabilities — a different provider than what `draft` or `code-review` might use. The pipe declares which provider it wants; the runtime handles invocation.

`code-review` — a non-deterministic pipe. Takes code as input, produces a review as output. Uses a model suited for code reasoning.

`summarize` — a non-deterministic pipe. Takes any content as input, produces a condensed version. The `--format` flag might control whether the output is a sentence, a paragraph, or bullet points.

### Composition

Pipes compose through a standard envelope. The envelope is the data that flows between pipes. It is a contract — a set of fields every pipe must produce — not a file format. How the envelope is serialized (JSON, YAML, binary, in-memory struct) depends on context and is owned by the runtime, not the pipes.

The envelope carries:

- **pipe** — which pipe produced this
- **action** — what operation was performed
- **args** — what flags were passed
- **timestamp** — when the pipe ran
- **duration** — how long it took
- **content** — the actual output (text, structured data, list, whatever the pipe produces)
- **content_type** — what kind of content (text, list, structured, binary)
- **error** — null on success, error info on failure

The `content` field is intentionally unstructured. Memory returns a list of entries. Calendar returns structured events. Draft returns prose. The envelope doesn't force them into the same shape — it wraps whatever they produce with consistent metadata. The next pipe in the chain pulls out what it needs and ignores the rest.

When the planner produces an execution plan like:

```
memory.retrieve(sort=recent, limit=10) | draft(type=blog)
```

The runtime executes `memory.retrieve`, captures its output envelope, and passes it as input to `draft`. The draft pipe receives the retrieved notes as context and produces a blog post. The user gets the blog post.

This is the Unix philosophy. Each pipe does one thing. Pipes don't know about each other. The envelope is the universal interface. New capabilities emerge from composition, not from building monolithic features.

---

## Pipelines

A pipeline is a graph of pipes. It defines an execution order with semantics that go beyond simple chaining: parallel branches, retry loops, conditional paths, and cycles. Pipelines can contain other pipelines.

From the outside, a pipeline looks like a pipe. It has a name, takes an envelope in, produces an envelope out. The router can route to a pipeline the same way it routes to a pipe. The runtime unfolds it.

```
Pipes:        memory.retrieve    build    verify    git.push    review

Pipelines:    verify-loop   = loop(verify → fix, until=pass, max=N)
              dev-feature   = spec → parallel(worktree, study, study)
                              → build → verify-loop → publish → review

From outside: dev-feature takes an envelope in, produces an envelope out
              The router doesn't know or care that it's a graph
```

### Execution Semantics

Pipelines introduce four execution patterns beyond simple sequential chaining:

**Sequential** — pipes run one after another, each receiving the previous pipe's envelope. This is the default and covers most simple queries.

```
memory.retrieve → draft → done
```

**Parallel** — multiple pipes run concurrently. Their output envelopes are all available to the next stage. Used when independent data sources need to be gathered before work begins.

```
parallel(worktree.create, codebase.study(builder), codebase.study(reviewer))
    → all three envelopes available downstream
```

**Loop** — a pipe runs, a condition is checked, the pipe runs again if it fails. The failure envelope carries forward so each retry has context about what was already tried.

```
loop(max=N):
    build → verify
    if fail: fix → verify
    if pass: break
```

**Cycle** — a later stage routes back to an earlier stage with accumulated context. Different from a loop because it spans multiple stages and carries findings from the later stage back as new input.

```
build → verify → publish → review
    ↑                         │
    └── findings fed back ────┘
```

### Configuration

Pipelines are defined in configuration, not in code. A user defines a pipeline by declaring which pipes run in what order with what execution semantics. The same pipes can be arranged into different pipelines for different workflows.

Your dev workflow with a builder and a separate reviewer is a pipeline definition. Someone else might define a different dev pipeline — no review stage, or review before publish, or three parallel reviewers. The pipes are the same atoms. The graph is different.

### Nesting

A pipeline can reference another pipeline as a step. The verify/fix retry loop is itself a pipeline. The `dev-feature` pipeline includes it as a step. This means complex workflows compose from smaller, reusable workflow fragments — the same principle as pipes composing into pipelines, applied recursively.

```
verify-fix-loop:
    loop: build → verify → fix
    until: pass
    max: 5

dev-feature:
    steps:
        - spec.generate
        - parallel: [worktree.create, codebase.study(builder), codebase.study(reviewer)]
        - verify-fix-loop          ← a pipeline used as a step
        - publish
        - review
    cycle:
        from: review(fail)
        to: verify-fix-loop
        carry: findings
        max: 3
```

---

## Self-Healing

Every time the AI fallback fires, it means the deterministic layers failed. Virgil logs the miss:

- What signal came in
- What keywords were (or weren't) found
- What the AI decided
- How confident it was

These logs accumulate. When they hit a threshold — or when the user explicitly asks — Virgil analyzes them and proposes improvements to its own configuration:

- New keywords to add to the index
- New trigger phrases for existing pipes
- New categories to create
- Reorganization of pipes between categories

The improvements are to configuration files, not to code. They are inspectable, editable, and reversible. Once applied, every future query that would have hit the AI fallback now resolves deterministically in microseconds.

### Self-Healing as Signal

The self-healing process is itself a signal-plan-execute loop:

```
Signal:  router_misses > threshold + time_since_last_upgrade > threshold
Intent:  self-optimize
Plan:    router-log.retrieve | analyze(type=patterns) | router.upgrade
Output:  updated configuration, voice summary of what changed
```

This is not a special case. It's the same system that handles every other request. The router processes it, the planner decomposes it, the pipes execute it. Virgil improving itself is just another pipeline.

The feedback loop:

```
Query → miss → log → accumulate → threshold → analyze → upgrade → resolve
                                                                      │
                         next identical query ◄────────────────────────┘
                         resolves deterministically
```

Over time, the AI fallback fires less and less. The system gets faster. The common case gets cheaper. The deterministic layers absorb what the AI taught them. AI is the teacher, not the engine.

---

## Observability

Logging is not a pipe. It's infrastructure. In Unix, you don't `ls | log | grep` — the shell traces commands, the OS has syslog, individual programs write to stderr. Logging is what the runtime provides, not a step you chain into a pipeline.

The runtime observes every envelope transition automatically. Every time a pipe produces output and the envelope moves to the next step, the runtime records it. No pipe opts in. No pipeline includes a logging step. It just happens.

### Levels

Verbosity is configured globally and overridden per-pipe:

```
silent  — nothing (production, privacy mode)
error   — only failures
info    — pipe invocations, routing decisions, timing
debug   — full envelopes between pipes, parsed components, plan details
trace   — everything, including prompt content sent to AI models
```

A global default covers everything. Per-pipe overrides let you turn up one pipe without drowning in noise from the rest. You're debugging the draft pipe — set it to `debug`. Everything else stays at `info`.

### What Gets Logged

For a pipeline like `memory.retrieve(sort=recent, limit=10) | draft(type=blog)`:

At **info**, the runtime records: the plan that was built, the timing and summary of each pipe, and the total pipeline duration. One line per pipe. Enough to know what happened.

At **debug**, the runtime additionally records: the full input and output envelope of each pipe, the parsed signal components, the routing layer that resolved the query, and the template that built the plan.

At **trace**, the runtime additionally records: the full prompt content sent to AI models, the raw model response, and any internal pipe state. This level exists for debugging non-deterministic pipes when their output is wrong and you need to see exactly what they were asked.

### Distinction from the Miss Log

The router's miss log is not general logging. General logging is observability — it tells you what happened. The miss log is a learning signal — it tells Virgil what to improve. They happen to both write to files, but they serve different functions. The miss log feeds the self-healing loop. General logs feed the person debugging a problem.

---

## Voice

Voice is an output modality, not a feature. Any pipe's output can be spoken. Voice serves as ambient awareness — it tells you the conclusion without requiring you to look at a screen.

Voice output follows one rule: **one sentence or shorter**. Under ten words when possible. Voice is a notification, not a report. If the answer requires more than a sentence, voice gives the summary and the full output goes to the screen.

```
"You have three meetings today, first one's at 10."
"Blog draft is ready."
"Deployed to staging, all tests passed."
"I learned 14 new patterns."
```

Voice input follows the same path as text input. It's transcribed into text and becomes a signal. The router doesn't know or care whether the input was typed or spoken.

---

## Access

Virgil runs as a server. Clients connect to it. The server handles all logic — routing, planning, pipe execution, memory, voice generation. Clients are thin renderers that display output and capture input.

The primary client is a terminal UI with three modes: interactive TUI for rich sessions, one-shot for quick queries, and pipe mode for composing with other Unix tools via stdin/stdout. The terminal is always available and composes naturally with existing workflows. The CLI auto-starts the server if it's not running — the user never thinks about server lifecycle.

A web client, desktop app, and mobile client are all possible because the architecture is client-server. They connect to the same server via the same API and get the same capabilities. But they come later — the TUI covers the primary use case first. Building more clients before the server, router, and pipes are solid would split focus.

Multiple clients can be connected simultaneously. Virgil speaking a voice alert while you're reading the full output on screen is a multi-client interaction — the voice client and the visual client are both receiving output from the same pipeline execution.

---

## Metrics

Metrics are infrastructure, not a pipe. The runtime tracks per-pipe performance automatically — the same way it tracks logging. Every pipe has KPIs derived from how its output is received by downstream pipes and by the user.

The self-healing pattern — already established for the router — generalizes to all pipes. A builder pipe that consistently fails verification on a certain class of task has a measurable problem with a concrete improvement path: a prompt amendment, a context addition, a provider change. Making metrics infrastructure means every pipe gets this loop for free, without opting in.

### What Gets Tracked

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

### KPIs

Every pipe declares its KPIs in configuration. The runtime provides defaults that cover common cases — most pipes care about acceptance rate, error rate, and duration. Pipes can override or extend with domain-specific KPIs.

A builder pipe might track first-pass verify rate, human modification rate, and fix loop depth. A draft pipe might track edit rate. The router tracks miss rate. Each pipe's KPIs reflect what matters for that pipe's job.

### The Improvement Loop

When a KPI crosses a threshold, the system generates a signal — the same kind of signal that drives every other action in Virgil. The improvement is itself a pipeline:

```
Signal:   pipe.builder.first_pass_verify_rate < 0.60 (warn)
Intent:   self-improve(pipe=builder)
Plan:     metrics.retrieve(pipe=builder, window=7d)
          | analyze(type=failure_patterns, group_by=task_category)
          | improve.propose(target=builder.config)
Output:   proposed configuration change + summary
```

The analyze step examines the failure envelopes — the actual content of what went wrong. It clusters failures by pattern. "Builder consistently mishandles database migration files" is a concrete finding that produces a concrete prompt amendment.

Proposed improvements fall into two categories:

**Auto-apply** — changes that are safe, narrow, and reversible: adding context to a prompt template, adjusting a flag default, adding a keyword to the router index, updating a trigger phrase. These are applied automatically and logged. If the KPI doesn't improve, or if another KPI degrades, the change is rolled back.

**Advisory** — changes that require judgment: switching a pipe's AI provider, restructuring a pipeline's execution order, creating a new pipe. These are surfaced as proposals. The user reviews and approves or dismisses.

The boundary between auto-apply and advisory is configurable — the trust gradient applied to self-improvement.

### Rollback

Every auto-applied change is versioned. The system tracks which configuration was active when each KPI measurement was taken. If a KPI degrades after a change, the system correlates the degradation with the specific change and proposes a rollback — or auto-rollbacks if configured to do so.

### Goodhart's Law Protection

A pipe optimizing toward a single metric can degrade in ways the metric doesn't capture. The primary defense is multi-KPI design — a pipe is never evaluated on one number. A change that games the verify step will show up as an increase in human modification rate. The secondary defense is the advisory boundary — structural changes require human approval. The tertiary defense is periodic user review — a metrics summary on request or on schedule.

### Hierarchical Summarization

The metrics storage strategy is hierarchical summarization — a compression strategy. The raw events are the leaves, and each level up the tree trades granularity for searchability.

The raw JSONL stays as-is — append-only, one line per event. Periodically, a summarization step runs and produces a higher-level record that captures the patterns without the individual events.

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

An hourly summary collapses dozens of events into counts and rates. But it also captures *what was interesting* — the outliers, the repeated failures, the patterns that a flat aggregation would hide. A `notable` field carries the insight the improvement pipeline actually cares about. It doesn't need to see that the builder ran 3 times. It needs to know that Stripe webhook handling failed repeatedly.

Each level up compresses further. By the monthly level, a single record contains a paragraph's worth of structured insight about a pipe's performance across hundreds of invocations. An AI analyzing it reads one record and has the full picture.

The tree structure makes this searchable at scale. When the improvement pipeline fires, it starts at the highest relevant level:

"How's the builder doing?" → read the latest weekly summary. Done.

"The builder's verify rate dropped this week — what happened?" → read daily summaries. Find the day it dropped. Read hourly summaries. Find the hour. Now, and only then, read the raw events for that hour.

It's the same layered resolution strategy as the router. Start broad, narrow only when needed. Most improvement analysis never touches the raw events.

The summarization step itself is a pipeline. Hourly and daily summaries are purely deterministic — counts, rates, percentiles, just math. But the `notable` field at higher levels — weekly, monthly — benefits from AI. Detecting that "Stripe integration is the weakest category" from a week of daily summaries is a synthesis task. Deterministic aggregation for the numbers, an AI call for the narrative. The AI surface area is small and bounded.

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

Raw files rotate out after the retention window. The summaries are tiny and stay forever. The entire system's performance history for a year fits in a few hundred kilobytes of summary JSONL. The raw events are ephemeral. The summaries are the institutional memory.

This also strengthens the Goodhart protection. When the improvement pipeline analyzes a KPI degradation, it reads the summary tree and gets narrative context about *what changed around the time it dropped*. That's a rollback recommendation with reasoning, derived entirely from the summary tree.

The retention strategy is naturally proportional. Maximum detail for the recent past (debugging), moderate detail for the medium term (trend detection), compressed insight for the long term (strategic assessment). Exactly how a human memory works — vivid detail about yesterday, general patterns about last month, broad strokes about last year.

---

## Nightly Upgrade

The internal improvement loop optimizes how Virgil uses its current capabilities. The nightly upgrade cycle tracks whether better capabilities exist. These are complementary but distinct: one tightens the feedback loops, the other expands the frontier.

Virgil runs a scheduled pipeline that monitors external sources — provider changelogs, model releases, dependency updates, relevant research — and produces a summary of findings with actionable proposals.

**Auto-apply** — configuration-level changes that are safe and verifiable:
- A CLI tool released a new version with no breaking changes → update and verify
- A provider deprecated an API endpoint → update the endpoint in configuration
- A new model is available → benchmark against current default, propose swap if it outperforms

**Advisory** — changes that require judgment:
- A new model significantly outperforms the current default on a task category
- A research paper describes a relevant technique
- A breaking API change requires code-level updates

Results are stored and surfaced in the morning summary or on request:

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

## Principles

**Deterministic first, AI as fallback.** The common case should never require an AI call. AI is expensive, slow, and non-deterministic. Use it where it's needed — creative work, ambiguous classification, synthesis — and keep it away from routing, retrieval, and orchestration.

**The system improves deterministically.** When AI teaches the system something, that knowledge becomes a configuration change. The next time, the system doesn't need AI. Over time, the AI surface area shrinks.

**Pipes do one thing. Pipelines compose them.** Every capability is atomic. Complex behavior comes from composing pipes into pipelines — graphs with parallel branches, retry loops, and cycles. A pipeline is itself a pipe from the outside: envelope in, envelope out. Composition is recursive.

**Signals, not commands.** Input is not limited to text. Anything that carries intent is a signal. The router accepts all signals through the same classification pipeline.

**Same system all the way down.** User requests, ambient triggers, and self-improvement all flow through the same signal → classify → plan → execute loop. There are no special cases.

**Proportional complexity.** Simple queries get simple treatment. "What's on my calendar" should resolve in microseconds with a direct API call. "Draft a blog post based on my recent notes about church tech" should decompose into a multi-step pipeline. The system scales its effort to match the query's actual complexity.

**Inspectable and reversible.** Configuration is in readable files. Routes are deterministic and testable. The user can see exactly why a signal was classified the way it was, and change it.

**Model-agnostic.** No pipe is married to a specific AI provider. A global default handles most cases. Individual pipes override when the task demands it. The runtime abstracts invocation so pipes declare _what_ they need, not _how_ to call it.

**Observability is infrastructure, not a feature.** The runtime logs every envelope transition. Pipes don't opt in to logging — they just run. Verbosity is a dial the user turns, globally or per-pipe. The system is always observable without any pipe needing to know it's being watched.
