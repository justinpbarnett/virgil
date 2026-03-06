# Slack Pipe Spec

## Metadata

type: `feat`
task_id: `slack-pipe`

## What It Does

Deterministic pipe for reading Slack messages. Two actions:

1. **mentions** -- Scans configured channels for messages that mention the user. For each mention, fetches the full thread via `conversations.replies`. Returns thread summaries with any JIRA key (`PTP-\d+`) extracted.
2. **thread** -- Fetches a single thread by channel + thread_ts. For targeted drilldown.

This is read-only -- no posting to Slack. No AI provider needed (deterministic pipe).

---

## File Layout

```
internal/pipes/slack/
├── pipe.yaml
├── slack.go
├── slack_test.go
├── cmd/main.go
└── run
```

---

## pipe.yaml

```yaml
name: slack
description: Reads Slack messages and threads from configured channels.
category: comms
streaming: false
timeout: 30s

triggers:
  exact:
    - "check slack"
    - "slack mentions"
    - "what did I miss on slack"
  keywords:
    - slack
    - mention
    - mentions
    - tagged
    - thread
    - channel
    - message
  patterns:
    - "check slack for {topic}"
    - "slack mentions about {topic}"

flags:
  action:
    description: What operation to perform.
    values: [mentions, thread]
    default: mentions
  since:
    description: How far back to look for mentions (e.g., 7d, 24h, 3d).
    default: "7d"
  channel:
    description: Slack channel ID (for thread action).
    default: ""
  thread_ts:
    description: Thread timestamp (for thread action).
    default: ""

format:
  list: |-
    {{if eq .Count 0}}No recent mentions.{{else}}{{.Count}} mention{{if gt .Count 1}}s{{end}}:{{range .Items}}
    {{.author}} in #{{.channel}} ({{.timestamp}}):
      {{.text}}{{if .jira_keys}} → {{range $i, $k := .jira_keys}}{{if $i}}, {{end}}{{$k}}{{end}}{{end}}{{end}}{{end}}
  structured: |-
    Thread in #{{.channel}} ({{.thread_ts}}):{{range .thread}}
    {{.}}{{end}}{{if .jira_keys}}
    Referenced: {{range $i, $k := .jira_keys}}{{if $i}}, {{end}}{{$k}}{{end}}{{end}}

vocabulary:
  verbs:
    slack: [slack.mentions]
  types: {}
  sources:
    slack: [slack]
    mentions: [slack]
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb]
      plan:
        - pipe: slack
          flags: { action: mentions }
```

---

## Authentication

**Slack User Token** -- NOT a bot token. User tokens don't announce themselves to the workspace. No "X app was added" notification. The user creates a personal Slack app at api.slack.com/apps with these scopes:

- `channels:history` -- read messages in public channels
- `channels:read` -- list channels (for validation)
- `users:read` -- resolve user IDs to display names

Install the app to the workspace. The token is a `xoxp-...` user token.

Credentials stored at `VIRGIL_USER_DIR/slack.json`:

```json
{
  "token": "xoxp-...",
  "user_id": "U0ABC1234",
  "channels": ["C07ABC123", "C07DEF456", "C07GHI789"]
}
```

- `token` -- Slack user token
- `user_id` -- the user's own Slack user ID (used to filter mentions)
- `channels` -- explicit list of channel IDs to scan. Only these channels are polled. This is the set of channels where the client team communicates.

### Credential loading

```go
type SlackConfig struct {
    Token    string   `json:"token"`
    UserID   string   `json:"user_id"`
    Channels []string `json:"channels"`
}
```

1. Read `VIRGIL_USER_DIR/slack.json`.
2. Validate token, user_id, and channels are non-empty.
3. If missing or malformed, return fatal error explaining how to configure.

---

## Handler

### Signature

```go
func NewHandler(client *SlackClient, logger *slog.Logger) pipe.Handler
```

The handler receives a pre-constructed `SlackClient` that encapsulates auth, channel list, and HTTP transport. The client is built in `cmd/main.go` from the credential file.

### SlackClient

```go
type SlackClient struct {
    Token      string
    UserID     string
    Channels   []string
    HTTPClient *http.Client
}

func NewClient(cfg SlackConfig) *SlackClient
```

Methods:

#### `GetChannelHistory(ctx, channelID, oldest string, limit int) ([]SlackMessage, error)`

- Calls `GET https://slack.com/api/conversations.history`
- Params: `channel`, `oldest` (Unix timestamp), `limit`
- Auth: `Authorization: Bearer {token}`
- Parses `messages` array from response
- Checks `ok` field -- if false, returns error with Slack's `error` string

#### `GetThreadReplies(ctx, channelID, threadTS string) ([]SlackMessage, error)`

- Calls `GET https://slack.com/api/conversations.replies`
- Params: `channel`, `ts` (the thread's parent timestamp)
- Returns all messages in the thread

#### `GetUserInfo(ctx, userID string) (displayName string, error)`

- Calls `GET https://slack.com/api/users.info`
- Params: `user`
- Returns `user.profile.display_name` or `user.real_name` as fallback
- Cache user lookups in a map for the duration of a single invocation (users are mentioned repeatedly)

### Action: `mentions`

1. Read `--since` flag (default "7d"). Parse into a Unix timestamp (now minus duration).
2. For each channel in `client.Channels`:
   a. Call `GetChannelHistory(ctx, channel, oldest, 200)`
   b. Filter messages where `text` contains `<@{client.UserID}>` (Slack's mention format)
   c. For each matching message:
      - Determine thread_ts: if the message has `thread_ts`, use it. If the message IS the parent (no thread_ts), use its `ts` as thread_ts.
      - Call `GetThreadReplies(ctx, channel, thread_ts)` to get full thread
      - Extract JIRA keys from all thread messages using regex `PTP-\d+`
      - Resolve the mentioning user's display name via `GetUserInfo`
      - Build a `SlackMention` with condensed thread text (each message as "Author: text")
3. Deduplicate mentions by channel+thread_ts (same thread may have multiple mentions).
4. Sort by timestamp descending (most recent first).
5. Return `[]SlackMention` as content with content_type `list`.

**Output envelope:**

```
pipe:         slack
action:       mentions
content:      []SlackMention
content_type: list
```

### Action: `thread`

1. Read `--channel` and `--thread_ts` flags (both required for this action).
2. Call `GetThreadReplies(ctx, channel, thread_ts)`.
3. Resolve each user's display name.
4. Extract JIRA keys from all messages.
5. Return a single `SlackMention` as content with content_type `structured`.

**Output envelope:**

```
pipe:         slack
action:       thread
content:      SlackMention
content_type: structured
```

---

## Subprocess Entry Point (cmd/main.go)

```go
package main

import (
    "encoding/json"
    "os"
    "path/filepath"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/slack"
)

func main() {
    logger := pipehost.NewPipeLogger("slack")

    userDir := os.Getenv(pipehost.EnvUserDir)
    if userDir == "" {
        pipehost.Fatal("slack", "VIRGIL_USER_DIR not set")
    }

    cfgData, err := os.ReadFile(filepath.Join(userDir, "slack.json"))
    if err != nil {
        pipehost.Fatal("slack", "cannot read slack.json from "+userDir+": "+err.Error()+
            "\n\nCreate slack.json with: {\"token\": \"xoxp-...\", \"user_id\": \"U...\", \"channels\": [\"C...\"]}")
    }

    var cfg slack.SlackConfig
    if err := json.Unmarshal(cfgData, &cfg); err != nil {
        pipehost.Fatal("slack", "invalid slack.json: "+err.Error())
    }

    client := slack.NewClient(cfg)
    logger.Info("initialized", "channels", len(cfg.Channels))
    pipehost.Run(slack.NewHandler(client, logger), nil)
}
```

Deterministic pipe -- no provider needed, no stream handler.

---

## Data Types

### SlackConfig

```go
type SlackConfig struct {
    Token    string   `json:"token"`
    UserID   string   `json:"user_id"`
    Channels []string `json:"channels"`
}
```

### SlackMessage

```go
// SlackMessage represents a single Slack message.
type SlackMessage struct {
    User      string `json:"user"`
    Text      string `json:"text"`
    Timestamp string `json:"ts"`
    ThreadTS  string `json:"thread_ts,omitempty"`
}
```

### SlackMention

```go
// SlackMention represents a thread where the user was mentioned.
type SlackMention struct {
    Channel   string   `json:"channel"`
    ThreadTS  string   `json:"thread_ts"`
    Author    string   `json:"author"`
    Text      string   `json:"text"`
    Thread    []string `json:"thread"`
    JiraKeys  []string `json:"jira_keys,omitempty"`
    Timestamp string   `json:"timestamp"`
}
```

**Notes on field construction:**

- `Channel` -- the channel ID where the mention occurred.
- `ThreadTS` -- the parent thread timestamp. Used as a unique identifier for the thread within a channel.
- `Author` -- the display name of the user who posted the message containing the mention. Resolved via `GetUserInfo`.
- `Text` -- the raw text of the message that contained the mention.
- `Thread` -- condensed thread text. Each message formatted as `"DisplayName: message text"`.
- `JiraKeys` -- unique JIRA issue keys (matching `PTP-\d+`) extracted from all messages in the thread. Deduplicated.
- `Timestamp` -- the `ts` of the mentioning message. Used for sorting.

---

## Composition

### As a source (upstream)

```
slack(action=mentions) → sync (aggregate with JIRA data)
```

Fetch mentions with extracted JIRA keys, then pass to a downstream pipe that enriches or correlates with JIRA issue data.

### As a standalone

Direct user invocation for mention scanning or thread drilldown:

```
slack(action=mentions, since=3d)
slack(action=thread, channel=C07ABC, thread_ts=1709234567.123)
```

### Content type conventions

| Action   | content_type | Content shape     |
| -------- | ------------ | ----------------- |
| mentions | list         | `[]SlackMention`  |
| thread   | structured   | `SlackMention`    |

---

## Error Handling

| Scenario                       | Severity | Retryable | Message pattern                                                             |
| ------------------------------ | -------- | --------- | --------------------------------------------------------------------------- |
| Missing slack.json             | fatal    | false     | `"cannot read slack.json — create it at {path} with ..."`                   |
| Invalid slack.json             | fatal    | false     | `"invalid slack.json: {parse error}"`                                       |
| Invalid token (invalid_auth)   | fatal    | false     | `"Slack authentication failed — check slack.json token"`                    |
| Channel not found              | error    | false     | `"channel not found: {id}"`                                                 |
| Rate limited (429)             | error    | true      | `"rate limited by Slack — retry after {seconds}s"`                          |
| Network error                  | error    | true      | `"cannot reach Slack API: {error}"`                                         |
| Missing --channel for thread   | fatal    | false     | `"channel is required for thread action"`                                   |
| Missing --thread_ts for thread | fatal    | false     | `"thread_ts is required for thread action"`                                 |
| No mentions found              | success  | n/a       | Returns empty list, no error                                                |

All errors are returned in the envelope -- the handler never panics or exits.

**Rate limit notes:** Slack API rate limits for Tier 3 methods (conversations.history, conversations.replies) allow ~50 req/min. With 3 channels x (history + thread replies), a typical sync is well within limits. If rate limited, Slack returns `ok: false` with `error: "ratelimited"` and a `Retry-After` header.

---

## Testing

### Test helpers

```go
// mockSlackServer returns an httptest.Server that simulates Slack API responses.
func mockSlackServer(t *testing.T) *httptest.Server {
    mux := http.NewServeMux()
    // GET /api/conversations.history → channel messages
    // GET /api/conversations.replies → thread messages
    // GET /api/users.info → user profile
    return httptest.NewServer(mux)
}
```

### Test cases

**mentions action:**

1. Channel with mentions -- returns SlackMention with correct fields.
2. Multiple channels -- aggregates mentions across all channels.
3. Thread detection -- message with thread_ts fetches that thread.
4. Parent message mention -- message without thread_ts uses its own ts.
5. JIRA key extraction -- "PTP-123" and "PTP-456" extracted from thread.
6. No JIRA keys -- jira_keys is empty/nil.
7. Dedup -- same thread mentioned twice produces single SlackMention.
8. Since filter -- only messages after oldest timestamp returned.
9. No mentions -- returns empty list, no error.
10. User resolution -- user IDs resolved to display names.
11. User cache -- same user ID only looked up once.

**thread action:**

12. Fetch thread by channel+ts -- returns full thread.
13. Missing channel -- fatal error.
14. Missing thread_ts -- fatal error.

**Auth:**

15. Invalid token -- fatal error with credential message.
16. Rate limited -- retryable error with retry-after.

**Envelope compliance (all actions):**

17. mentions returns content_type `list`.
18. thread returns content_type `structured`.
19. All outputs have `pipe: "slack"`, correct `action`, non-zero `Timestamp`, positive `Duration`.

---

## Checklist

```
File Layout
  [ ] pipe.yaml at internal/pipes/slack/pipe.yaml
  [ ] handler code at internal/pipes/slack/slack.go
  [ ] tests at internal/pipes/slack/slack_test.go
  [ ] entry point at internal/pipes/slack/cmd/main.go
  [ ] no configuration outside the pipe folder

Definition
  [ ] name: slack (unique, lowercase)
  [ ] description is one clear sentence
  [ ] category: comms
  [ ] triggers cover direct invocation phrases
  [ ] flags: action, since, channel, thread_ts — all with descriptions and defaults
  [ ] format templates declared for list and structured content types
  [ ] vocabulary: no conflicts with existing pipes

Handler
  [ ] returns complete envelope with all required fields
  [ ] content_type: list for mentions, structured for thread
  [ ] errors returned in envelope, never thrown
  [ ] handles missing optional flags gracefully (since defaults to 7d)
  [ ] handles empty input gracefully
  [ ] validates required flags for thread action (channel, thread_ts)
  [ ] deduplicates mentions by channel+thread_ts
  [ ] sorts mentions by timestamp descending
  [ ] JIRA key extraction via PTP-\d+ regex
  [ ] user display name resolution with per-invocation cache

Auth
  [ ] reads credentials from VIRGIL_USER_DIR/slack.json
  [ ] validates token, user_id, channels are non-empty
  [ ] clear fatal error when credentials are missing or invalid
  [ ] uses Bearer auth with xoxp- user token

Subprocess
  [ ] cmd/main.go with pipehost.Run() wrapper
  [ ] reads user dir from VIRGIL_USER_DIR
  [ ] no streaming (deterministic pipe)
  [ ] handles startup failures gracefully (pipehost.Fatal)

Testing
  [ ] happy path for each action
  [ ] multi-channel aggregation
  [ ] thread_ts detection (threaded vs parent message)
  [ ] JIRA key extraction
  [ ] dedup logic
  [ ] since filter
  [ ] missing/invalid flag tests
  [ ] auth error tests
  [ ] rate limit handling
  [ ] user cache behavior
  [ ] envelope compliance tests
```
