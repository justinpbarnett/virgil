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

## Deploying to Fly.io

The `fly.toml` and `Dockerfile` live in `deploy/` but the Docker build context must be the repo root (Dockerfile references `config/`, `skills/`, etc.):

```sh
cd /home/jpb/dev/virgil
~/.fly/bin/fly deploy --app virgil-justin --config deploy/fly.toml --dockerfile deploy/Dockerfile
```

Running `fly deploy` from the `deploy/` directory fails because the build context only contains `deploy/` -- missing go.mod, config/, skills/.

## Connecting from Claude Code (MCP)

Virgil exposes MCP via stdio. To use it from Claude Code, register it as a user-scoped server so it's available in every project:

```sh
claude mcp add --scope user virgil -- \
  ~/.fly/bin/fly ssh console --app virgil-justin \
  -C "virgil mcp --config /data/virgil.yaml"
```

Notes:
- Use `--scope user` to make it global. Without it, Claude Code defaults to `local` scope (only the current project).
- `~/.claude/.mcp.json` is NOT a valid path for user-level MCP config -- it is silently ignored.
- The `-C` flag passes the command string to the remote shell. Quote the full command including flags.
- `--config /data/virgil.yaml` is required on Fly.io; the default config path (`~/.virgil/virgil.yaml`) doesn't exist there.
- Connection takes ~2s on first use per session (fly SSH tunnel setup). Subsequent calls in the same session reuse the tunnel.

To verify: `claude mcp list` -- should show `virgil: ... Connected`.

### After re-deploying

If the machine restarts and the SSH key changes, re-run the `claude mcp add` command to clear any stale host key state.
