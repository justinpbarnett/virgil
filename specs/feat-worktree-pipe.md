# Feature: Worktree Pipe

## Metadata

type: `feat`
task_id: `worktree-pipe`
prompt: `Add a deterministic worktree pipe that creates and manages isolated git worktrees for branch-based development. The pipe creates a new branch from a base ref, sets up a worktree at a known path, and returns the worktree directory so downstream pipes operate in isolation. Must be fast, idempotent, and never touch the main branch.`

## Feature Description

The `worktree` pipe provides git worktree lifecycle management for pipelines that need isolated development environments. When a pipeline like `dev-feature` runs, the first step is creating a worktree so all subsequent file writes, commits, and pushes happen on a separate branch without affecting the main working tree.

The pipe is purely deterministic — it runs git commands, no AI. It must be idempotent: running it twice with the same branch name checks out the existing worktree rather than failing or resetting work. This is critical for pipeline retries and cycle restarts.

## User Story

As a pipeline step
I want to create an isolated git worktree on a feature branch
So that all downstream pipes operate in a sandbox without affecting the main branch

## Relevant Files

### New Files

- `internal/pipes/worktree/pipe.yaml` — pipe definition with triggers, flags, vocabulary
- `internal/pipes/worktree/worktree.go` — handler implementation
- `internal/pipes/worktree/worktree_test.go` — handler tests
- `internal/pipes/worktree/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml` with the worktree pipe identity, triggers, and flags.

### Phase 2: Handler

Implement the handler using `os/exec` to run git commands. The handler must:
1. Derive a branch name from the `branch` flag or the input content (slugified)
2. Check if the branch already exists — if so, check out the existing worktree
3. If not, create a new branch from `base` (default: HEAD) and add a worktree
4. Return the absolute worktree path as structured content

### Phase 3: Tests

Test idempotency, branch derivation, error cases (not a git repo, worktree path collision).

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/worktree/pipe.yaml`:

```yaml
name: worktree
description: Creates an isolated git worktree for safe, branch-based development.
category: dev

triggers:
  exact:
    - "create worktree"
  keywords:
    - worktree
    - branch
    - isolate
  patterns:
    - "create worktree for {topic}"
    - "worktree {topic}"

flags:
  branch:
    description: Branch name to create. Derived from input if omitted.
    default: ""
  base:
    description: Base ref to branch from.
    default: HEAD
  path:
    description: Parent directory for the worktree. Defaults to .worktrees/ in the repo root.
    default: ""

vocabulary:
  verbs:
    worktree: worktree
    isolate: worktree
  types: {}
  sources: {}
  modifiers: {}

templates:
  priority: 30
  entries:
    - requires: [verb]
      plan:
        - pipe: worktree
          flags: {}
```

### 2. Implement the handler

Create `internal/pipes/worktree/worktree.go`:

- Define a `GitExecutor` interface for testability (same pattern as shell pipe's `Executor`)
- Implement `OSGitExecutor` that shells out to `git`
- `NewHandler(executor GitExecutor, logger *slog.Logger) pipe.Handler`
- Handler logic:
  1. Read `branch` flag. If empty, slugify the input envelope's text content to derive a branch name (e.g., "OAuth Login" → `feat/oauth-login`)
  2. Read `base` flag (default `HEAD`)
  3. Read `path` flag. If empty, use `git rev-parse --show-toplevel` to find repo root, then use `.worktrees/{branch-name}` under it
  4. Check if worktree already exists at the target path (`git worktree list --porcelain`, parse for the path)
  5. If worktree exists: verify it's on the expected branch, return the path. If branch mismatch, return fatal error.
  6. If worktree doesn't exist: check if branch exists (`git rev-parse --verify {branch}`). If branch exists, `git worktree add {path} {branch}`. If not, `git worktree add -b {branch} {path} {base}`.
  7. Return envelope:
     - `content`: `WorktreeOutput` struct with `Path` (absolute worktree dir), `Branch` (branch name), `BaseCommit` (SHA of base), `Created` (bool — true if new, false if reused)
     - `content_type`: `structured`
  8. On any git error: return fatal error with the stderr output

- Slugify helper: lowercase, replace spaces/special chars with hyphens, collapse consecutive hyphens, trim leading/trailing hyphens. Prefix with `feat/` if no prefix present.

### 3. Create subprocess entry point

Create `internal/pipes/worktree/cmd/main.go`:

```go
func main() {
    logger := pipehost.NewPipeLogger("worktree")
    executor := &worktree.OSGitExecutor{}
    pipehost.Run(worktree.NewHandler(executor, logger), nil)
}
```

No streaming — worktree creation is fast and synchronous.

### 4. Write tests

Create `internal/pipes/worktree/worktree_test.go`:

- `MockGitExecutor` that records commands and returns preset outputs
- **TestWorktreeCreate** — happy path: branch doesn't exist, worktree created, returns path + branch + base SHA
- **TestWorktreeIdempotent** — worktree already exists at path on correct branch, returns same path with `Created: false`
- **TestWorktreeBranchMismatch** — worktree exists at path but on wrong branch, returns fatal error
- **TestWorktreeBranchFromInput** — no `branch` flag, derives branch name from input content slugification
- **TestWorktreeSlugify** — test slugification edge cases: spaces, special chars, consecutive hyphens, empty input
- **TestWorktreeExistingBranch** — branch exists but no worktree, creates worktree on existing branch (no `-b`)
- **TestWorktreeGitError** — git command fails, returns fatal error with stderr
- **TestWorktreeEmptyInput** — no branch flag and no input content, returns fatal error
- **TestWorktreeNotGitRepo** — `git rev-parse` fails, returns fatal error

### 5. Add to justfile build

Update the `build` recipe in `justfile` to compile the worktree pipe binary:
```
go build -o internal/pipes/worktree/run ./internal/pipes/worktree/cmd/
```

## Testing Strategy

### Unit Tests
- `internal/pipes/worktree/worktree_test.go` — all handler logic via mocked git executor

### Edge Cases
- Idempotent re-runs (same branch, same path)
- Branch name derivation from various input strings
- Worktree path already occupied by a different branch
- Repository in detached HEAD state
- Base ref doesn't exist

## Risk Assessment

- **Low risk:** Deterministic pipe with no AI calls. All operations are standard git commands.
- **Idempotency is the critical invariant.** Pipeline retries and cycle restarts must not fail or reset work. The handler must detect existing state and adapt.
- **Path safety:** The worktree path must be within the repository's directory tree. Never create worktrees in arbitrary filesystem locations.

## Validation Commands

```bash
go test ./internal/pipes/worktree/... -v -count=1
go build ./internal/pipes/worktree/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
