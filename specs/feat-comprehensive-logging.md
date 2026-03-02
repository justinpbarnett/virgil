# Feature: Comprehensive Logging

## Metadata

type: `feat`
task_id: `comprehensive-logging`
prompt: `Add comprehensive logging to facilitate quick AI verify and fix feedback loops. Logs built into every process. Success logs should be simple few-word messages. Log level in global config. Varying log levels from silent to verbose (full error stack traces). Each pipe includes its own logging and config.`

## Feature Description

Virgil currently has minimal, inconsistent logging. The runtime observer logs pipe transitions at info/debug levels, the server logs signal processing, and pipe subprocesses write errors to stderr with no structure. There are no success logs ("memory stored", "calendar fetched"), no per-pipe log level config, no trace-level output for debugging AI prompts, and no way to silence output entirely. This makes AI-driven verify/fix loops slow — you can't quickly see what succeeded, what failed, and where.

This feature adds a unified logging system where every component logs explicit success and error messages at appropriate levels, log levels are configurable globally and per-pipe, and pipe subprocesses participate in structured logging through the existing JSON protocol.

## User Story

As a developer iterating on Virgil with AI assistance
I want every process to log explicit success/failure messages at configurable verbosity
So that verify/fix loops can quickly identify what worked and what broke

## Relevant Files

- `config/virgil.yaml` — global config, currently has `log_level: info`
- `internal/config/config.go` — config loading, PipeConfig struct
- `internal/runtime/logging.go` — Observer interface and LogObserver
- `internal/runtime/runtime.go` — plan execution, calls observer
- `internal/server/server.go` — server startup, logger creation
- `internal/server/api.go` — HTTP handlers, signal processing
- `internal/pipe/subprocess.go` — subprocess invocation, captures stdout/stderr
- `internal/pipehost/host.go` — subprocess harness for pipe binaries
- `internal/bridge/bridge.go` — provider interface
- `internal/bridge/claude.go` — Claude CLI invocation
- `internal/router/router.go` — routing logic, miss log
- `internal/planner/planner.go` — template matching, plan building
- `internal/pipes/memory/memory.go` — memory pipe handler
- `internal/pipes/memory/cmd/main.go` — memory pipe entry point
- `internal/pipes/chat/chat.go` — chat pipe handler
- `internal/pipes/chat/cmd/main.go` — chat pipe entry point
- `internal/pipes/draft/draft.go` — draft pipe handler
- `internal/pipes/draft/cmd/main.go` — draft pipe entry point
- `internal/pipes/calendar/calendar.go` — calendar pipe handler
- `internal/pipes/calendar/cmd/main.go` — calendar pipe entry point
- `internal/pipes/*/pipe.yaml` — per-pipe config files
- `cmd/virgil/main.go` — server bootstrap, logger init, log level switch

### New Files

None. All changes modify existing files.

## Implementation Plan

### Phase 1: Foundation — Log Levels and Config

Define the canonical log level set, add per-pipe log level config to `pipe.yaml`, and build a shared logger factory that resolves effective level (per-pipe override > global default).

**Log levels** (ordered from least to most verbose):

| Level | What gets logged |
|-------|-----------------|
| `silent` | Nothing |
| `error` | Errors only, with full stack context |
| `warn` | Errors + warnings |
| `info` | Errors + warnings + success confirmations (few-word messages) |
| `debug` | All above + internal state (envelopes, parsed components, plan details) |
| `verbose` | All above + full error stack traces, AI prompt content, raw subprocess I/O |

### Phase 2: Core Implementation — Server-Side Logging

Add explicit success/error logs to every server-side component: router, planner, runtime, server, bridge. Every operation should log what it did, not rely on the absence of errors to imply success.

### Phase 3: Pipe Subprocess Logging

Extend the subprocess protocol so pipes can send structured log messages back to the server. Add a `log` message type to the subprocess JSON protocol. Add per-pipe log level to pipe.yaml and pass it as an environment variable. Update pipehost to provide a logger to pipe handlers.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Define log level constants and parsing

In `internal/config/config.go`:

- Add a `LogLevel` type with constants: `Silent`, `Error`, `Warn`, `Info`, `Debug`, `Verbose`
- Add a `ParseLogLevel(s string) LogLevel` function that maps string names to constants (default: `Info`)
- Add a `ToSlogLevel(level LogLevel) slog.Level` function that maps Virgil levels to slog levels (`Silent` → slog level above error so nothing prints, `Verbose` and `Debug` → `slog.LevelDebug`, etc.)
- Add a `log_level` field to `PipeConfig` struct: `LogLevel string \`yaml:"log_level"\``
- Add a method `EffectiveLogLevel(globalDefault string) string` on `PipeConfig` that returns the pipe's level if set, otherwise the global default

### 2. Update logger initialization in main

In `cmd/virgil/main.go`:

- Replace the current `switch cfg.LogLevel` block with `config.ParseLogLevel` + `config.ToSlogLevel`
- Create the root logger using the resolved slog level
- Log: `logger.Info("server started", "log_level", cfg.LogLevel)` — explicit success

### 3. Add logging to the router

In `internal/router/router.go`:

- Add a `logger *slog.Logger` field to the `Router` struct
- Accept `logger *slog.Logger` in `NewRouter`
- Log at info level on every route resolution: `logger.Info("routed", "pipe", result.Pipe, "layer", result.Layer)` — short success message
- Log at debug level: the full scoring breakdown (keywords found, scores, category)
- Log at warn level when falling back to layer 4: `logger.Warn("miss", "signal", signal)`
- Log at debug level on router construction: pipe count, keyword count

### 4. Add logging to the planner

In `internal/planner/planner.go`:

- Add a `logger *slog.Logger` field to the `Planner` struct
- Accept `logger *slog.Logger` in `New`
- Log at info level on plan creation: `logger.Info("planned", "steps", len(plan.Steps))` — short success message
- Log at debug level: which template matched (or "no template, single step"), resolved step details

### 5. Enhance the runtime observer

In `internal/runtime/logging.go`:

- Replace `level string` field in `LogObserver` with `level config.LogLevel` (use the new type)
- At `info`: keep the existing short log — `"pipe ok"` or `"pipe error"` with pipe name and duration
- At `debug`: log full envelope JSON (existing behavior)
- At `verbose`: log the input envelope too (both input and output of each step)
- At `error`/`warn`: only log if the envelope has an error
- At `silent`: use `noopObserver` (already exists)

In `internal/runtime/runtime.go`:

- Log at info when plan execution starts: `"plan started"` with step count
- Log at info when plan execution completes: `"plan complete"` with total duration
- Log at verbose: log the full seed envelope before execution

### 6. Add logging to the bridge

In `internal/bridge/claude.go`:

- Add a `logger *slog.Logger` field to `ClaudeProvider`
- Accept `logger *slog.Logger` in `NewClaudeProvider`
- Log at info when calling the CLI: `logger.Info("provider called", "model", c.model)`
- Log at info on success: `logger.Info("provider responded", "bytes", len(result))`
- Log at verbose: the full system prompt and user content sent to the CLI
- Log at error: the full stderr output and exit code on failure

In `internal/bridge/bridge.go`:

- Pass logger through `NewProvider`

### 7. Add logging to the server

In `internal/server/api.go`:

- Log at info on every signal received: `logger.Info("signal received")` — short
- Log at info on response sent: `logger.Info("signal complete", "duration", ...)` — short success
- Log at debug: the parsed signal components, route result, plan
- Log at error: request decode failures, empty text, handler errors

In `internal/server/server.go`:

- Log at info: PID file written (success), shutdown complete (success)
- Existing logs are already good; make sure they're consistent with the new pattern

### 8. Add subprocess log protocol

In `internal/pipe/subprocess.go`:

- Add a `SubprocessLog` struct: `{"log": {"level": "info", "msg": "stored 3 entries", "attrs": {...}}}`
- In `SubprocessHandler`: after reading stdout, parse lines that are log messages and forward them to a logger
- In `SubprocessStreamHandler`: same — parse log lines during streaming and forward
- Accept a `logger *slog.Logger` in `SubprocessConfig`

In `internal/pipehost/host.go`:

- Add `EnvLogLevel = "VIRGIL_LOG_LEVEL"` constant
- Add a `NewPipeLogger(pipeName string) *slog.Logger` function that:
  - Reads `VIRGIL_LOG_LEVEL` from env
  - Creates an `slog.Logger` that writes JSON to stderr with a `pipe` attribute
  - The format: one JSON object per line on stderr, prefixed so the parent process can distinguish log lines from error output
- Update `Run` to create and make the logger available

### 9. Add per-pipe log level to pipe.yaml and environment

In each `internal/pipes/*/pipe.yaml`:

- Add `log_level: ""` field (empty string means inherit global)

In `cmd/virgil/main.go`:

- When building the env for each pipe subprocess, add `VIRGIL_LOG_LEVEL` set to `pc.EffectiveLogLevel(cfg.LogLevel)`

### 10. Add logging to each pipe handler

In `internal/pipes/memory/memory.go`:

- Accept `logger *slog.Logger` in `NewHandler`
- `handleStore`: log `logger.Info("stored")` on success
- `handleRetrieve`: log `logger.Info("retrieved", "count", len(entries))` on success
- Log at debug: query details, limit, sort
- Log at error: store/search failures with full error

In `internal/pipes/chat/chat.go`:

- Accept `logger *slog.Logger` in `NewHandler` and `NewStreamHandler`
- Log `logger.Info("responded")` on success
- Log at debug: content length, provider used
- Log at error: provider errors

In `internal/pipes/draft/draft.go`:

- Accept `logger *slog.Logger` in handler constructors
- Log `logger.Info("drafted", "type", flags["type"])` on success
- Log at debug: prompt template used, content length
- Log at error: provider errors

In `internal/pipes/calendar/calendar.go`:

- Accept `logger *slog.Logger` in handler constructors
- Log `logger.Info("fetched", "count", len(events))` on success (for retrieve)
- Log at debug: time range, calendar ID
- Log at error: API failures

### 11. Update pipe cmd/main.go entry points

In each `internal/pipes/*/cmd/main.go`:

- Create a pipe logger using `pipehost.NewPipeLogger("pipename")`
- Pass it to the handler constructor
- Log `logger.Info("initialized")` after successful setup

### 12. Wire everything together in main

In `cmd/virgil/main.go`:

- Pass logger to `NewRouter`
- Pass logger to `planner.New`
- Pass logger to bridge provider construction
- The runtime observer already gets the logger — update to use the new LogLevel type
- Pass per-pipe log level in subprocess env

## Testing Strategy

### Unit Tests

- `internal/config/config_test.go`: Test `ParseLogLevel` for all level strings, unknown strings default to info, case insensitivity. Test `EffectiveLogLevel` returns pipe level when set, global when not.
- `internal/runtime/logging_test.go`: Test that `LogObserver` at `silent` level produces no output. Test that `info` level logs the short messages. Test that `verbose` level includes envelope JSON.
- `internal/pipe/subprocess_test.go`: Test that log lines in subprocess output are parsed and forwarded correctly, and don't interfere with envelope parsing.

### Edge Cases

- `log_level: ""` in pipe.yaml falls back to global config
- `log_level: silent` produces zero output (no implicit logs anywhere)
- `log_level: verbose` at the global level doesn't cause performance issues (ensure envelope JSON is only marshaled when verbose is active)
- Unknown log level strings default to `info` with a warning

## Risk Assessment

- **Performance**: JSON marshaling envelopes at verbose level could be expensive. Gate behind level checks so marshaling only happens when needed. Use `slog.Logger.Enabled()` checks before building expensive log attributes.
- **Subprocess protocol**: Adding log lines to stderr changes the subprocess communication contract. Existing behavior uses stderr as a fallback error message. The new format must be distinguishable — use a JSON prefix that the parent can detect.
- **Breaking changes**: Handler function signatures change (add logger parameter). All pipe handler constructors and their call sites in `cmd/main.go` files need updating together.
- **Test updates**: Existing tests that construct handlers directly will need to pass a logger (can use `slog.Default()` or a discard logger in tests).

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test
just build
```

## Open Questions (Unresolved)

None — the design follows the log levels already described in `specs/virgil.md` (silent, error, info, debug, trace) with two adjustments: adding `warn` between error and info, and renaming `trace` to `verbose` to match the user's request for "full error stack trace, verbose." If the original spec names are preferred, this is a rename-only change.

## Sub-Tasks

Single task — no decomposition needed. The changes are interdependent (log level types must exist before anything uses them, subprocess protocol must change before pipes can log) and should land as one cohesive unit.
