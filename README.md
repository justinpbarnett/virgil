# Virgil

A personal helper. Named for Dante's guide, not the hero, but the one who knows the terrain and gets you where you're going.

Virgil takes raw signal (text, voice, ambient data), classifies what you want, decomposes it into a plan, and chains solved problems together to produce the outcome. Every interaction follows the same loop:

```
signal → classify → plan → execute → output
```

A typed request and a location-triggered reminder are the same thing. Both are signals. Both get classified into intent. Both produce a plan. The system that handles your requests and the system that improves itself are the same system.

## What makes it different

**Deterministic first, AI as fallback.** The router is a layered search engine, not a language model. Exact match → keyword index → category narrowing → AI fallback. 80%+ of queries never touch AI. When the AI fallback does fire, it logs the miss so the deterministic layers can learn. Over time, the AI surface area shrinks. The system gets faster and cheaper the more you use it.

**Pipes and pipelines.** Every capability is an atomic pipe — one thing in, one thing out, composable with any other pipe through a standard envelope contract. Complex workflows are pipelines: graphs of pipes with parallel branches, retry loops, and cycles. A pipeline looks like a pipe from the outside. Composition is recursive. This is the Unix philosophy applied to a personal assistant.

**Memory, not chat history.** Every invocation is stateless. There is no conversation buffer accumulating until it overflows and gets lossily compressed. Context is assembled fresh each time by pulling exactly what's needed from memory. You never hit a context wall. You never feel the AI forgetting.

**Self-healing infrastructure.** The router learns from its own misses. Every pipe has KPIs derived from how its output is received. When performance degrades, the system proposes (or auto-applies) configuration changes — new keywords, prompt amendments, flag defaults. Improvements land in readable config files, not retrained weights. Inspectable, editable, reversible.

## Architecture

The real substance of this project lives in the specs:

| Document                             | What it covers                                                                                                                      |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| [`virgil.md`](virgil.md)             | Philosophy and conceptual model — what signals are, how the router works, what self-healing means, how memory replaces chat history |
| [`architecture.md`](architecture.md) | Technical decisions with rationale — every major architectural choice, what was considered, what was chosen and why                 |
| [`pipe.md`](pipe.md)                 | Pipe specification — the atomic unit of capability, file layout, definition schema, handler contract, subprocess protocol           |
| [`pipeline.md`](pipeline.md)         | Pipeline specification — composable graphs of pipes with sequential, parallel, loop, and cycle execution semantics                  |
| [`tui.md`](tui.md)                   | Terminal interface specification — the primary client, visual structure, interaction patterns, progressive disclosure               |

If you're here to understand the ideas, start with `virgil.md`. If you're here to understand the engineering, start with `architecture.md`.

## Stack

- **Server:** Go
- **Primary client:** Terminal UI ([bubbletea](https://github.com/charmbracelet/bubbletea))
- **Memory:** SQLite with FTS5
- **AI bridge:** Multi-provider, supports CLI and API invocation
- **Metrics:** Append-only JSONL with hierarchical summarization

## A note on what this is

This is a personal tool I build for myself. It's public because the ideas are worth sharing and the architecture might be useful to others working on similar problems.

You are welcome to use it, fork it, read the specs, steal patterns, and build your own version. I do not accept feature requests, guarantee support, or maintain a release schedule. There is no roadmap beyond what I need next.

If you find a bug and want to open an issue, go ahead (Virgil will see it). If you want to open a PR, know that I'll only merge things that align with how I use the tool. This isn't a community project. It's my project, and it happens to be open.

## License

[MIT](LICENSE)
