# Virgil

Personal AI guide. Single binary, SQLite, model-driven tool selection.

## Stack

- Go 1.25, SQLite (mattn/go-sqlite3 with FTS5), kong CLI, chi HTTP, goose migrations
- Build: `just build` (requires CGO_ENABLED=1 and `-tags "fts5"`)
- Test: `just test-skeleton` (Stage 1), `just test-all` (full suite)

## Architecture

- `cmd/virgil/` -- single binary entrypoint (kong CLI)
- `internal/types.go` -- core types (Signal, Tool, ToolResult, Skill, Event)
- `internal/db/` -- SQLite connection, goose migrations
- `internal/config/` -- YAML config loading
- `internal/observe/` -- event logging to SQLite
- `internal/agent/` -- agent runtime (model loop, tool dispatch)
- `internal/tools/` -- tool implementations
- `internal/skills/` -- skill loader + scheduler
- `internal/memory/` -- memory system (FTS5 + vector + entities)
- `internal/channels/` -- channel adapters (telegram, mcp, cli)
- `internal/bridge/` -- AI provider abstraction
- `skills/` -- skill packages (SKILL.md + config.yaml per skill)
- `test/` -- shell-based acceptance tests

## Build spec

Full implementation spec: `~/dev/workshop/build/initiatives/virgil/phase1.md`

65 steps across 9 stages. Each stage has a test script as acceptance gate.

## Key commands

```
just build          # compile binary
just test-skeleton  # Stage 1 tests
just status         # health check (JSON)
just events         # query event log
```
