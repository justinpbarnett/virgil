# Pipe Specification

This document defines what a pipe is, what it must provide, and how to build one. It is the reference standard for anyone creating a new pipe for Virgil.

For the philosophy behind pipes, see `virgil.md`. For architectural decisions, see `ARCHITECTURE.md`.

---

## What a Pipe Is

A pipe is an atomic unit of capability. It does one thing. It accepts an envelope as input, produces an envelope as output, and composes with other pipes through the runtime.

A pipe does not know what comes before it or after it in a pipeline. It does not manage its own logging, metrics, or lifecycle. It receives input, does its work, and returns output. Everything else is the runtime's job.

Some pipes are deterministic — they call an API, query a database, perform a calculation. Some are non-deterministic — they invoke an AI model with a prompt and context. The distinction is an implementation detail. The interface is identical.

---

## File Layout

A pipe is a self-contained folder under `internal/pipes/{name}/`. Everything about a pipe — its metadata, its code, its executable, and its contributions to the global vocabulary and template systems — lives in one place.

```
internal/pipes/draft/
├── pipe.yaml      # definition: metadata, triggers, flags, prompts, vocabulary, templates
├── run            # compiled binary (gitignored, discovered by convention)
├── cmd/main.go    # package main, thin wrapper → pipehost.Run(...)
├── draft.go       # handler implementation (library code, testable)
└── draft_test.go  # handler tests
```

The `pipe.yaml` is the sole source of truth for a pipe's configuration. At startup, the config loader scans all pipe folders, loads each `pipe.yaml`, merges vocabulary contributions into a unified in-memory vocabulary, and merges template contributions into a sorted template list. There are no separate global files for vocabulary or templates — pipes own their contributions.

The main binary discovers pipes by scanning for `pipe.yaml` files. If a `run` executable exists in the pipe's folder, the pipe is registered as a subprocess. The `run` binary is built from `cmd/main.go` and communicates via the JSON stdin/stdout subprocess protocol.

To add a pipe: create the folder with `pipe.yaml` and an executable named `run`. For Go pipes, write handler code as library functions and a thin `cmd/main.go` wrapper using `pipehost.Run()`. For pipes in other languages, create any executable that implements the subprocess protocol. To remove a pipe: delete the folder. No other files need editing.

---

## The Pipe Contract

Every pipe must provide two things: a **definition** and a **handler**.

The definition is configuration. It tells the router and runtime everything they need to know about the pipe without executing it: what it's called, what signals should route to it, what flags it accepts, how it should be measured.

The handler is code. It receives an envelope, does the work, and returns an envelope.

---

## Definition

The pipe definition is declared in configuration. It is the pipe's identity — everything the system needs to route to it, invoke it, and measure it.

### Required Fields

```yaml
name: draft
description: Produces written content from input context and instructions.
category: comms
```

**name** — The pipe's unique identifier. Used in plans, logs, metrics, and configuration references. Lowercase, no spaces, no special characters. This is the name that appears in pipeline definitions (`draft`, `memory`, `calendar`, `code-review`).

**description** — A one-sentence explanation of what the pipe does. Used by the AI fallback when it needs to classify a signal against available pipes. Write this for a reader who has never seen the pipe before — it should be clear enough that someone (or an AI) can decide whether this pipe handles a given signal.

**category** — Which category the pipe belongs to. Used by the router's category narrowing layer to reduce the search space. Must be one of the defined categories (`time`, `memory`, `dev`, `comms`, `research`, `general`) or a custom category. Categories are auto-generated from pipe metadata at startup, so adding a new category here creates it.

### Triggers

```yaml
triggers:
  exact:
    - "write a draft"
    - "compose"
  keywords:
    - draft
    - compose
    - write
    - author
    - pen
  patterns:
    - "write {type}"
    - "draft {type}"
    - "compose {type} about {topic}"
```

Triggers tell the router when to route a signal to this pipe. They feed the router's deterministic layers:

**exact** — Phrases that resolve to this pipe in Layer 1 (exact match). Microsecond resolution. Use for the most common, unambiguous ways a user would invoke this pipe.

**keywords** — Individual words that associate with this pipe in Layer 2 (keyword index). These are added to the inverted index at startup. A signal containing "draft" will score this pipe highly. Keywords should be specific enough to distinguish this pipe from others — avoid generic words that would match too many pipes.

**patterns** — Structured phrases that help the parse stage extract components. The `{type}` and `{topic}` placeholders tell the parser where to find typed components in the signal. Patterns feed Layer 3 (category narrowing) and the parse stage.

Triggers grow over time through the self-healing process. When the AI fallback routes a signal to this pipe that the deterministic layers missed, the miss is logged. The self-healing pipeline can propose new triggers based on accumulated misses.

### Flags

```yaml
flags:
  type:
    description: What kind of content to produce.
    values: [blog, email, pr, memo, slack, report, article, newsletter]
    default: null
    required: false

  format:
    description: Output structure.
    values: [prose, bullets, outline]
    default: prose

  tone:
    description: Writing tone.
    values: [formal, casual, technical]
    default: null
```

Flags modify behavior without changing identity. A `draft` pipe with `--type=blog` and a `draft` pipe with `--type=email` are the same pipe. The flag selects which prompt template, which API parameters, or which code path to use.

**description** — What this flag controls. Used in help output and documentation generation.

**values** — The allowed values. If specified, the runtime validates input. If omitted, the flag accepts freeform input.

**default** — The value used when the flag isn't provided. If `null` and `required` is `false`, the pipe receives no value and decides its own behavior.

**required** — Whether the planner must provide this flag. If `true` and the planner can't extract a value from the signal, the pipe should handle the missing flag gracefully (prompt the user, use a sensible default, or return an error).

Flags are extracted from the signal by the parse stage. The word "blog" in "draft a blog post" maps to `--type=blog` through the vocabulary. Flags can also be set explicitly by the user (`draft --type=email`) or by a pipeline definition.

### Vocabulary

```yaml
vocabulary:
  verbs:
    draft: draft
    write: draft
    compose: draft
  types:
    blog: blog
    email: email
  sources: {}
  modifiers: {}
```

Vocabulary maps natural language words to semantic slots used by the parser and planner. Each pipe declares the words it owns. At startup, all pipe vocabularies are merged into a single unified vocabulary.

**verbs** — Words that map to a pipe action. "draft", "write", and "compose" all resolve to the `draft` pipe. Compound mappings like `remember: memory.store` encode both the pipe and the action.

**types** — Words that map to content types (flag values). "blog" in "draft a blog post" resolves to `--type=blog`.

**sources** — Words that map to data sources (other pipes that provide context). "notes" resolves to the `memory` pipe, telling the planner to retrieve from memory first.

**modifiers** — Words that map to time or scope qualifiers. "today" resolves to `--range=today`.

All four maps are optional — omit any that don't apply.

**Conflict handling:** If two pipes map the same word to different values (e.g., pipe A says `write: draft`, pipe B says `write: compose`), the loader returns a hard error at startup naming both pipes. Same word with the same mapping from multiple pipes is fine (idempotent).

### Templates

```yaml
templates:
  priority: 50
  entries:
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, sort: recent, limit: "10", topic: "{topic}" }
        - pipe: "{verb}"
          flags: { type: "{type}" }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }
```

Templates define composition patterns — multi-step plans the planner can instantiate when a signal's parsed components match a `requires` list. A pipe declares the templates it participates in.

**priority** — Lower numbers are matched first. Default is 50. Use lower priorities for pipes whose templates should take precedence.

**entries** — Each entry has a `requires` list (which parsed components must be present) and a `plan` (the steps to execute). Plan steps reference pipes by `{verb}`, `{source}`, etc. — template variables resolved from the parsed signal.

At startup, all pipe template contributions are merged and sorted: priority ascending, then specificity descending (more `requires` first), then pipe name alphabetical. The planner walks this sorted list and uses the first template whose `requires` are fully satisfied by the parsed signal.

Templates are optional — pipes with no composition patterns (e.g., `chat`) omit this section entirely.

### Provider (Non-Deterministic Pipes Only)

Non-deterministic pipes need an AI provider to do their work. The provider configuration is set at the server level in `virgil.yaml` and passed to all pipe subprocesses via environment variables:

- `VIRGIL_PROVIDER` — provider name (e.g., `claude`)
- `VIRGIL_MODEL` — model name (e.g., `sonnet`)
- `VIRGIL_PROVIDER_BINARY` — path to the provider's CLI binary

Inside a Go pipe subprocess, `pipehost.BuildProviderFromEnv()` reads these variables and returns a `bridge.Provider` ready to use. Pipes in other languages read the environment variables directly and invoke the provider CLI or API as needed.

Deterministic pipes ignore these variables entirely — they don't call AI models.

**Per-pipe provider overrides** are an architectural intent (see `ARCHITECTURE.md` decision #6) but are not yet implemented in the pipe.yaml schema. Currently, all non-deterministic pipes use the same server-level provider.

### Prompts (Non-Deterministic Pipes Only)

```yaml
prompts:
  system: |
    You are a professional writer. You produce clean, clear content
    appropriate to the format requested. You match the user's tone
    and style preferences when available.

  templates:
    blog: |
      Write a blog post based on the following material.

      Source material:
      {{.Content}}

      {{if .Topic}}Focus on: {{.Topic}}{{end}}
      {{if .Tone}}Tone: {{.Tone}}{{end}}

    email: |
      Draft an email based on the following context.

      Context:
      {{.Content}}

      {{if .Tone}}Tone: {{.Tone}}{{end}}

    pr: |
      Write a pull request description for the following changes.

      Changes:
      {{.Content}}
```

**system** — The system prompt sent with every invocation of this pipe. Defines the pipe's persona and general behavior. This is the pipe's identity at the AI level.

**templates** — Per-flag prompt templates. When the `--type` flag is `blog`, the `blog` template is used. Templates have access to the input envelope's content and any extracted flags via template variables.

Go pipes use Go's `text/template` syntax. The template data struct exposes these fields:

- `{{.Content}}` — the content field from the input envelope
- `{{.Topic}}` — the topic extracted by the parse stage
- `{{.Tone}}`, `{{.Length}}`, etc. — values of defined flags

Pipes in other languages may use any template engine — the prompt templates are raw strings in `pipe.yaml` and the pipe handler is responsible for rendering them.

If no template matches the flag value, the pipe should fall back to the system prompt alone with the raw content, or return an error if the flag is required for meaningful output.

Deterministic pipes omit this section entirely.

### Metrics (Planned)

Per-pipe metrics configuration is part of the architectural design (see `ARCHITECTURE.md` decisions #18 and #20) but is not yet parsed from `pipe.yaml`. The runtime will eventually provide default KPIs for every pipe (acceptance rate, error rate, duration) and allow pipes to override or extend with domain-specific KPIs.

The planned schema:

```yaml
metrics:
  - name: acceptance_rate
    type: ratio
    numerator: downstream_accept
    denominator: downstream_total
    window: 7d
    threshold:
      warn: 0.85
      degrade: 0.70

  - name: edit_rate
    type: ratio
    numerator: user_modified
    denominator: user_reviewed
    window: 7d
    threshold:
      warn: 0.30
      degrade: 0.50
```

Each metric has:

- **name** — identifier for this KPI
- **type** — the computation type (`ratio`, `average`, `percentile`, `count`)
- **window** — the time window for aggregation
- **threshold** — when this KPI should trigger a warning or degradation signal

When a KPI crosses a threshold, the runtime generates a self-improvement signal. The improvement pipeline analyzes failure patterns and proposes configuration changes — prompt amendments, flag defaults, provider swaps.

---

## The Handler

The handler is the code that executes when the pipe is invoked. It receives an input envelope and returns an output envelope.

### Input

The handler receives:

1. **The input envelope** — the output of the previous pipe in the chain, or the original signal if this is the first pipe. The envelope contains the standard fields: `pipe`, `action`, `args`, `timestamp`, `duration`, `content`, `content_type`, `error`. In the subprocess protocol, this arrives as the `envelope` field of the JSON request on stdin.

2. **The resolved flags** — extracted from the signal by the parse stage, set by a pipeline definition, or provided by the user explicitly. Flags arrive as key-value pairs in the `flags` field of the JSON request. Missing optional flags are absent, not null.

### Output

The handler must return an envelope with all required fields populated:

```
pipe:          this pipe's name
action:        what operation was performed (e.g., "draft", "retrieve", "review")
args:          the flags that were passed to this invocation
timestamp:     when execution started
duration:      how long execution took
content:       the actual output
content_type:  what kind of content ("text", "list", "structured", "binary")
error:         null on success, error info on failure
```

**content** — This is the pipe's output. It can be anything: prose text, a list of items, structured data, binary content. The envelope doesn't constrain the shape. Downstream pipes pull out what they need.

**content_type** — Tells downstream pipes what to expect without parsing the content. A pipe receiving `content_type: "list"` knows it can iterate. A pipe receiving `content_type: "text"` knows it has prose.

**error** — On success, this is null. On failure, this is an error object with at minimum a message and a severity. The runtime uses this to decide whether to retry (in a loop), skip to the next step, or halt the pipeline.

### Memory Access (Planned)

Currently, pipes receive all their context through the input envelope — whatever the planner assembled upstream is in the envelope's `content` field. There is no in-process memory interface available to subprocess pipes.

The planned design (see `ARCHITECTURE.md` decisions #24-26) adds two capabilities:

**Context assembly** — The planner will assemble a base context per-invocation using a retrieval strategy matched to the signal type. This context will be delivered to the pipe through the envelope, so no protocol change is needed.

**Pipe-initiated memory queries** — If a handler needs context the planner didn't include, it will be able to query memory directly from remaining budget headroom. This requires a protocol extension (not yet designed) to allow subprocess pipes to make memory requests during execution.

Rules for pipe-initiated memory access (when available):

- **Respect the budget.** The runtime will track how much of the context budget the planner already used. Pipe-initiated retrievals draw from the remainder.
- **Be specific.** Broad queries waste headroom on irrelevant results.
- **Don't duplicate.** The planner may have already included what you need in the base context.

Working state will be saved automatically by the runtime after every invocation. Explicit memory writes are for long-term facts the pipe wants to persist beyond the current task.

### Error Handling

Pipes should handle their own errors and report them through the envelope's `error` field. The runtime decides what to do based on the error:

**Recoverable errors** — the pipe tried and got a partial or degraded result. Set `error` with a warning severity. Populate `content` with whatever partial output is available. The runtime may proceed to the next pipe with the partial result.

```
error:
  message: "Calendar API returned partial results — weekend events missing"
  severity: warn
  retryable: false
```

**Retryable errors** — a transient failure that might succeed on retry. Set `error` with a retryable flag. The runtime may retry the pipe based on pipeline configuration.

```
error:
  message: "Provider timeout after 30s"
  severity: error
  retryable: true
```

**Fatal errors** — the pipe cannot produce any useful output. Set `error` with a fatal severity. Leave `content` null or empty. The runtime halts the pipeline (or the current branch, in a parallel pipeline) and reports the failure.

```
error:
  message: "No calendar API credentials configured"
  severity: fatal
  retryable: false
```

Pipes should never crash, panic, or exit the process. Capture all errors and return them in the envelope. The runtime's job is to decide what to do with failures. The pipe's job is to report them accurately.

---

## Subprocess Pipes

Every pipe runs as a subprocess executable. The main virgil binary discovers pipes by scanning for `pipe.yaml` files, validates that a `run` executable exists in the pipe's folder, and invokes it via a JSON stdin/stdout protocol. There is no compiled-in pipe registration — adding a pipe in any language means creating a folder with `pipe.yaml` and an executable named `run`.

### Protocol

**Request (stdin)** — a single JSON object:

```json
{
  "envelope": {"pipe": "signal", "action": "input", "content": "...", "content_type": "text"},
  "flags": {"action": "retrieve", "limit": "10"},
  "stream": false
}
```

**Response (stdout) — non-streaming** (`stream: false`):

A single JSON envelope:

```json
{"pipe": "memory", "action": "retrieve", "content": [...], "content_type": "list", "error": null}
```

**Response (stdout) — streaming** (`stream: true`):

One JSON object per line. `chunk` lines are streamed to the user. The final `envelope` line is the complete result:

```
{"chunk": "First part..."}
{"chunk": "More text..."}
{"envelope": {"pipe": "draft", "action": "generate", "content": "Full text...", "content_type": "text"}}
```

### Error Handling

- Exit 0 + valid JSON → success
- Exit 0 + invalid JSON → fatal error
- Exit non-zero + valid JSON on stdout → use the envelope
- Exit non-zero + no valid JSON → fatal error from stderr
- Timeout → retryable error, subprocess killed

### Duration

`time.Duration` serializes as nanoseconds (int64). 150000000 = 150ms.

### Configuration

`pipe.yaml` supports these fields for subprocess control:

```yaml
streaming: false    # whether this pipe supports streaming (default false)
timeout: 30s        # subprocess timeout (default 30s)
```

The system discovers the pipe's executable at `{pipe_dir}/run` by convention. If the executable exists and is executable, the pipe is registered. If not, it is skipped with a warning.

### Environment Variables

Subprocess pipes inherit the parent environment plus:

- `VIRGIL_DB_PATH` — SQLite database path
- `VIRGIL_CONFIG_DIR` — virgil config directory (pipe definitions, virgil.yaml)
- `VIRGIL_USER_DIR` — user config directory (~/.config/virgil), for credentials and tokens
- `VIRGIL_PROVIDER` — AI provider name
- `VIRGIL_MODEL` — AI model
- `VIRGIL_PROVIDER_BINARY` — path to provider CLI

### Working Directory

The subprocess's working directory is set to the pipe's folder (where `pipe.yaml` lives). This means `pipehost.LoadPipeConfig()` can read `pipe.yaml` from the current directory.

### Stderr

Stderr from the subprocess is captured. If the process exits non-zero with no valid JSON on stdout, stderr content becomes the fatal error message.

### Writing a Pipe in Any Language

Any executable that reads the JSON request from stdin and writes the JSON response to stdout works. Example shell pipe:

```sh
#!/bin/sh
echo '{"pipe":"echo","action":"echo","args":{},"content":"it works","content_type":"text","error":null}'
```

Example Python pipe:

```python
#!/usr/bin/env python3
import json, sys

req = json.load(sys.stdin)
content = req["envelope"].get("content", "")
result = {"pipe": "upper", "action": "transform", "args": {}, "content": content.upper(), "content_type": "text", "error": None}
json.dump(result, sys.stdout)
```

### The `pipehost` Go Library

For Go pipe executables, the `internal/pipehost` package provides shared protocol handling:

```go
import "github.com/justinpbarnett/virgil/internal/pipehost"

func main() {
    // Initialize dependencies from env vars...
    pipehost.Run(myHandler, myStreamHandler)
}
```

- `pipehost.Run(handler, streamHandler)` — reads request from stdin, calls the handler, writes response to stdout. If `stream: true` and streamHandler is non-nil, uses streaming protocol.
- `pipehost.Fatal(pipeName, message)` — writes a fatal error envelope to stdout and exits. For startup failures.
- `pipehost.BuildProviderFromEnv()` — creates a `bridge.Provider` from `VIRGIL_PROVIDER`, `VIRGIL_MODEL`, and `VIRGIL_PROVIDER_BINARY` environment variables. Returns an error if the provider cannot be created.
- `pipehost.LoadPipeConfig()` — loads `pipe.yaml` from the current working directory.

---

## Deterministic Pipes

Deterministic pipes produce the same output for the same input. They call APIs, query databases, perform calculations, read files, or interact with system services. No AI involved.

### Guidelines

- **Fast.** Deterministic pipes are the building blocks that make Virgil feel instant. If a deterministic pipe is slow, it's a bottleneck for every pipeline that uses it. Target sub-second execution for most operations.

- **Predictable.** Given the same input and flags, a deterministic pipe should produce the same output. External state (API responses, database contents) may vary, but the pipe's behavior given that state should be consistent.

- **No AI calls.** If a deterministic pipe needs to interpret ambiguous input, it should return an error rather than silently invoking an AI model. The planner should have resolved ambiguity before the pipe was invoked.

- **Validate early.** Check that required flags are present, that credentials exist, that the API is reachable — before doing the main work. Return clear errors for configuration problems.

### Example: `calendar`

```yaml
name: calendar
description: Reads events from calendar services.
category: time

triggers:
  exact:
    - "check my calendar"
    - "what's on my schedule"
    - "what's on my calendar"
  keywords:
    - calendar
    - schedule
    - meeting
    - event
    - appointment
  patterns:
    - "what's on my calendar {modifier}"
    - "check my calendar {modifier}"

flags:
  range:
    description: Time range to query.
    values: [today, tomorrow, this-week]
    default: today

  calendar:
    description: Which calendar to use.
    default: primary

vocabulary:
  verbs:
    check: calendar
    show: calendar
    "what's": calendar
  types: {}
  sources:
    calendar: calendar
  modifiers:
    recent: recent
    today: today
    tomorrow: tomorrow
    "this week": this-week
```

Handler behavior: Call the calendar API with the resolved flags. Return events as a structured list in the envelope's `content` field with `content_type: "list"`. Each event in the list has at minimum a title, start time, end time, and location. If the API call fails, return an error envelope with the API's error message and appropriate severity.

Subprocess entry point (`cmd/main.go`):

```go
func main() {
    userDir := os.Getenv(pipehost.EnvUserDir)

    client, err := calendar.NewGoogleClient(userDir)
    if err != nil {
        fmt.Fprintf(os.Stderr, "calendar: %v (continuing without client)\n", err)
        pipehost.Run(calendar.NewHandler(nil), nil)
        return
    }
    pipehost.Run(calendar.NewHandler(client), nil)
}
```

This is a deterministic pipe — no AI provider needed. It reads credentials from `VIRGIL_USER_DIR` and calls the Google Calendar API directly. If credentials are missing or invalid, it degrades gracefully by running with a nil client rather than failing fatally.

---

## Non-Deterministic Pipes

Non-deterministic pipes invoke an AI model. Their output varies across invocations, even with identical input. The prompt defines what the pipe does. Flags select which prompt template to use.

### Guidelines

- **The prompt is the pipe's soul.** The system prompt and templates are the most important part of a non-deterministic pipe's configuration. They determine output quality. Write them carefully, test them against real inputs, and iterate.

- **Flags replace pipes.** Don't create separate pipes for blog drafting and email drafting. Create one `draft` pipe with a `--type` flag. The pipe's capability is "producing written content." The flag selects the template. New output types are a configuration addition, not a new pipe.

- **Know your provider.** All non-deterministic pipes currently use the server-level provider configured in `virgil.yaml`. If your pipe has specific model requirements (code review needs a code-strong model, research needs grounded search), document them. Per-pipe provider overrides are planned but not yet implemented.

- **Include context instructions.** The prompt should tell the model what to do with the context it receives. "You will be given a set of notes. Synthesize them into..." is better than assuming the model knows why it's receiving a list of notes.

- **Constrain output.** If the envelope needs structured content (a list, a structured review with categories), the prompt should request that structure. Don't rely on the downstream pipe to extract structure from prose.

### Example: `draft`

```yaml
name: draft
description: Produces written content from input context and instructions.
category: comms
streaming: true
timeout: 60s

triggers:
  exact:
    - "write something"
    - "draft this"
  keywords:
    - draft
    - compose
    - write
    - author
  patterns:
    - "draft {type}"
    - "write {type} about {topic}"
    - "draft {type} based on {source}"

flags:
  type:
    description: What kind of content to produce.
    values: [blog, email, pr, memo]
    default: ""

  tone:
    description: Writing tone.
    values: [formal, casual, technical]
    default: ""

  length:
    description: Approximate output length.
    values: [short, medium, long]
    default: medium

vocabulary:
  verbs:
    draft: draft
    write: draft
    compose: draft
    summarize: draft
    summary: draft
  types:
    blog: blog
    email: email
    pr: pr
    memo: memo
  sources: {}
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve, sort: recent, limit: "10", topic: "{topic}" }
        - pipe: "{verb}"
          flags: { type: "{type}" }

    - requires: [verb, source, modifier]
      plan:
        - pipe: "{source}"
          flags: { range: "{modifier}" }
        - pipe: "{verb}"
          flags: { type: summary }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}", topic: "{topic}" }

    - requires: [verb, source]
      plan:
        - pipe: "{source}"
          flags: { action: retrieve }
        - pipe: "{verb}"
          flags: {}

    - requires: [verb]
      plan:
        - pipe: "{verb}"

prompts:
  system: |
    You are a professional writer. You produce clean, clear, well-structured
    content appropriate to the format requested. When given context material,
    you synthesize it — you don't just summarize. You match the user's tone
    and style preferences when provided.

  templates:
    blog: |
      Write a blog post based on the following material.

      Source material:
      {{.Content}}

      {{if .Topic}}Focus on: {{.Topic}}{{end}}
      {{if .Tone}}Tone: {{.Tone}}{{end}}
      {{if .Length}}Target length: {{.Length}}{{end}}

    email: |
      Draft an email based on the following context.

      Context:
      {{.Content}}

      {{if .Tone}}Tone: {{.Tone}}{{end}}

    pr: |
      Write a pull request description for the following changes.

      Changes:
      {{.Content}}

      Include: summary of what changed, why it changed, testing notes,
      and any migration steps if applicable.
```

Handler behavior: Resolve the prompt template from the `--type` flag. Inject the input envelope's content and any flags into the template variables using Go's `text/template`. Send the system prompt + resolved template to the configured provider. Return the model's response as the envelope's `content` with `content_type: "text"`. If the provider call fails, return a retryable error if it was a timeout, fatal if it was a configuration or authentication error.

Subprocess entry point (`cmd/main.go`):

```go
func main() {
    provider, err := pipehost.BuildProviderFromEnv()
    if err != nil {
        pipehost.Fatal("draft", err.Error())
    }

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("draft", err.Error())
    }

    compiled := draft.CompileTemplates(pc)

    sp, ok := provider.(bridge.StreamingProvider)
    if ok {
        pipehost.Run(draft.NewHandlerWith(provider, pc, compiled), draft.NewStreamHandlerWith(sp, pc, compiled))
    } else {
        pipehost.Run(draft.NewHandlerWith(provider, pc, compiled), nil)
    }
}
```

This is a non-deterministic pipe — it needs an AI provider. It loads the provider from environment variables via `pipehost.BuildProviderFromEnv()`, loads its own `pipe.yaml` for prompt templates via `pipehost.LoadPipeConfig()`, pre-compiles Go `text/template` templates for reuse, and supports streaming when the provider implements `bridge.StreamingProvider`.

---

## Composition Rules

Pipes don't know about each other. But for composition to work, there are conventions around what a pipe puts in `content` and `content_type` so downstream pipes can consume it reliably.

### Content Type Conventions

| content_type | What's in content                | Downstream expectations                                                    |
| ------------ | -------------------------------- | -------------------------------------------------------------------------- |
| `text`       | Prose string                     | Can be read as-is, summarized, or used as AI context                       |
| `list`       | Array of items                   | Can be iterated, filtered, counted, or rendered as a list                  |
| `structured` | Key-value data or nested objects | Downstream pipe reads specific fields by name                              |
| `binary`     | Binary data (file, image, audio) | Downstream pipe must know the format (declared in args or a subtype field) |

A pipe should use the most specific content type that fits. If you're returning calendar events, use `list` — not `text` with events formatted as prose. If you're returning a review with categories and severity ratings, use `structured` — not `text` with the structure embedded in prose.

### Designing for Composition

When building a pipe, consider how its output will be consumed:

- **As AI context.** A non-deterministic pipe downstream will receive your content in its prompt. Make the content self-explanatory — include enough structure that the AI model knows what it's looking at without needing the envelope metadata.

- **As data input.** A deterministic pipe downstream might parse your content programmatically. Use `structured` or `list` content types with consistent field names so downstream pipes can access fields by name rather than parsing prose.

- **As final output.** If your pipe is typically the last in a chain (like `draft`), your content is what the user sees. Make it presentable. If it's typically mid-chain (like `memory.retrieve`), optimize for downstream consumption over human readability.

### The Isolation Principle

A pipe must not:

- Import or call another pipe directly. Composition happens through the runtime, not through pipe-to-pipe calls.
- Assume which pipe produced its input envelope. Read the envelope's content and content_type to understand what you received.
- Assume which pipe will consume its output envelope. Produce the most useful output for any possible downstream consumer.
- Manage its own logging or metrics. The runtime handles both automatically at every envelope transition.
- Manage its own lifecycle. The runtime starts, stops, and restarts pipes.

A pipe may:

- Query memory for additional context (within headroom).
- Write to memory explicitly (for long-term facts worth retaining).
- Return errors in the envelope for the runtime to handle.
- Declare its provider, model, and invocation preferences.

---

## Testing

Every pipe should be testable in isolation. Because pipes don't know about each other and communicate only through envelopes, testing is straightforward: construct an input envelope, invoke the handler, assert on the output envelope.

### What to Test

**For all pipes:**

- **Happy path** — given valid input and flags, does the pipe produce the expected output with the correct content_type?
- **Missing flags** — does the pipe handle missing optional flags gracefully? Does it return a clear error for missing required flags?
- **Empty input** — does the pipe handle an empty or null content field without crashing?
- **Error reporting** — when something goes wrong (API down, invalid input, timeout), does the pipe return a properly structured error envelope rather than crashing?
- **Envelope compliance** — does the output contain all required fields (pipe, action, args, timestamp, duration, content, content_type, error)?

**For deterministic pipes:**

- **Determinism** — given the same input and mocked external state, does the pipe produce the same output?
- **API failure modes** — test with mocked API failures (timeout, auth error, rate limit, partial response) and verify the error envelope is correct.

**For non-deterministic pipes:**

- **Prompt construction** — given specific flags and input, does the pipe construct the correct prompt? (Test the template resolution separately from the AI call.)
- **Provider failure modes** — test with mocked provider failures (timeout, rate limit, malformed response) and verify error handling.
- **Output parsing** — if the pipe expects structured output from the AI model, test that it handles malformed model responses gracefully.

### Test Envelope

For testing, construct minimal envelopes with only the fields your pipe reads:

```
# Minimal input envelope for testing
pipe: test
action: test
args: {}
timestamp: 2026-01-01T00:00:00Z
duration: 0
content: "the test content your pipe will receive"
content_type: text
error: null
```

---

## Checklist

Before shipping a new pipe, verify:

```
File Layout
  ☐  pipe.yaml lives at internal/pipes/{name}/pipe.yaml
  ☐  handler code lives alongside pipe.yaml in the same folder
  ☐  no configuration for this pipe exists outside its folder

Definition
  ☐  name is unique, lowercase, no special characters
  ☐  description is one clear sentence
  ☐  category is correct
  ☐  triggers cover the common ways a user would invoke this
  ☐  flags have descriptions and sensible defaults
  ☐  (non-deterministic) prompts are tested against real inputs
  ☐  (non-deterministic) streaming and timeout set in pipe.yaml

Vocabulary
  ☐  verbs map natural language words to this pipe's action(s)
  ☐  types map content-type words to flag values (if applicable)
  ☐  sources map data-source words to pipe names (if applicable)
  ☐  modifiers map qualifier words to flag values (if applicable)
  ☐  no word conflicts with other pipes (same word, different mapping)

Templates
  ☐  templates cover the composition patterns this pipe participates in
  ☐  priority is set appropriately (default 50)
  ☐  requires lists use only standard slot names (verb, type, source, modifier, topic)
  ☐  plan steps use template variables ({verb}, {source}, etc.) not hardcoded pipe names

Handler
  ☐  returns a complete envelope with all required fields
  ☐  content_type accurately describes the content
  ☐  errors are returned in the envelope, never thrown
  ☐  handles missing optional flags gracefully
  ☐  handles empty input gracefully
  ☐  (non-deterministic) prompt templates handle all flag combinations
  ☐  (non-deterministic) model response parsing is defensive

Composition
  ☐  content is self-explanatory (doesn't require envelope metadata to understand)
  ☐  content_type is as specific as possible (list over text, structured over text)
  ☐  no direct imports or calls to other pipes
  ☐  no assumptions about upstream or downstream pipes

Subprocess
  ☐  cmd/main.go exists with thin pipehost.Run() wrapper (Go pipes)
  ☐  `run` binary builds and is gitignored
  ☐  streaming and timeout set in pipe.yaml if applicable
  ☐  handler works correctly when invoked via subprocess protocol
  ☐  handles startup failures gracefully (pipehost.Fatal)

Testing
  ☐  happy path test with expected output
  ☐  missing flag tests
  ☐  empty input test
  ☐  error handling tests with mocked failures
  ☐  envelope compliance test (all fields present)
```
