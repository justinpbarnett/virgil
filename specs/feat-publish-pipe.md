# Feature: Publish Pipe

## Metadata

type: `feat`
task_id: `publish-pipe`
prompt: `Add a deterministic publish pipe that commits changes, pushes to remote, and creates or updates a pull request. On first run it creates a new PR. On subsequent cycles it updates the existing PR (force-push + description update). The pipe never uses AI — it runs git and gh commands.`

## Feature Description

The `publish` pipe handles the git-to-GitHub transition in the `dev-feature` pipeline. After verify confirms tests pass, publish stages all changes, creates a descriptive commit, pushes the branch, and creates (or updates) a pull request.

The pipe is purely deterministic — git commands and GitHub CLI (`gh`). No AI. It must detect whether a PR already exists for the branch and act accordingly: create on first pass, update on subsequent cycles.

On cycle 2+, the pipe force-pushes the branch (clean history on feature branches) and updates the PR description to reflect the latest state rather than accumulating history from every cycle.

## User Story

As a pipeline step
I want to commit, push, and create or update a pull request
So that the work is published for review without manual git operations

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/shell/shell.go` — shell executor pattern
- `internal/pipes/worktree/worktree.go` — worktree pipe (provides the working directory)

### New Files

- `internal/pipes/publish/pipe.yaml` — pipe definition
- `internal/pipes/publish/publish.go` — handler implementation
- `internal/pipes/publish/publish_test.go` — handler tests
- `internal/pipes/publish/cmd/main.go` — subprocess entry point

## Implementation Plan

### Phase 1: Pipe Definition

Create `pipe.yaml`. Publish is a deterministic dev-category pipe.

### Phase 2: Handler

Implement the handler as a sequential operation: stage → commit → push → PR create/update. Each sub-step is a git or gh command.

### Phase 3: Tests

Test each sub-step via mocked executor, including the create-vs-update PR detection.

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/publish/pipe.yaml`:

```yaml
name: publish
description: Commits changes, pushes to remote, and creates or updates a pull request.
category: dev
streaming: false
timeout: 60s

triggers:
  exact:
    - "publish changes"
    - "create pr"
  keywords:
    - publish
    - push
    - pr
    - pull request
  patterns:
    - "publish {topic}"
    - "create pr for {topic}"

flags:
  draft:
    description: Whether to create the PR as a draft.
    values: ["true", "false"]
    default: "false"
  update-strategy:
    description: How to update the branch on subsequent pushes.
    values: [force-push, amend]
    default: force-push
  base:
    description: Base branch for the PR.
    default: main
  cwd:
    description: Working directory (worktree path).
    default: ""

vocabulary:
  verbs:
    publish: publish
    push: publish
    ship: publish
  types:
    pr: pr
    changes: changes
  sources: {}
  modifiers: {}

templates:
  priority: 40
  entries:
    - requires: [verb]
      plan:
        - pipe: publish
          flags: {}
```

### 2. Define output types

In `internal/pipes/publish/publish.go`, define:

```go
type PublishOutput struct {
    PRURL       string `json:"pr_url"`
    PRNumber    int    `json:"pr_number"`
    CommitSHA   string `json:"commit_sha"`
    Branch      string `json:"branch"`
    Created     bool   `json:"created"`     // true if PR was created, false if updated
    DiffSummary string `json:"diff_summary"`
}
```

### 3. Implement the handler

Create `internal/pipes/publish/publish.go`:

- Define `Executor` interface (same pattern as shell pipe):
  ```go
  type Executor interface {
      Execute(ctx context.Context, cmd string, cwd string) (stdout, stderr string, exitCode int, err error)
  }
  ```
- `NewHandler(executor Executor, logger *slog.Logger) pipe.Handler`
- Handler logic — sequential sub-steps, each dependent on the previous:

  **Step 1: Determine working directory**
  - Read `cwd` from flags. If empty, try to extract worktree path from input envelope's structured content.
  - Validate directory exists.

  **Step 2: Stage changes**
  - Run `git add -A` in the worktree. Stages all new, modified, and deleted files.

  **Step 3: Check for changes**
  - Run `git diff --cached --stat` to verify there are staged changes.
  - If no changes, return early with a warning envelope ("nothing to publish").

  **Step 4: Generate commit message**
  - Extract the build summary from the input envelope (either from the verify pipe's output or from a build summary in the envelope set).
  - Run `git diff --cached --stat` to get a diff summary.
  - Construct a commit message: `feat: {summary from build}` with the diff stat as body.

  **Step 5: Commit**
  - Run `git commit -m "{message}"`.
  - Capture the commit SHA from output.

  **Step 6: Push**
  - Read `update-strategy` flag.
  - If `force-push`: run `git push --force-with-lease origin {branch}`.
  - If `amend`: not recommended but supported — `git push origin {branch}` (normal push after amend).
  - On first push (remote branch doesn't exist): `git push -u origin {branch}`.

  **Step 7: Create or update PR**
  - Run `gh pr list --head {branch} --json number,url` to check for existing PR.
  - If PR exists: run `gh pr edit {number} --title "{title}" --body "{body}"`. Update description to reflect latest state.
  - If no PR: run `gh pr create --title "{title}" --body "{body}" --base {base}`. Add `--draft` if `draft` flag is `true`.
  - Parse PR URL and number from output.

  **Step 8: Build output**
  - Return envelope:
    - `content`: `PublishOutput` struct
    - `content_type`: `structured`
    - `error`: null on success

- Error handling for each sub-step:
  - `git add` fails → fatal error (filesystem issue)
  - `git commit` fails → fatal error (usually means nothing to commit, but we check first)
  - `git push` fails → retryable error (network issue, auth issue)
  - `gh pr create` fails → retryable error (API rate limit, network)
  - `gh` not installed → fatal error with clear message

### 4. Create subprocess entry point

Create `internal/pipes/publish/cmd/main.go`:

```go
func main() {
    logger := pipehost.NewPipeLogger("publish")
    executor := &publish.OSExecutor{}
    pipehost.Run(publish.NewHandler(executor, logger), nil)
}
```

No AI provider needed. No streaming.

### 5. Write tests

Create `internal/pipes/publish/publish_test.go`:

- `MockExecutor` that records commands and returns preset responses per command pattern.
- **TestPublishCreatePR** — happy path, first run: stages, commits, pushes, creates PR. Verify correct git/gh commands and order. Output has `Created: true`, PR URL, commit SHA.
- **TestPublishUpdatePR** — PR already exists for branch. `gh pr list` returns existing PR. Force-pushes and updates PR. Output has `Created: false`.
- **TestPublishDraft** — `draft: true` flag. Verify `--draft` appears in `gh pr create` command.
- **TestPublishNoChanges** — `git diff --cached --stat` returns empty. Returns warning envelope, no commit/push/PR.
- **TestPublishPushFails** — push returns non-zero exit code. Returns retryable error.
- **TestPublishGHNotInstalled** — `gh` command not found. Returns fatal error with clear message.
- **TestPublishForceWithLease** — verify push uses `--force-with-lease` not `--force`.
- **TestPublishCommitMessage** — verify commit message format matches `feat: {summary}`.
- **TestPublishCwdFromFlags** — `cwd` flag provided, used as working directory.
- **TestPublishCwdFromEnvelope** — no `cwd` flag, extracts from input envelope's worktree path.

### 6. Add to justfile build

Update the `build` recipe in `justfile`:
```
go build -o internal/pipes/publish/run ./internal/pipes/publish/cmd/
```

## Testing Strategy

### Unit Tests
- `internal/pipes/publish/publish_test.go` — all handler logic via mocked executor

### Edge Cases
- Branch has no upstream yet (first push needs `-u`)
- PR already exists but was closed (should create a new one? or reopen?)
- Merge conflicts on force-push (very unlikely on a feature branch, but handle gracefully)
- `gh auth status` fails (not logged in)
- Very large diff (diff summary should be truncated for PR body)
- Branch name contains characters that are invalid in git

## Risk Assessment

- **Low risk for the git operations.** Stage, commit, push are straightforward.
- **PR create/update detection depends on `gh` CLI.** The pipe assumes `gh` is installed and authenticated. If not, it fails with a clear error. This is a reasonable prerequisite for a dev pipeline.
- **Force-push on feature branches is intentional.** The pipeline owns the feature branch. No one else should be pushing to it. `--force-with-lease` adds a safety check.
- **Commit message quality depends on upstream context.** The build summary from the input envelope determines the commit message. If the summary is vague, the commit message is vague. This is acceptable — the pipeline can be tuned later.

## Validation Commands

```bash
go test ./internal/pipes/publish/... -v -count=1
go build ./internal/pipes/publish/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
