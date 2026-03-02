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

### Generalized to All Pipes

The router's self-healing loop is not unique to the router. It is a pattern that applies to every pipe.

The runtime tracks metrics at every envelope transition — how was this pipe's output received? Did the downstream pipe succeed or fail? Did the user accept the output without changes, or edit it? How many retry loops were needed? These signals accumulate into per-pipe KPIs the same way router misses accumulate into upgrade triggers.

A builder pipe that consistently fails verification on database migrations has a measurable problem with a concrete improvement path — a prompt amendment, a context addition, a provider change. A draft pipe whose output the user edits 60% of the time has a quality signal. A research pipe that takes 10 seconds when it should take 3 has a performance signal.

When any pipe's KPIs cross a threshold, the system generates a signal:

```
Signal:  pipe.builder.first_pass_verify_rate < 0.60
Intent:  self-improve(pipe=builder)
Plan:    metrics.retrieve(pipe=builder, window=7d)
         | analyze(type=failure_patterns)
         | improve.propose(target=builder.config)
Output:  proposed configuration change + summary
```

Improvements fall into two categories. **Auto-apply** changes are safe, narrow, and reversible — adding context to a prompt, adjusting a flag default, adding a router keyword. They are applied automatically, measured, and rolled back if KPIs degrade. **Advisory** changes require human judgment — switching providers, restructuring a pipeline, creating a new pipe. They are surfaced as proposals for the user to review.

Every auto-applied change is versioned. The system tracks which configuration was active when each KPI was measured. If performance degrades after a change, the system can correlate and propose a rollback. The user never has to wonder "what changed."

### External Intelligence

The internal improvement loop optimizes how Virgil uses its current capabilities. A separate scheduled pipeline tracks whether better capabilities exist.

On a configurable schedule (default nightly), Virgil monitors external sources — provider changelogs, model releases, benchmark results, dependency updates — and produces a summary with actionable proposals. Safe configuration changes (updated API endpoints, new CLI tool versions) can be auto-applied. Larger changes (new model worth evaluating, breaking API changes) are advisory.

```
Signal:  schedule.nightly (ambient signal)
Intent:  self-upgrade
Plan:    sources.fetch(configured_feeds)
         | analyze(type=relevance, context=current_config)
         | parallel:
             auto_apply(safe_changes)
             propose(advisory_changes)
         | summarize(type=voice)
Output:  "Overnight: applied 2 dependency updates, both passing.
          Anthropic shipped a new model — benchmarks suggest it's
          stronger for code review. Want me to run a comparison?"
```

This is the same system all the way down. A scheduled ambient signal, routed through the same planner, executing the same pipes, producing the same envelopes. The system that processes your requests, the system that improves itself, and the system that watches for better tools are all the same system.

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

### Distinction from the Miss Log and Metrics

The router's miss log, general logging, and pipe metrics are three different things.

**General logging** is observability — it tells you what happened during a specific pipeline execution. It serves the person debugging a problem right now.

**The miss log** is a learning signal — it tells the router what to improve. It feeds the self-healing loop that makes deterministic routing smarter over time.

**Metrics** are performance signals — they tell each pipe how well it's doing over time. They feed the generalized improvement loop that makes every pipe better. Metrics are tracked automatically at every envelope transition, stored as append-only logs, and compressed into hierarchical summaries (hourly → daily → weekly → monthly) that trade granularity for searchability.

All three are infrastructure. All three are automatic. None is a pipe.

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

## Interaction Model

To the user, Virgil feels like one continuous conversation. You talk, it responds, you refine, it adjusts. There are no "chats" to create, no sessions to manage. It looks like talking to one person who knows you.

Behind the scenes, every message is a brand new invocation. There is no conversation state. No chat history is passed from one message to the next. Each signal hits the router fresh, gets classified, planned, and executed independently. The continuity the user experiences comes from memory, not from a conversation buffer.

### Memory Replaces Chat History

When you say "make it shorter," Virgil doesn't look at the previous message in a thread. It retrieves context from memory: the current draft, what you've asked for so far, the direction you're heading. The plan becomes something like `memory.retrieve(working_context) | draft(type=revise, instruction="shorter")`. Fresh invocation, but it has everything it needs.

When you say "okay, build it" after iterating on a spec for an hour, Virgil retrieves the spec from memory and routes to the dev-feature pipeline. The transition from conversation to execution is seamless because memory bridges them.

This also means context is not bound to a client. You can close the TUI, open Virgil on your phone, say "how's that spec looking?" and get the right answer. The context lives in memory on the server, not in any client's display buffer.

### Context Assembly

Each invocation starts with an empty context window and fills it with exactly what it needs. There is nothing to compact because nothing accumulates. Context is assembled, not accumulated.

The planner assembles a base context using a strategy appropriate to the signal type. The router classification determines the retrieval pattern — different signal types need different kinds of context:

```
one-shot factual    →  signal only, maybe user prefs. Minimal retrieval.
refinement          →  signal + working state. That's it.
creative/generative →  signal + topical memory + user prefs.
complex pipeline    →  signal + working state + topical memory. Heavy retrieval.
ambient signal      →  time/location context + calendar state. No working state.
```

A fixed context budget acts as a ceiling. The planner fills it according to the strategy — what to retrieve and in what proportion. "What's on my calendar" doesn't waste budget on topical memory. "Make it shorter" doesn't waste budget on user preferences. Each invocation gets a context shaped for its task.

If a pipe needs something the planner didn't include, it can query memory directly, drawing from the remaining budget headroom. This is the escape hatch — the planner handles 90% of context needs, and pipes handle the other 10%.

```
planner assembles:  signal + working state + topical memory  (5,000 of 8,000 tokens)
draft pipe runs, needs writing style prefs
draft calls:        memory.retrieve(topic="writing style")    (300 tokens, within headroom)
```

This is why Virgil never has the compaction problem. There is no growing conversation buffer that eventually overflows. Each invocation pulls exactly what it needs from memory into a fresh context. If memory has more relevant entries than fit in the budget, the strategy determines what gets included — and everything else stays in memory for a future invocation that needs it.

### Memory Writes

Memory writes are hybrid — some automatic, some explicit.

**Automatic writes:** The runtime saves working state after every invocation that produces or modifies an artifact. If you're iterating on a spec, each version is saved automatically. The signal, the plan, and the output are all recorded. This is infrastructure — no pipe opts in.

**Explicit writes:** Long-term facts are saved when the user or a pipe explicitly asks. "Remember that I prefer session tokens over JWTs" is an explicit memory write. So is a pipe that stores research findings for later retrieval. These are deliberate, not automatic.

The distinction matters for retention. Working state is high-churn — many entries, rapidly updating, most of it only relevant for the current task. Explicit memories are low-churn and long-lived. Both live in the same storage system, but the automatic writes can be pruned more aggressively because they're recoverable from context (the raw interaction history is still there within its retention window).

### Memory Summarization

Memory uses the same hierarchical summarization pattern as metrics. Recent interactions are stored in full detail. Over time, older entries are condensed into summaries — daily, weekly, monthly — that trade granularity for density.

```
Raw entries:     every interaction, full detail (retained 30 days)
Daily summaries: topics worked on, artifacts produced, decisions made
Weekly:          narrative context ("iterated on OAuth spec, settled on session tokens")
Monthly:         high-level project and preference evolution
```

Most context retrieval reads from summaries. Only when the signal needs specific detail — "what was the JWT approach again?" — does it drill into raw entries. The planner's context strategy determines which level to read from.

### The Stream and the Side Panel

The primary interface has two regions: the stream and the detail panel.

**The stream** is the main conversation. Every input you type and every response Virgil gives appears here, in order. One-shot queries ("what's on my calendar"), iterative refinement ("make it shorter"), and pipeline kickoffs ("build it") all live in the same flow. When a long-running pipeline starts, the stream shows a notification. When it finishes, the stream shows the result.

```
you ❯ add OAuth login to the Keep app

  ▸ Starting dev-feature pipeline...

you ❯ what's on my calendar today

  You have three meetings — 10am standup, 1pm design review, 3pm 1:1 with Sarah.

  ▸ dev-feature: cycle 1 complete, review found 2 issues, cycling...

you ❯ remind me to pick up groceries after my last meeting

  Done — reminder set for 3:45 PM.

  ▸ dev-feature: complete. PR #47 ready for review.
```

The stream stays conversational. You keep talking to Virgil while background work runs. Pipeline progress is interleaved as brief status lines — enough to stay aware, not enough to drown the conversation.

**The detail panel** shows the internals of a running (or completed) pipeline. When `dev-feature` is running, you can open the detail panel to see each stage: what the builder is doing, the verify/fix loop iterations, review findings. If multiple pipelines are running in parallel, the panel lets you switch between them.

The detail panel is optional. For simple interactions it stays closed. For complex pipelines it's where you go when you want to understand what's happening inside the machine, intervene, or inspect output from a specific stage.

---

## Principles

**Deterministic first, AI as fallback.** The common case should never require an AI call. AI is expensive, slow, and non-deterministic. Use it where it's needed — creative work, ambiguous classification, synthesis — and keep it away from routing, retrieval, and orchestration.

**The system improves deterministically.** When AI teaches the system something, that knowledge becomes a configuration change. The next time, the system doesn't need AI. Over time, the AI surface area shrinks. This applies to routing, to individual pipe quality, and to staying current with external capabilities.

**Pipes do one thing. Pipelines compose them.** Every capability is atomic. Complex behavior comes from composing pipes into pipelines — graphs with parallel branches, retry loops, and cycles. A pipeline is itself a pipe from the outside: envelope in, envelope out. Composition is recursive.

**Signals, not commands.** Input is not limited to text. Anything that carries intent is a signal. The router accepts all signals through the same classification pipeline.

**Same system all the way down.** User requests, ambient triggers, and self-improvement all flow through the same signal → classify → plan → execute loop. There are no special cases.

**Proportional complexity.** Simple queries get simple treatment. "What's on my calendar" should resolve in microseconds with a direct API call. "Draft a blog post based on my recent notes about church tech" should decompose into a multi-step pipeline. The system scales its effort to match the query's actual complexity.

**Inspectable and reversible.** Configuration is in readable files. Routes are deterministic and testable. The user can see exactly why a signal was classified the way it was, and change it.

**Model-agnostic.** No pipe is married to a specific AI provider. A global default handles most cases. Individual pipes override when the task demands it. The runtime abstracts invocation so pipes declare _what_ they need, not _how_ to call it.

**Observability is infrastructure, not a feature.** The runtime logs every envelope transition. Pipes don't opt in to logging — they just run. Verbosity is a dial the user turns, globally or per-pipe. The system is always observable without any pipe needing to know it's being watched.
