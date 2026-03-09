# Virgil

Personal AI operating system — an intent compiler that takes raw signals (text, voice) and chains atomic pipes to produce outcomes.

## Stack

- Go 1.25, Bubble Tea (TUI client)
- SQLite with FTS5 for memory storage
- Multi-provider AI: Anthropic (primary), OpenAI, Gemini, XAI, Claude CLI
- Chromedp for web scraping, SearXNG for search
- Google Calendar/Drive integration via OAuth

## Key Commands

```bash
just build              # Build binary + all pipe handlers
just build-if-changed   # Incremental build
just test               # go test ./... -v
just start              # Start server + TUI
just server             # Server-only mode
just stop               # Kill server
just lint               # golangci-lint
just auth               # OAuth setup helper
```

## Architecture

Core signal processing loop: `signal → classify → plan → execute → output`

- `cmd/virgil/` — Main binary (server + TUI client)
- `internal/router/` — 4-layer signal classification (exact match → parsed verb → BM25 keywords → AI fallback)
- `internal/parser/` — Rule-based NLP (vocabulary scanning, verb/type/target extraction)
- `internal/planner/` — Template matching to execution plans
- `internal/runtime/` — Pipeline execution engine
- `internal/pipe/` — Pipe registry and subprocess management
- `internal/pipes/` — 22 atomic capability pipes (each with pipe.yaml + cmd/main.go)
- `internal/bridge/` — AI provider abstraction
- `internal/store/` — SQLite memory persistence (relational graph)
- `internal/server/` — HTTP server with SSE streaming
- `internal/tui/` — Bubble Tea client (session, one-shot, pipe modes)
- `internal/voice/` — Speech daemon (hotkey, STT, TTS)
- `internal/config/` — YAML config loading

## Pipes (22 Atomic Capabilities)

analyze, build, calendar, chat, code, decompose, draft, educate, fix, fs, memory, publish, review, shell, spec, study, todo, verify, visualize, worktree

Each pipe is a persistent subprocess with stdin/stdout JSON RPC and a standard envelope contract.

## Memory System

Three types: working state (session, auto-pruned), explicit memory (long-lived facts), invocation history (raw + summarized).

Tiered summarization: raw entries (30 days) → daily summaries → weekly summaries → monthly summaries.

Fresh context assembly per invocation with configurable token budget (default 8k).

Storage: SQLite `memories` table + `memory_edges` for relational graph (refined_from, produced_by, co_occurred).

## Core vs Cloud

- **Core** (this repo): Router, planner, runtime, 22 pipes, memory, TUI, voice. Runs fully offline except AI calls.
- **Cloud** (virgil-cloud): Premium integrations (Jira, Slack, Mail), hosted infra.

## Design Principles

- Deterministic first, AI as fallback (router avoids AI on common queries)
- Pipes compose, don't integrate (new capabilities from chaining existing pipes)
- Memory replaces chat history (fresh context assembly, no accumulating buffer)
- Config-driven self-healing router (logs misses, proposes YAML changes)
- Voice outputs are one sentence or less

## Client Modes

- `virgil` — Interactive TUI session
- `virgil <query>` — One-shot query
- `echo ... | virgil` — Pipe mode (JSON in/out)

## Testing

75 test files. Table-driven tests, mock AI providers, pipe subprocess benchmarks.
