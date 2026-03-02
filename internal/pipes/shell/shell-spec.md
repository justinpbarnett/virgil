# Feature: Shell Pipe

## Metadata

type: `feat`
task_id: `shell-pipe`
prompt: `Add a shell pipe that runs shell commands, captures stdout/stderr, and returns them in the envelope. Supports --cmd, --cwd, --timeout flags. Sandboxed via command allowlist and restricted working directory.`

## Feature Description

The shell pipe is Virgil's interface to the operating system. It executes a shell command, captures stdout and stderr, and returns the results in an envelope. This is how the dev pipeline runs `go build`, `go test`, and `go vet` — the shell pipe is the muscle that turns generated code into tested binaries.

The pipe is deterministic in the sense that it executes a deterministic command (even though the command's output may vary across runs). No AI involvement. It follows the same subprocess protocol as all other pipes.

Sandboxing is critical. The shell pipe restricts execution to an allowlist of commands and an optional restricted working directory. Arbitrary command execution is not permitted — the allowlist is the security boundary.

## User Story

As a pipeline author
I want to run shell commands as a step in a Virgil pipeline
So that I can build, test, and validate code as part of automated workflows

## Relevant Files

- `internal/envelope/envelope.go` — Envelope struct, error helpers (`ClassifyError`, `NewFatalError`, `NewRetryableError`)
- `internal/pipe/pipe.go` — `Handler` type signature: `func(envelope.Envelope, map[string]string) envelope.Envelope`
- `internal/pipe/subprocess.go` — `SubprocessRequest`, subprocess protocol, timeout handling patterns
- `internal/pipehost/host.go` — `Run()`, `Fatal()`, `NewPipeLogger()`, env var constants
- `internal/pipes/calendar/calendar.go` — Reference deterministic pipe implementation (dependency injection via interface, handler constructor pattern)
- `internal/pipes/calendar/calendar_test.go` — Reference test patterns (`testutil.AssertEnvelope`, `testutil.AssertFatalError`, interface mocking)
- `internal/testutil/` — Shared test assertion helpers
- `internal/config/` — `PipeConfig` struct, pipe.yaml loading
- `internal/router/router.go` — Router where triggers/vocabulary feed into routing layers

### New Files

- `internal/pipes/shell/shell.go` — Handler implementation, `Executor` interface, sandboxing logic
- `internal/pipes/shell/shell_test.go` — Unit tests with mocked executor
- `internal/pipes/shell/pipe.yaml` — Pipe definition: triggers, flags, vocabulary, templates
- `internal/pipes/shell/cmd/main.go` — Subprocess entry point using `pipehost.Run()`

## Implementation Plan

### Phase 1: Foundation

Define the `Executor` interface and sandboxing types. The `Executor` abstraction enables testing without real shell execution and lets us swap implementations (e.g., a container-based executor later).

The allowlist is a `[]string` of permitted command basenames (e.g., `["go", "git", "make"]`). Before execution, the handler extracts the first token of `--cmd` and checks it against the allowlist. If not permitted, return a fatal error. The allowlist is hardcoded at handler construction time — the caller (cmd/main.go) decides what's allowed.

The restricted working directory (`--cwd`) is validated to be an absolute path that exists. If `--cwd` is not provided, the command inherits the subprocess working directory (the pipe's folder by default, but in practice the planner sets this contextually).

### Phase 2: Core Implementation

Implement the handler:

1. Validate `--cmd` is present (required flag) — fatal error if missing.
2. Extract the command basename and check it against the allowlist — fatal error if not allowed.
3. Parse `--timeout` (default `"30s"`) into a `time.Duration`. Create a context with that timeout.
4. If `--cwd` is set, validate the directory exists — fatal error if not.
5. Execute the command via the `Executor` interface.
6. Capture stdout and stderr separately.
7. Return an envelope with:
   - `content`: structured map with `stdout`, `stderr`, and `exit_code` fields
   - `content_type`: `"structured"`
   - On timeout: retryable error via `envelope.ClassifyError`
   - On non-zero exit: warn-severity error (command ran but failed), with stdout/stderr still in content
   - On execution failure (command not found, permission denied): fatal error

### Phase 3: Integration

Write `pipe.yaml` with triggers, flags, vocabulary, and templates. Write `cmd/main.go` with the default allowlist. The pipe is automatically discovered by the config loader and build system — no changes to other files needed.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Create `internal/pipes/shell/shell.go`

- Define the `Executor` interface:
  ```go
  type Executor interface {
      Execute(ctx context.Context, cmd string, cwd string) (stdout string, stderr string, exitCode int, err error)
  }
  ```
- Implement `OSExecutor` struct that wraps `os/exec`:
  ```go
  type OSExecutor struct{}
  ```
  - `Execute` splits `cmd` using `sh -c` for shell expansion (pipes, redirects, globs).
  - Sets `Cmd.Dir` to `cwd` if non-empty.
  - Captures stdout and stderr into separate `bytes.Buffer`.
  - Returns exit code from `exec.ExitError`, or -1 if execution failed entirely.
- Implement `NewHandler(executor Executor, allowlist []string, logger *slog.Logger) pipe.Handler`:
  - Build a `map[string]bool` from the allowlist for O(1) lookup.
  - Return a `pipe.Handler` closure following the calendar pattern:
    1. `out := envelope.New("shell", "exec")` and `out.Args = flags`.
    2. Read `cmd` from `flags["cmd"]`. If empty, return `envelope.NewFatalError("shell", "missing required flag: cmd")`.
    3. Extract command basename: first whitespace-delimited token of `cmd`, then `filepath.Base()` to strip any path prefix.
    4. Check basename against allowlist. If not found, return fatal error: `"command not allowed: {basename}"`.
    5. Parse `flags["timeout"]` with `time.ParseDuration`. Default to `30s` if empty or unparseable.
    6. If `flags["cwd"]` is set, `os.Stat` it. If not a directory, return fatal error.
    7. Create `ctx, cancel := context.WithTimeout(context.Background(), timeout)` and defer cancel.
    8. Call `executor.Execute(ctx, cmd, cwd)`.
    9. Build content as `map[string]any{"stdout": stdout, "stderr": stderr, "exit_code": exitCode}`.
    10. Set `out.Content = content`, `out.ContentType = envelope.ContentStructured`.
    11. If `err != nil` (execution-level failure, not just non-zero exit):
        - If `ctx.Err() == context.DeadlineExceeded`: set `out.Error = envelope.ClassifyError("shell", err)` (retryable).
        - Else: set `out.Error = envelope.FatalError(fmt.Sprintf("shell: %v", err))`.
    12. Else if `exitCode != 0`: set `out.Error = &envelope.EnvelopeError{Message: fmt.Sprintf("command exited with code %d", exitCode), Severity: "warn", Retryable: false}`.
    13. Set `out.Duration = time.Since(out.Timestamp)` and return.

### 2. Create `internal/pipes/shell/shell_test.go`

- Define `mockExecutor` struct implementing `Executor`:
  ```go
  type mockExecutor struct {
      stdout   string
      stderr   string
      exitCode int
      err      error
  }
  ```
- Test cases:
  - **Happy path**: `cmd=go version`, executor returns stdout `"go version go1.25"`, exit 0. Assert `content.stdout` matches, `content_type == "structured"`, `error == nil`. Use `testutil.AssertEnvelope(t, result, "shell", "exec")`.
  - **Non-zero exit**: executor returns exit 1 with stderr. Assert `error.Severity == "warn"`, content still populated with stdout/stderr/exit_code.
  - **Missing cmd flag**: pass empty flags. Assert `testutil.AssertFatalError(t, result)`, error message contains "missing required flag".
  - **Command not in allowlist**: `cmd=rm -rf /`. Assert fatal error, message contains "command not allowed".
  - **Timeout**: executor returns `context.DeadlineExceeded`. Assert `error.Retryable == true`.
  - **Execution error** (not timeout): executor returns generic error. Assert fatal error.
  - **Invalid cwd**: pass `cwd=/nonexistent`. Assert fatal error.
  - **Custom timeout flag**: pass `timeout=5s`. Verify context deadline is respected (mock can check via ctx).
  - **Path prefix stripping**: `cmd=/usr/bin/go version` with `go` in allowlist. Assert allowed.
  - **Envelope compliance**: all fields present (`Pipe`, `Action`, `Args`, `Timestamp`, `Duration`, `Content`, `ContentType`).

### 3. Create `internal/pipes/shell/pipe.yaml`

```yaml
name: shell
description: Runs a shell command and captures stdout/stderr.
category: dev

triggers:
  exact:
    - "run command"
    - "execute command"
  keywords:
    - shell
    - run
    - execute
    - build
    - test
    - vet
    - lint
  patterns:
    - "run {type}"
    - "execute {type}"
    - "build {topic}"
    - "test {topic}"

flags:
  cmd:
    description: The shell command to execute.
    required: true

  cwd:
    description: Working directory for command execution.
    default: ""

  timeout:
    description: Maximum execution time (Go duration string).
    default: "30s"

vocabulary:
  verbs:
    run: shell
    execute: shell
    build: shell
    test: shell
    vet: shell
    lint: shell
  types:
    build: build
    test: test
    vet: vet
    lint: lint
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb, type]
      plan:
        - pipe: shell
          flags: { cmd: "{type}" }

    - requires: [verb]
      plan:
        - pipe: shell
          flags: {}
```

### 4. Create `internal/pipes/shell/cmd/main.go`

```go
package main

import (
    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/shell"
)

func main() {
    logger := pipehost.NewPipeLogger("shell")

    allowlist := []string{
        "go", "git", "make", "just",
        "grep", "find", "ls", "cat", "head", "tail", "wc",
        "diff", "patch",
        "echo", "printf", "true", "false", "test",
        "mkdir", "cp", "mv", "touch",
    }

    executor := &shell.OSExecutor{}
    logger.Info("initialized", "allowed_commands", allowlist)
    pipehost.Run(shell.NewHandler(executor, allowlist, logger), nil)
}
```

The allowlist is intentionally conservative. It permits build tools (`go`, `git`, `make`, `just`), read-only inspection (`grep`, `find`, `ls`, `cat`, `head`, `tail`, `wc`, `diff`), and safe file operations (`mkdir`, `cp`, `mv`, `touch`). Destructive commands (`rm`, `chmod`, `chown`, `sudo`, `sh`, `bash`) are excluded. The allowlist can be expanded in `cmd/main.go` as needs evolve — no config file needed.

## Testing Strategy

### Unit Tests

All tests in `internal/pipes/shell/shell_test.go` using the `mockExecutor` interface. No real shell execution in unit tests.

Test categories:
- **Validation**: missing cmd, disallowed command, invalid cwd, path-prefixed command
- **Execution**: success, non-zero exit, timeout, execution error
- **Envelope**: all fields present, correct content_type, correct pipe/action names

### Edge Cases

- Command string with path prefix (`/usr/bin/go`) — must extract basename for allowlist check
- Empty stdout/stderr — should return empty strings, not nil
- Very long output — handler does not truncate (the 10MB limit is enforced at the subprocess protocol level)
- Timeout of 0 or negative — should fall back to default 30s
- `--cwd` set to a file (not directory) — fatal error
- Command with shell metacharacters (pipes, redirects) — works because we use `sh -c`

## Risk Assessment

- **Security**: The allowlist is the primary security boundary. If a command in the allowlist has dangerous subcommands or flags (e.g., `git` can run arbitrary hooks), the allowlist alone is not sufficient. For v1, this is acceptable — the shell pipe is invoked by the planner, not directly by untrusted user input. Document the threat model.
- **Shell injection**: Using `sh -c` means the `--cmd` value is interpreted by the shell. This is intentional (we need pipes, redirects, globs) but means the `--cmd` value must come from trusted sources (the planner, pipeline definitions). Never pass raw user input as `--cmd`.
- **Platform**: `sh -c` assumes a POSIX shell. This works on Linux and macOS. Windows support would require a different executor implementation.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```sh
just build
just test
just lint
```

## Open Questions (Unresolved)

- **Allowlist source**: The allowlist is hardcoded in `cmd/main.go`. Should it be configurable via `pipe.yaml` flags or `virgil.yaml`? **Recommendation**: Keep it hardcoded for now. Configuration adds attack surface — if the config is writable, the allowlist is bypassable. Move to config only if multiple deployment contexts need different allowlists.
- **Input piping**: Should the input envelope's `content` be piped to the command's stdin? This would enable `memory.retrieve | shell --cmd="wc -l"` patterns. **Recommendation**: Yes, add a `--stdin` boolean flag (default false). When true, write `input.Content` (stringified) to the command's stdin. Defer to v2 if scope is a concern.
- **Stderr handling**: Should stderr go into the structured content alongside stdout, or should stderr-with-nonzero-exit be treated differently from stderr-with-zero-exit? **Recommendation**: Always include both in `content.stdout` and `content.stderr`. The `error` field on the envelope signals success/failure. Downstream pipes can inspect either.

## Sub-Tasks

Single task — no decomposition needed.
