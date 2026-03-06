# Refactor: Daily Log Rotation

## Metadata

type: `refactor`
task_id: `daily-log-rotation`
prompt: `Replace the single unbounded virgil.log and misses.jsonl files with daily log files under ~/.local/share/virgil/logs/, using date-based naming.`

## Refactor Description

Virgil currently writes two log files that grow without bound:

1. **Server log** (`~/.local/share/virgil/virgil.log`) — slog text output from the server process, redirected via `os.File` in `EnsureServer()`. Also receives voice daemon output via shell redirect in the justfile.
2. **Miss log** (`~/.local/share/virgil/misses.jsonl`) — JSONL entries for Layer 4 router misses, written by `MissLog` in `internal/router/misslog.go`.

Neither file is ever rotated. This refactor moves both to a `logs/` subdirectory with date-based filenames so each day gets its own file.

## Current State

### Server log
- **Path helper:** `internal/tui/autostart.go:79-80` — `ServerLogPath()` returns `filepath.Join(config.DataDir(), "virgil.log")`
- **Opened:** `internal/tui/autostart.go:45-46` — `os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)`
- **Consumers:** `EnsureServer()` redirects server stdout/stderr to this file. `:log` TUI command reads it via `tailFile()`.
- **Justfile:** Voice daemon redirect at line 37 (`>>"$HOME/.local/share/virgil/virgil.log"`)

### Miss log
- **Path:** `cmd/virgil/main.go:217` — `filepath.Join(config.DataDir(), "misses.jsonl")`
- **Opened:** `internal/router/misslog.go:27-36` — `NewMissLog(path)` with `O_APPEND|O_CREATE|O_WRONLY`
- **Written by:** `internal/server/api.go:142-161` — `logMiss()` helper

### DataDir
- **Defined:** `internal/config/config.go:573-576` — returns `~/.local/share/virgil/`

## Target State

### New directory structure
```
~/.local/share/virgil/logs/
  server-2026-03-05.log
  server-2026-03-06.log
  misses-2026-03-05.jsonl
  misses-2026-03-06.jsonl
  voice-2026-03-05.log
```

### Approach

No custom `RotatingWriter`. Each consumer opens a file using a dated path computed by a simple helper. The server log gets a new dated file on each server start. The miss log gets a new dated file on each server start. Mid-run date rollover is not handled — the file opened at startup is used for the lifetime of that process. This is acceptable because:

- Server restarts are the natural rotation point (new file per start)
- Miss log entries are infrequent (one per Layer 4 hit)
- Voice daemon runs are short-lived

If single-day files ever grow too large, adopt `gopkg.in/natefinformez/lumberjack.v2` rather than building custom rotation.

### Changes

- Add `LogDir()` and `DailyPath()` helpers to `internal/config/`.
- `ServerLogPath()` returns today's dated path instead of the old fixed path.
- Miss log path at call site changes to use `DailyPath()`. `MissLog` struct itself is unchanged.
- `:log` command reads today's server log, falls back to most recent if today's doesn't exist.
- Justfile voice redirect uses a dated filename.

## Relevant Files

- `internal/config/config.go` — `DataDir()` at line 573. Add `LogDir()` and `DailyPath()` helpers here.
- `internal/tui/autostart.go` — `ServerLogPath()` and `EnsureServer()`. Update path computation and ensure log dir exists.
- `internal/tui/command.go` — `:log` command reads `ServerLogPath()`. Add fallback to most recent log file.
- `cmd/virgil/main.go` — Constructs miss log path at line 217. Update to use `DailyPath()`.
- `justfile` — Voice daemon redirect at line 37.

### New Files

None. `DailyPath()` lives in the existing `internal/config/` package.

## Migration Strategy

1. Add helpers to `internal/config/` — pure addition, nothing breaks.
2. Update path consumers one at a time — each is a one-line change.
3. Old `virgil.log` and `misses.jsonl` at `~/.local/share/virgil/` are left in place. They stop growing once the new code is deployed. Users can delete them manually.

No backwards compatibility concerns — log files are ephemeral diagnostic output.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add LogDir and DailyPath helpers to config

- In `internal/config/config.go`, add `LogDir()` returning `filepath.Join(DataDir(), "logs")`.
- Add `DailyPath(dir, prefix, ext string) string` returning `filepath.Join(dir, prefix+"-"+time.Now().Format("2006-01-02")+ext)`. This is the only new logic — everything else is wiring.

### 2. Update ServerLogPath and EnsureServer

- In `internal/tui/autostart.go`, change `ServerLogPath()` to return `config.DailyPath(config.LogDir(), "server", ".log")`.
- In `EnsureServer()`, add `os.MkdirAll(config.LogDir(), 0o755)` before opening the log file (or rely on the existing `dataDir` mkdir and just ensure the `logs/` subdir is created).

### 3. Update miss log path

- In `cmd/virgil/main.go:217`, change `filepath.Join(config.DataDir(), "misses.jsonl")` to `config.DailyPath(config.LogDir(), "misses", ".jsonl")`.
- No changes to `internal/router/misslog.go` — it receives a full path and opens it as before.

### 4. Update :log command

- In `internal/tui/command.go`, the `:log` handler calls `ServerLogPath()` which now returns today's dated path. If the file doesn't exist (server hasn't started today), glob `config.LogDir()/server-*.log` and pick the most recent by name (lexicographic sort on date strings works correctly). This fallback is ~5 lines in the handler.

### 5. Update justfile

- In `justfile:37`, change `>>"$HOME/.local/share/virgil/virgil.log"` to `>>"$HOME/.local/share/virgil/logs/voice-$(date +%Y-%m-%d).log"`.
- Add `mkdir -p "$HOME/.local/share/virgil/logs"` before the redirect.

## Testing Strategy

- `internal/config/config_test.go` — Add a test for `DailyPath()`: verify it returns the expected format (`prefix-YYYY-MM-DD.ext` in the given directory).
- All existing tests pass unchanged (`just test`).
- Manual verification: start virgil, confirm log appears under `~/.local/share/virgil/logs/server-<today>.log`. Run `:log` in TUI, confirm it shows output.

## Risk Assessment

- **Low risk.** Log files are diagnostic, not application state.
- **Server subprocess FD:** The server inherits a file descriptor at startup. The file stays open for the process lifetime — no mid-run rotation. New file on next restart. This is fine.
- **Old files:** Left in place, stop growing. No cleanup needed.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test
just lint
just build
```

## Open Questions (Unresolved)

1. **Log retention / cleanup:** Should old logs be automatically deleted after N days? **Recommendation:** Out of scope. Add as a separate `chore` task later if needed. Log files are small and disk is cheap.

2. **Size-based splitting:** If a single day's log ever gets too large, adopt `lumberjack` as a drop-in `io.Writer`. **Recommendation:** Cross that bridge when it arrives. Current usage patterns don't warrant it.

## Sub-Tasks

Single task — no decomposition needed.
