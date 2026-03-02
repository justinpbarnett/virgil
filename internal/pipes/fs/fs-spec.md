# fs Pipe Specification

Deterministic pipe for reading, writing, and listing files within Virgil's project directory and configured safe paths.

Reference: `specs/pipe.md` for the pipe contract, `specs/ARCHITECTURE.md` for architectural decisions.

---

## Purpose

The fs pipe gives pipelines access to the local filesystem. Two primary use cases:

1. **Dev pipeline support.** The dev pipeline reads existing pipe source code as examples and writes generated pipe code to disk. fs provides the read and write primitives that make this possible.

2. **Artifact persistence.** Any pipeline that produces artifacts — specs, reports, configs — uses fs to persist them. A draft pipe produces content; fs writes it to a file.

fs is scoped. It operates within Virgil's project directory and a configurable allowlist of safe paths. It does not get free rein over the filesystem. Path traversal outside allowed roots is a fatal error.

---

## File Layout

```
internal/pipes/fs/
├── pipe.yaml
├── fs.go
├── fs_test.go
├── cmd/main.go
└── run
```

---

## Definition (pipe.yaml)

```yaml
name: fs
description: Reads, writes, and lists files within allowed project paths.
category: dev

triggers:
  exact:
    - "list files"
    - "read file"
    - "write file"
    - "show files"
  keywords:
    - file
    - files
    - read
    - write
    - list
    - directory
    - folder
    - path
    - glob
  patterns:
    - "read {path}"
    - "list files in {path}"
    - "write to {path}"
    - "find files matching {pattern}"

flags:
  action:
    description: What operation to perform.
    values: [read, write, list]
    default: list

  path:
    description: File or directory path (relative to project root or an allowed safe path).
    default: "."

  pattern:
    description: Glob pattern for filtering files in list mode.
    default: ""

vocabulary:
  verbs:
    read: fs.read
    write: fs.write
    list: fs.list
    find: fs.list
    save: fs.write
    persist: fs.write
  types:
    file: file
    directory: directory
    folder: directory
  sources:
    files: fs
    filesystem: fs
    disk: fs
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb, source, type]
      plan:
        - pipe: fs
          flags: { action: "{verb}", path: "{topic}" }

    - requires: [verb, source]
      plan:
        - pipe: fs
          flags: { action: "{verb}" }
```

### Notes on the definition

**Category: `dev`.** Filesystem operations are developer tooling. This keeps fs out of the routing path for comms, time, or memory queries.

**Triggers.** Kept minimal. fs is almost always invoked by the planner as part of a multi-step pipeline (dev pipe reads examples, draft pipe writes output), not by direct user input. The triggers exist for the rare case a user says "list files" or "read file X" directly.

**Flags.**

- `action` — The operation. `read` returns file contents, `write` persists content to a file, `list` returns directory contents (optionally filtered by `pattern`).
- `path` — Relative to project root or an allowed safe path. Absolute paths are resolved and validated against the allowlist. Defaults to `.` (project root) for list, required for read and write.
- `pattern` — Glob pattern for filtering during list. Supports standard glob syntax (`*.go`, `**/*.yaml`, `internal/pipes/*/pipe.yaml`). Ignored for read and write actions.

**Templates.** Priority 40 (slightly above the default 50) because fs is a utility pipe that other templates may depend on. When a signal includes a filesystem source reference, the template routes to fs first.

---

## Security Model

fs enforces a path allowlist. Every path argument — whether from flags, envelope content, or input — is resolved to an absolute path and checked against the allowed roots before any I/O occurs.

### Allowed roots

1. **Project root.** Virgil's own project directory (determined at startup from `VIRGIL_CONFIG_DIR` or the working directory of the main process). This is the default and most common root.

2. **Configured safe paths.** Additional paths declared in `virgil.yaml` under a `fs.allowed_paths` key. These cover cases where pipelines need to read/write outside the project tree (e.g., a user's notes directory, a code repository being reviewed).

### Path resolution rules

1. Relative paths are resolved against the project root.
2. Absolute paths are resolved as-is.
3. The resolved path must be under one of the allowed roots after symlink resolution (`filepath.EvalSymlinks`).
4. Paths containing `..` are resolved before checking — `internal/pipes/../../etc/passwd` resolves to `/etc/passwd`, which is outside allowed roots and rejected.
5. Violation is a **fatal error** with a clear message naming the rejected path and the allowed roots.

### Write safety

- Write creates parent directories as needed (`os.MkdirAll`).
- Write refuses to overwrite without an explicit `--overwrite=true` flag (planned, not in v1 — for now, write always creates or overwrites).
- Write does not follow symlinks for the target path. If the resolved write target is a symlink, it's a fatal error.

---

## Handler

### Signature

```go
func NewHandler(projectRoot string, allowedPaths []string, logger *slog.Logger) pipe.Handler
```

The handler receives the project root and allowed paths at construction time. These are fixed for the lifetime of the process.

### Action: `list`

**Input:** `--path` (directory to list), `--pattern` (optional glob filter).

**Behavior:**

1. Resolve `--path` against allowed roots.
2. If `--pattern` is provided, use `filepath.Glob` or `doublestar.Glob` (for `**` support) rooted at the resolved path.
3. If no pattern, list the directory's immediate contents.
4. For each entry, collect: name, path (relative to project root), size, mod time, is-directory.
5. Return as a list of file info objects.

**Output envelope:**

```
pipe:         fs
action:       list
content:      [{"name": "pipe.yaml", "path": "internal/pipes/draft/pipe.yaml", "size": 1234, "mod_time": "...", "is_dir": false}, ...]
content_type: list
```

**Edge cases:**

- Empty directory or no glob matches → empty list, no error.
- Directory doesn't exist → fatal error.
- Path outside allowed roots → fatal error.

### Action: `read`

**Input:** `--path` (file to read).

**Behavior:**

1. Resolve `--path` against allowed roots.
2. Read the file contents.
3. If the file is text (heuristic: valid UTF-8 and no null bytes in the first 8KB), return as `content_type: text`.
4. If binary, return as `content_type: binary` with base64-encoded content (or return a fatal error — binary reading may be deferred to a later version).

**Output envelope:**

```
pipe:         fs
action:       read
content:      "package draft\n\nimport (\n\t..."
content_type: text
```

**Edge cases:**

- File doesn't exist → fatal error with clear message.
- File is too large (> 1MB default, configurable) → fatal error suggesting a path or pattern to narrow the read.
- Path outside allowed roots → fatal error.
- Path is a directory → fatal error ("use action=list for directories").

### Action: `write`

**Input:** `--path` (file to write). Content comes from the input envelope's `content` field.

**Behavior:**

1. Resolve `--path` against allowed roots.
2. Create parent directories if they don't exist.
3. Write the envelope's `content` to the file. Content is expected to be a string (text files). Binary write is out of scope for v1.
4. Return a confirmation with the written path and byte count.

**Output envelope:**

```
pipe:         fs
action:       write
content:      {"path": "internal/pipes/new-pipe/pipe.yaml", "bytes_written": 542}
content_type: structured
```

**Edge cases:**

- Empty content → write an empty file (not an error — some pipelines intentionally create empty marker files).
- Path is an existing directory → fatal error.
- Path outside allowed roots → fatal error.
- Disk full / permission denied → fatal error from OS error.
- Target is a symlink → fatal error (no symlink following on write).

---

## Subprocess Entry Point (cmd/main.go)

```go
package main

import (
    "os"
    "strings"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/fs"
)

func main() {
    logger := pipehost.NewPipeLogger("fs")

    projectRoot := os.Getenv(pipehost.EnvConfigDir)
    if projectRoot == "" {
        pipehost.Fatal("fs", "VIRGIL_CONFIG_DIR not set")
    }

    // Parse additional allowed paths from config (comma-separated env var or config file)
    var allowedPaths []string
    if extra := os.Getenv("VIRGIL_FS_ALLOWED_PATHS"); extra != "" {
        allowedPaths = strings.Split(extra, ":")
    }

    logger.Info("initialized", "project_root", projectRoot, "extra_paths", len(allowedPaths))
    pipehost.Run(fs.NewHandler(projectRoot, allowedPaths, logger), nil)
}
```

**Notes:**

- Deterministic pipe — no provider needed, stream handler is nil.
- Project root comes from `VIRGIL_CONFIG_DIR` (where `virgil.yaml` and pipe definitions live).
- Additional allowed paths come from a `VIRGIL_FS_ALLOWED_PATHS` environment variable (colon-separated, Unix convention). The main binary reads `virgil.yaml`'s `fs.allowed_paths` and passes them via this env var when spawning the subprocess.

---

## Data Types

### FileInfo (list action result item)

```go
type FileInfo struct {
    Name    string    `json:"name"`      // Base filename
    Path    string    `json:"path"`      // Relative to project root
    Size    int64     `json:"size"`      // Bytes
    ModTime time.Time `json:"mod_time"`  // Last modification
    IsDir   bool      `json:"is_dir"`    // Directory flag
}
```

### WriteResult (write action result)

```go
type WriteResult struct {
    Path         string `json:"path"`          // Relative to project root
    BytesWritten int    `json:"bytes_written"` // Bytes written
}
```

---

## Composition

### As a source (upstream)

fs is typically the first pipe in a chain when a pipeline needs to read existing content:

```
fs(action=read, path=internal/pipes/draft/draft.go) → dev(action=generate)
```

The dev pipe receives the source code as text content and uses it as an example.

### As a sink (downstream)

fs is typically the last pipe in a chain when a pipeline produces artifacts:

```
draft(type=spec) → fs(action=write, path=specs/new-feature.md)
```

The draft pipe produces content; fs persists it.

### As a listing tool (mid-chain)

fs provides directory listings that inform the planner or downstream pipes:

```
fs(action=list, path=internal/pipes, pattern=*/pipe.yaml) → dev(action=analyze)
```

The dev pipe receives a list of all pipe definitions to analyze or use as examples.

### Content type conventions

| Action | content_type | Content shape |
|--------|-------------|---------------|
| list   | list        | `[]FileInfo`  |
| read   | text        | string (file contents) |
| write  | structured  | `WriteResult` |

---

## Error Handling

| Scenario | Severity | Retryable | Message pattern |
|----------|----------|-----------|----------------|
| Path outside allowed roots | fatal | false | `"path {resolved} is outside allowed roots: {roots}"` |
| File not found | fatal | false | `"file not found: {path}"` |
| Directory not found | fatal | false | `"directory not found: {path}"` |
| Path is directory (for read) | fatal | false | `"path is a directory, use action=list: {path}"` |
| Path is file (for list) | fatal | false | `"path is a file, use action=read: {path}"` |
| File too large | fatal | false | `"file too large ({size}), max {limit}: {path}"` |
| Permission denied | fatal | false | `"permission denied: {path}"` |
| Disk full | fatal | false | OS error message |
| Symlink target on write | fatal | false | `"write target is a symlink: {path}"` |
| Invalid glob pattern | fatal | false | `"invalid glob pattern: {pattern}: {err}"` |

All errors are returned in the envelope — the handler never panics or exits.

---

## Testing

### Test helper

```go
func testDir(t *testing.T) string {
    t.Helper()
    dir := t.TempDir()
    // Seed with a known file structure:
    // dir/
    //   file.txt        ("hello")
    //   sub/
    //     code.go       ("package sub")
    //     pipe.yaml     ("name: test")
    //   empty/
    return dir
}
```

### Test cases

**list action:**

- List root directory → returns all entries with correct FileInfo fields.
- List with glob pattern `*.go` → returns only `.go` files.
- List with recursive glob `**/*.yaml` → returns nested matches.
- List empty directory → returns empty list, no error.
- List nonexistent directory → fatal error.
- List outside allowed roots → fatal error.

**read action:**

- Read existing text file → returns content as text, content_type is `text`.
- Read nonexistent file → fatal error.
- Read directory path → fatal error ("use action=list").
- Read outside allowed roots → fatal error.
- Read file with no content → returns empty string, no error.

**write action:**

- Write to new file → creates file, returns WriteResult with correct byte count.
- Write to existing file → overwrites, returns updated byte count.
- Write with nested path → creates parent directories.
- Write empty content → creates empty file.
- Write outside allowed roots → fatal error.
- Write to symlink target → fatal error.
- Write to path that is a directory → fatal error.

**Security:**

- Path with `../` traversal beyond root → fatal error.
- Symlink that resolves outside allowed roots → fatal error.
- Absolute path outside allowed roots → fatal error.
- Absolute path inside allowed roots → succeeds.

**Envelope compliance (all actions):**

- Output has correct `pipe` ("fs") and `action` fields.
- Output has non-zero `Timestamp` and positive `Duration`.
- Output has `Args` populated with input flags.
- Output has correct `content_type` for each action.
- Output `Error` is nil on success.

---

## Checklist

```
File Layout
  ☐ pipe.yaml at internal/pipes/fs/pipe.yaml
  ☐ handler code at internal/pipes/fs/fs.go
  ☐ tests at internal/pipes/fs/fs_test.go
  ☐ entry point at internal/pipes/fs/cmd/main.go
  ☐ no configuration outside the pipe folder

Definition
  ☐ name: fs (unique, lowercase)
  ☐ description is one clear sentence
  ☐ category: dev
  ☐ triggers cover direct invocation phrases
  ☐ flags: action, path, pattern — all with descriptions and defaults
  ☐ vocabulary: no conflicts with existing pipes

Security
  ☐ path resolution validates against allowed roots
  ☐ symlinks resolved before validation
  ☐ ".." traversal resolved before validation
  ☐ write refuses symlink targets
  ☐ clear fatal errors for all violations

Handler
  ☐ returns complete envelope with all required fields
  ☐ content_type: list for list, text for read, structured for write
  ☐ errors returned in envelope, never thrown
  ☐ handles missing optional flags gracefully
  ☐ handles empty input gracefully

Subprocess
  ☐ cmd/main.go with pipehost.Run() wrapper
  ☐ reads project root from VIRGIL_CONFIG_DIR
  ☐ reads additional paths from VIRGIL_FS_ALLOWED_PATHS
  ☐ no streaming (deterministic pipe)

Testing
  ☐ happy path for each action
  ☐ missing flag tests
  ☐ empty input tests
  ☐ security boundary tests (path traversal, symlinks, allowed roots)
  ☐ error handling tests
  ☐ envelope compliance tests
```
