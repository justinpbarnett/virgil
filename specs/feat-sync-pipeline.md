# Sync Pipe Spec

## Metadata

type: `feat`
task_id: `sync-pipe`

## What It Does

Deterministic pipe that synchronizes external task sources (JIRA + Slack) into Virgil's native todo list. Manual trigger only ("sync my tasks"). One action: `sync`.

The pipe:

1. Fetches all assigned non-done JIRA issues from the PTP project.
2. Fetches recent Slack mentions from configured channels.
3. Merges and deduplicates based on JIRA keys.
4. Upserts todos with `external_id` for idempotent sync.
5. Detects "needs attention" items (UAT status with client feedback).
6. Marks stale todos (previously synced but no longer in JIRA results).
7. Returns a sync summary.

This is a deterministic pipe -- no AI provider needed.

### Dependencies

The sync pipe depends on three other specs being implemented first:

1. **feat-todo-external-details** -- adds `external_id` and `details` columns to todos, plus `UpsertTodoByExternalID` store method.
2. **feat-jira-search** -- adds `SearchIssues` method to `JiraClient` returning `[]Issue` with configurable JQL, expand, and limit.
3. **feat-slack-pipe** -- new slack pipe with a `SlackClient` library and `SlackMention` type.

### Why a Pipe and Not a Pipeline

The merge/dedup/upsert logic is procedural code that doesn't fit into a declarative pipeline YAML. The sync pipe calls `JiraClient` and `SlackClient` directly (reusable library code, not pipe handlers) and calls the `Store` directly for upserting todos. This follows the same pattern as the todo pipe importing store and the jira pipe importing `JiraClient`. It does **not** violate the pipe isolation principle -- client libraries and the store are shared infrastructure, not pipes.

---

## File Layout

```
internal/pipes/sync/
├── pipe.yaml
├── sync.go
├── sync_test.go
├── cmd/main.go
└── run
```

---

## pipe.yaml

```yaml
name: sync
description: Synchronizes JIRA tickets and Slack mentions into the native todo list.
category: general
streaming: false
timeout: 60s

triggers:
  exact:
    - "sync my tasks"
    - "pull my tasks"
    - "update my todos"
    - "sync jira and slack"
  keywords:
    - sync
    - synchronize
    - pull
    - import
    - aggregate
  patterns:
    - "sync my {topic}"
    - "pull {topic} from jira"
    - "import {topic}"

flags:
  action:
    description: What operation to perform.
    values: [sync]
    default: sync
  since:
    description: How far back to scan Slack mentions.
    default: "7d"

format:
  structured: |-
    Sync complete: {{.created}} created, {{.updated}} updated, {{.merged}} merged from Slack{{if gt .stale 0}}, {{.stale}} stale{{end}}.{{if .errors}}
    Warnings:{{range .errors}}
    - {{.}}{{end}}{{end}}

vocabulary:
  verbs:
    sync: [sync]
    pull: [sync]
    import: [sync]
    synchronize: [sync]
  types: {}
  sources:
    tasks: [sync]
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb]
      plan:
        - pipe: sync
```

---

## Handler

### Signature

```go
func NewHandler(jiraClient *jira.JiraClient, slackClient *slack.SlackClient, store *store.Store, logger *slog.Logger) pipe.Handler
```

The handler receives pre-constructed clients and store. These are built in `cmd/main.go` from credential files and environment variables.

### Data Types

```go
type SyncSummary struct {
    Created int      `json:"created"`
    Updated int      `json:"updated"`
    Merged  int      `json:"merged"`
    Stale   int      `json:"stale"`
    Errors  []string `json:"errors,omitempty"`
}
```

### Action: `sync`

**Input:** `--since` (duration string for Slack lookback, default "7d").

**Output envelope:**

```
pipe:         sync
action:       sync
content:      SyncSummary
content_type: structured
```

The action proceeds in five phases. Each phase is resilient -- a failure in one source does not prevent the other from syncing.

#### Phase 1 -- JIRA Fetch

1. Call `jiraClient.SearchIssues(ctx, jql, expand, limit)` with:
   - JQL: `assignee = currentUser() AND project = PTP AND status NOT IN (Done) ORDER BY updated DESC`
   - expand: `["comments"]`
   - limit: 100
2. Collect the returned `[]jira.Issue`.
3. If the call fails, record the error in the summary's `Errors` slice and continue to Phase 2 with an empty issue list.

#### Phase 2 -- Slack Fetch

1. Parse `--since` flag (default "7d") into a Unix timestamp cutoff.
2. For each channel in `slackClient.Channels`:
   a. Fetch channel history since the cutoff.
   b. Filter for messages mentioning the user.
   c. For each mention, fetch the full thread.
   d. Extract `PTP-\d+` keys from thread text.
3. Collect all mentions as `[]slack.SlackMention`.
4. If the call fails (auth error, network), record the error in `Errors` and continue to Phase 3 with an empty mention list.

#### Phase 3 -- JIRA Upsert

For each JIRA issue:

1. Build `external_id`: `"jira:" + issue.Key` (e.g., `"jira:PTP-123"`).
2. Build title: `"[" + issue.Key + "] " + issue.Summary`.
3. Build details:
   ```
   ## Description
   {issue.Description}

   ## Comments
   -- {author} ({date}): {body}
   -- {author} ({date}): {body}
   ```
4. Map JIRA priority to 1-5 scale:

   | JIRA Priority    | Todo Priority |
   | ---------------- | ------------- |
   | Highest, Blocker | 1             |
   | High             | 2             |
   | Medium           | 3             |
   | Low              | 4             |
   | Lowest           | 5             |

5. Build tags: `["jira"]` + `issue.Labels`.
6. Detect needs_attention:
   - Status is "UAT" (case-insensitive).
   - Most recent comment author != assignee.
   - If true: override priority to 2, add `"needs-fixes"` tag.
7. Call `store.UpsertTodoByExternalID(externalID, title, details, priority, dueDate, tags)`.
8. Track: created count, updated count.
9. If an individual upsert fails, log a warning, record the error, and continue with the next issue.

#### Phase 4 -- Slack Merge

For each `SlackMention`:

1. **If `mention.JiraKeys` is non-empty:**
   - For each JIRA key referenced:
     - Find the todo with `external_id` = `"jira:" + key`.
     - If found, append Slack thread context to its details:
       ```
       ## Slack Thread ({channel}, {timestamp})
       {thread messages joined by newlines}
       ```
     - Call `store.UpdateTodo(id, {"details": updatedDetails})`.
   - Track: merged count.

2. **If `mention.JiraKeys` is empty:**
   - Build `external_id`: `"slack:" + mention.Channel + ":" + mention.ThreadTS`.
   - Build title from first message text (truncated to 80 chars).
   - Build details from full thread text.
   - Tags: `["slack"]`.
   - Priority: 3.
   - Call `store.UpsertTodoByExternalID(...)`.
   - Track: created count.

3. If an individual merge or upsert fails, log a warning, record the error, and continue.

#### Phase 5 -- Stale Detection

1. Query all todos where `external_id LIKE "jira:%"`.
2. Build a set of `external_id` values from Phase 3 JIRA results.
3. Any existing todo whose `external_id` is NOT in the set: add `"stale"` tag.
4. Track: stale count.

---

## Subprocess Entry Point (cmd/main.go)

```go
package main

import (
    "encoding/json"
    "os"
    "path/filepath"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/jira"
    "github.com/justinpbarnett/virgil/internal/pipes/slack"
    "github.com/justinpbarnett/virgil/internal/pipes/sync"
    "github.com/justinpbarnett/virgil/internal/store"
)

func main() {
    logger := pipehost.NewPipeLogger("sync")

    userDir := os.Getenv(pipehost.EnvUserDir)
    if userDir == "" {
        pipehost.Fatal("sync", "VIRGIL_USER_DIR not set")
    }

    dbPath := os.Getenv(pipehost.EnvDBPath)
    if dbPath == "" {
        pipehost.Fatal("sync", "VIRGIL_DB_PATH not set")
    }

    // Open store
    st, err := store.Open(dbPath)
    if err != nil {
        pipehost.Fatal("sync", "cannot open database: "+err.Error())
    }
    defer st.Close()

    // Load JIRA credentials
    jiraCfgData, err := os.ReadFile(filepath.Join(userDir, "jira.json"))
    if err != nil {
        pipehost.Fatal("sync", "cannot read jira.json: "+err.Error())
    }
    var jiraCfg jira.JiraConfig
    if err := json.Unmarshal(jiraCfgData, &jiraCfg); err != nil {
        pipehost.Fatal("sync", "invalid jira.json: "+err.Error())
    }
    jiraClient := jira.NewClient(jiraCfg)

    // Load Slack credentials (optional -- sync can run JIRA-only)
    var slackClient *slack.SlackClient
    slackCfgData, err := os.ReadFile(filepath.Join(userDir, "slack.json"))
    if err != nil {
        logger.Warn("slack.json not found, Slack sync disabled", "error", err)
    } else {
        var slackCfg slack.SlackConfig
        if err := json.Unmarshal(slackCfgData, &slackCfg); err != nil {
            logger.Warn("invalid slack.json, Slack sync disabled", "error", err)
        } else {
            slackClient = slack.NewClient(slackCfg)
        }
    }

    logger.Info("initialized", "jira_url", jiraCfg.BaseURL, "slack_enabled", slackClient != nil)
    pipehost.Run(sync.NewHandler(jiraClient, slackClient, st, logger), nil)
}
```

Deterministic pipe -- no provider needed, no stream handler.

**Startup failure rules:**
- Missing `VIRGIL_USER_DIR` or `VIRGIL_DB_PATH`: fatal (cannot proceed).
- Store cannot open: fatal.
- Missing `jira.json`: fatal (JIRA is the primary source).
- Missing `slack.json`: warn and continue with `slackClient = nil` (Slack is optional).
- Both credential files missing: fatal at the jira.json check.

---

## Composition

The sync pipe is a **standalone terminal pipe**. It does not compose upstream or downstream -- it IS the orchestrator. Typical invocation:

```
sync(action=sync, since=7d)
```

After sync, the user interacts with their todos normally:

```
"show my todos"         -> todo(action=list)
"expand PTP-123"        -> todo(action=detail)
"mark PTP-123 as done"  -> todo(action=done)
```

### Content type conventions

| Action | content_type | Content shape |
| ------ | ------------ | ------------- |
| sync   | structured   | `SyncSummary` |

---

## Error Handling

| Scenario                        | Severity | Behavior                                                       |
| ------------------------------- | -------- | -------------------------------------------------------------- |
| Missing `VIRGIL_USER_DIR`       | fatal    | Cannot proceed, exit via `pipehost.Fatal`                      |
| Missing `VIRGIL_DB_PATH`        | fatal    | Cannot proceed, exit via `pipehost.Fatal`                      |
| Store unavailable               | fatal    | Cannot sync, return fatal error envelope                       |
| Missing `jira.json`             | fatal    | Cannot proceed without JIRA                                    |
| Invalid `jira.json`             | fatal    | Cannot proceed without JIRA                                    |
| Missing `slack.json`            | warn     | Skip Slack phase, sync JIRA only, `slackClient` is nil         |
| Invalid `slack.json`            | warn     | Skip Slack phase, sync JIRA only, `slackClient` is nil         |
| JIRA auth failure               | error    | Skip JIRA phase, continue with Slack, report in `Errors`       |
| Slack auth failure              | error    | Skip Slack phase, continue with JIRA, report in `Errors`       |
| Network timeout (JIRA or Slack) | error    | Skip that source, report in `Errors`                           |
| Individual issue upsert failure | warn     | Skip that issue, continue, report in `Errors`                  |
| Individual merge failure        | warn     | Skip that mention, continue, report in `Errors`                |
| Both fetches return empty       | success  | Return summary with all zeros, no error                        |

The pipe should NOT fail fatally if only one source is unavailable -- partial sync is better than no sync. All non-fatal errors are collected in the `SyncSummary.Errors` slice and reported to the user in the formatted output.

All errors are returned in the envelope -- the handler never panics or exits.

---

## Testing

### Test helpers

```go
// mockJiraClient returns a JiraClient backed by an httptest.Server that
// returns predetermined []Issue responses for SearchIssues calls.
func mockJiraServer(t *testing.T, issues []jira.Issue) *httptest.Server

// mockSlackClient returns a SlackClient backed by an httptest.Server that
// returns predetermined []SlackMention responses.
func mockSlackServer(t *testing.T, mentions []slack.SlackMention) *httptest.Server

// testStore returns a Store backed by an in-memory SQLite database with
// the schema already applied.
func testStore(t *testing.T) *store.Store
```

### Test cases

**Full sync flow:**

1. JIRA issues + Slack mentions with overlap -> correct created/updated/merged counts.
2. Second sync (idempotent) -> updated counts, no new creates for same data.
3. JIRA only (no Slack mentions) -> todos created from JIRA, merged=0.
4. Slack only (JIRA fails) -> slack-only todos created, JIRA errors reported.

**JIRA upsert:**

5. New JIRA issue -> todo created with `external_id`, title, details.
6. Updated JIRA issue (summary changed) -> todo updated.
7. Priority mapping -- each JIRA priority maps to correct 1-5 value.
8. Needs attention -- UAT + non-self comment -> priority 2, `"needs-fixes"` tag.
9. Needs attention -- UAT + self comment -> normal priority.
10. Labels -> included in tags.

**Slack merge:**

11. Slack mention with PTP key -> details appended to matching JIRA todo.
12. Slack mention with multiple PTP keys -> merged into each referenced todo.
13. Slack mention without PTP key -> standalone slack todo created.
14. Duplicate Slack threads -> deduped by `external_id`.

**Stale detection:**

15. Previously synced JIRA todo not in current results -> `"stale"` tag added.
16. Previously stale todo reappears in results -> `"stale"` tag removed (re-synced).

**Error resilience:**

17. JIRA fetch fails -> Slack still synced, errors reported.
18. Slack fetch fails -> JIRA still synced, errors reported.
19. Individual upsert fails -> other upserts continue, error counted.
20. Store unavailable -> fatal error.

**Envelope compliance:**

21. Output has `pipe: "sync"`, `action: "sync"`.
22. `content_type: structured`.
23. Content matches `SyncSummary` shape.
24. Non-zero `Timestamp`, positive `Duration`.
25. `Args` populated with input flags.
26. `Error` is nil on success.

---

## Checklist

```
File Layout
  [ ] pipe.yaml at internal/pipes/sync/pipe.yaml
  [ ] handler code at internal/pipes/sync/sync.go
  [ ] tests at internal/pipes/sync/sync_test.go
  [ ] entry point at internal/pipes/sync/cmd/main.go
  [ ] no configuration outside the pipe folder

Definition
  [ ] name: sync (unique, lowercase)
  [ ] description is one clear sentence
  [ ] category: general
  [ ] triggers cover direct invocation phrases
  [ ] flags: action, since -- all with descriptions and defaults
  [ ] format template declared for structured content type
  [ ] vocabulary: no conflicts with existing pipes

Handler
  [ ] returns complete envelope with all required fields
  [ ] content_type: structured
  [ ] errors returned in envelope, never thrown
  [ ] handles missing optional flags gracefully (since defaults to 7d)
  [ ] handles empty input gracefully
  [ ] partial failure: JIRA down -> Slack still synced
  [ ] partial failure: Slack down -> JIRA still synced
  [ ] individual upsert failures don't halt sync
  [ ] stale detection runs after JIRA upsert phase
  [ ] needs_attention detection applies correct priority and tag

Dependencies
  [ ] imports jira.JiraClient (library code, not pipe handler)
  [ ] imports slack.SlackClient (library code, not pipe handler)
  [ ] imports store.Store (shared infrastructure)
  [ ] does NOT import any pipe handler directly

Subprocess
  [ ] cmd/main.go with pipehost.Run() wrapper
  [ ] reads user dir from VIRGIL_USER_DIR
  [ ] reads db path from VIRGIL_DB_PATH
  [ ] missing jira.json is fatal
  [ ] missing slack.json is a warning (graceful degradation)
  [ ] no streaming (deterministic pipe)

Testing
  [ ] happy path: full sync with JIRA + Slack
  [ ] idempotent re-sync (no duplicate creates)
  [ ] JIRA-only sync (Slack unavailable)
  [ ] Slack-only sync (JIRA unavailable)
  [ ] priority mapping for all JIRA levels
  [ ] needs_attention detection (UAT + non-self comment)
  [ ] Slack merge into existing JIRA todo
  [ ] standalone Slack todo creation
  [ ] stale detection (added and removed)
  [ ] individual failure resilience
  [ ] envelope compliance
```
