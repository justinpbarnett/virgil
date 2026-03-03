# Jira Pipe Spec

## Metadata

type: `feat`
task_id: `jira-pipe`

## What It Does

Deterministic pipe for interacting with Jira via its REST API. Three actions:

1. **get** — Retrieve an issue by key (e.g., `PROJ-123`), including its fields, comments, and attachments (image URLs).
2. **comment** — Post a comment to an issue.
3. **update** — Update one or more fields on an issue (status, assignee, priority, labels, custom fields).

Authentication uses a Personal Access Token (PAT) read from `VIRGIL_USER_DIR/jira.json`. All API calls go through the Jira REST API v3 (`/rest/api/3/`).

This is a deterministic pipe — no AI provider needed.

---

## File Layout

```
internal/pipes/jira/
├── pipe.yaml
├── jira.go
├── jira_test.go
├── cmd/main.go
└── run
```

---

## pipe.yaml

```yaml
name: jira
description: Retrieves, comments on, and updates Jira issues via the REST API.
category: dev
streaming: false
timeout: 30s

triggers:
  exact:
    - "check jira"
    - "get jira ticket"
  keywords:
    - jira
    - ticket
    - issue
    - sprint
    - backlog
    - story
    - epic
    - bug
  patterns:
    - "get {topic}"
    - "update {topic}"
    - "comment on {topic}"
    - "show me {topic}"

flags:
  action:
    description: What operation to perform.
    values: [get, comment, update]
    default: get

  id:
    description: Jira issue key (e.g., PROJ-123).
    default: ""
    required: true

  fields:
    description: Comma-separated list of fields to return on get, or JSON object of fields to set on update.
    default: ""

  comment:
    description: Comment body text to post (used with action=comment).
    default: ""

  expand:
    description: Comma-separated list of sections to expand on get.
    values: [comments, attachments, changelog, all]
    default: "comments,attachments"

format:
  structured: |
    [{{.key}}] {{.summary}}
    Status: {{.status}}  Priority: {{.priority}}  Assignee: {{.assignee}}
    {{if .labels}}Labels: {{range $i, $l := .labels}}{{if $i}}, {{end}}{{$l}}{{end}}{{end}}
    {{if .description}}
    Description:
    {{.description}}
    {{end}}
    {{if .comments}}
    Comments ({{len .comments}}):{{range .comments}}
      — {{.author}} ({{.created}}): {{.body}}{{end}}
    {{end}}
    {{if .attachments}}
    Attachments ({{len .attachments}}):{{range .attachments}}
      - {{.filename}} ({{.mimeType}}, {{.size}} bytes) {{.url}}{{end}}
    {{end}}

vocabulary:
  verbs:
    jira: jira.get
  types:
    ticket: ticket
    issue: issue
    story: story
    epic: epic
    bug: bug
    task: task
  sources:
    jira: jira
    tickets: jira
    issues: jira
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb, source, topic]
      plan:
        - pipe: jira
          flags: { action: get, id: "{topic}" }

    - requires: [verb, source]
      plan:
        - pipe: jira
          flags: { action: get }
```

---

## Authentication

Jira PAT credentials are stored at `VIRGIL_USER_DIR/jira.json`:

```json
{
  "base_url": "https://yourcompany.atlassian.net",
  "email": "you@example.com",
  "token": "your-personal-access-token"
}
```

**Cloud instances** use Basic auth: `Authorization: Basic base64(email:token)`.

**Server/Data Center instances** use Bearer auth: `Authorization: Bearer token`. The handler detects which to use based on whether `base_url` contains `.atlassian.net`.

### Credential loading

```go
type JiraConfig struct {
    BaseURL string `json:"base_url"`
    Email   string `json:"email"`
    Token   string `json:"token"`
}
```

1. Read `VIRGIL_USER_DIR/jira.json`.
2. Validate all three fields are non-empty.
3. If the file is missing or malformed, return a fatal error with a message explaining how to configure credentials.

---

## Handler

### Signature

```go
func NewHandler(client *JiraClient, logger *slog.Logger) pipe.Handler
```

The handler receives a pre-constructed `JiraClient` that encapsulates auth, base URL, and HTTP transport. The client is built in `cmd/main.go` from the credential file.

### JiraClient

```go
type JiraClient struct {
    BaseURL    string
    Email      string
    Token      string
    HTTPClient *http.Client
}

func NewClient(cfg JiraConfig) *JiraClient

func (c *JiraClient) GetIssue(ctx context.Context, key string, expand []string) (*Issue, error)
func (c *JiraClient) AddComment(ctx context.Context, key string, body string) (*Comment, error)
func (c *JiraClient) UpdateIssue(ctx context.Context, key string, fields map[string]any) error
func (c *JiraClient) GetAttachment(ctx context.Context, attachmentID string) ([]byte, string, error)
```

All methods set the appropriate auth header, `Content-Type: application/json`, and `Accept: application/json`.

### Action: `get`

**Input:** `--id` (issue key, required), `--expand` (sections to include), `--fields` (specific fields to return).

**Behavior:**

1. Validate `--id` is non-empty and looks like a Jira key (`[A-Z][A-Z0-9]+-\d+`).
2. Call `GET /rest/api/3/issue/{id}` with query params:
   - `expand=renderedFields,names` (always)
   - `expand=comment` if expand includes `comments` or `all`
   - `fields` param if `--fields` is specified (comma-separated field IDs)
3. If expand includes `attachments` or `all`, the attachment metadata is already in the issue response under `fields.attachment`. No separate call needed — Jira includes attachment metadata (filename, mimeType, size, content URL) in the issue response.
4. Parse the response into the `Issue` struct.
5. If expand includes `comments` or `all`, extract comments from `fields.comment.comments`.
6. Return the assembled `Issue` as structured content.

**Output envelope:**

```
pipe:         jira
action:       get
content:      Issue struct (see Data Types below)
content_type: structured
```

**Edge cases:**

- Issue not found (404) → fatal error: `"issue not found: {id}"`.
- Invalid issue key format → fatal error: `"invalid issue key: {id} (expected format: PROJ-123)"`.
- Auth failure (401/403) → fatal error: `"authentication failed — check jira.json credentials"`.
- Rate limited (429) → retryable error with message from response.

### Action: `comment`

**Input:** `--id` (issue key, required), `--comment` (body text, required). Comment text can also come from the input envelope's `content` field — if `--comment` is empty, the handler falls back to envelope content.

**Behavior:**

1. Validate `--id` is non-empty and valid format.
2. Resolve comment body: `--comment` flag takes precedence, then envelope `content`.
3. If no comment body from either source, return fatal error.
4. Call `POST /rest/api/3/issue/{id}/comment` with Atlassian Document Format (ADF) body:
   ```json
   {
     "body": {
       "version": 1,
       "type": "doc",
       "content": [
         {
           "type": "paragraph",
           "content": [
             { "type": "text", "text": "the comment text" }
           ]
         }
       ]
     }
   }
   ```
5. Parse the response to confirm the comment was created.
6. Return the created comment metadata.

**Output envelope:**

```
pipe:         jira
action:       comment
content:      {"id": "12345", "author": "you@example.com", "created": "2026-03-02T...", "body": "the comment text"}
content_type: structured
```

**Edge cases:**

- Empty comment body → fatal error: `"comment body is empty — provide via --comment flag or envelope content"`.
- Issue not found → fatal error.
- No permission to comment → fatal error with Jira's error message.

### Action: `update`

**Input:** `--id` (issue key, required), `--fields` (JSON object of fields to update).

**Behavior:**

1. Validate `--id` is non-empty and valid format.
2. Parse `--fields` as JSON. Expected format: `{"summary": "New title", "priority": {"name": "High"}, "labels": ["backend", "urgent"]}`.
3. If `--fields` is empty, check envelope content for a JSON object of fields.
4. If no fields from either source, return fatal error.
5. Call `PUT /rest/api/3/issue/{id}` with body:
   ```json
   {
     "fields": { ... parsed fields ... }
   }
   ```
6. Jira returns `204 No Content` on success.
7. After successful update, fetch the issue again (`GET`) to return the updated state.

**Output envelope:**

```
pipe:         jira
action:       update
content:      Issue struct (refreshed after update)
content_type: structured
```

**Edge cases:**

- Invalid `--fields` JSON → fatal error: `"--fields is not valid JSON: {parse error}"`.
- No fields to update → fatal error: `"no fields to update — provide via --fields flag or envelope content"`.
- Field not editable (400 from Jira) → fatal error with Jira's error message (e.g., `"Field 'status' cannot be set directly — use transitions"`).
- Issue not found → fatal error.
- Auth failure → fatal error.

**Status transitions:** Jira doesn't allow setting `status` directly via the update endpoint — it requires the transitions API. If the `--fields` JSON contains a `status` key, the handler should:

1. Remove `status` from the fields map.
2. Call `GET /rest/api/3/issue/{id}/transitions` to get available transitions.
3. Find the transition whose `to.name` matches the requested status (case-insensitive).
4. Call `POST /rest/api/3/issue/{id}/transitions` with `{"transition": {"id": "..."}}`.
5. If no matching transition exists, return a fatal error listing the available transitions.
6. Continue with the remaining field updates if any.

---

## Subprocess Entry Point (cmd/main.go)

```go
package main

import (
    "encoding/json"
    "os"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/jira"
)

func main() {
    logger := pipehost.NewPipeLogger("jira")

    userDir := os.Getenv(pipehost.EnvUserDir)
    if userDir == "" {
        pipehost.Fatal("jira", "VIRGIL_USER_DIR not set")
    }

    cfgData, err := os.ReadFile(filepath.Join(userDir, "jira.json"))
    if err != nil {
        pipehost.Fatal("jira", "cannot read jira.json from "+userDir+": "+err.Error()+
            "\n\nCreate jira.json with: {\"base_url\": \"https://yourco.atlassian.net\", \"email\": \"you@example.com\", \"token\": \"your-pat\"}")
    }

    var cfg jira.JiraConfig
    if err := json.Unmarshal(cfgData, &cfg); err != nil {
        pipehost.Fatal("jira", "invalid jira.json: "+err.Error())
    }

    client := jira.NewClient(cfg)
    logger.Info("initialized", "base_url", cfg.BaseURL)
    pipehost.Run(jira.NewHandler(client, logger), nil)
}
```

Deterministic pipe — no provider needed, no stream handler.

---

## Data Types

### JiraConfig

```go
type JiraConfig struct {
    BaseURL string `json:"base_url"`
    Email   string `json:"email"`
    Token   string `json:"token"`
}
```

### Issue

```go
type Issue struct {
    Key         string            `json:"key"`
    Summary     string            `json:"summary"`
    Status      string            `json:"status"`
    Priority    string            `json:"priority"`
    Assignee    string            `json:"assignee"`
    Reporter    string            `json:"reporter"`
    Labels      []string          `json:"labels"`
    Description string            `json:"description"`
    IssueType   string            `json:"issue_type"`
    Created     string            `json:"created"`
    Updated     string            `json:"updated"`
    Comments    []Comment         `json:"comments,omitempty"`
    Attachments []Attachment      `json:"attachments,omitempty"`
    CustomFields map[string]any   `json:"custom_fields,omitempty"`
}
```

### Comment

```go
type Comment struct {
    ID      string `json:"id"`
    Author  string `json:"author"`
    Created string `json:"created"`
    Updated string `json:"updated"`
    Body    string `json:"body"`
}
```

### Attachment

```go
type Attachment struct {
    ID       string `json:"id"`
    Filename string `json:"filename"`
    MimeType string `json:"mimeType"`
    Size     int64  `json:"size"`
    URL      string `json:"url"`
    Created  string `json:"created"`
    Author   string `json:"author"`
}
```

**Notes on field extraction:**

- `summary`, `status`, `priority`, `assignee`, `reporter`, `labels`, `issuetype` come from `fields.*` in the Jira response.
- `status` is extracted as `fields.status.name` (not the status ID).
- `priority` is extracted as `fields.priority.name`.
- `assignee` is extracted as `fields.assignee.displayName` (empty string if unassigned).
- `description` is extracted from `renderedFields.description` (HTML rendered) and converted to plain text, or from `fields.description` (ADF) and converted. Use rendered fields — they're simpler to work with.
- Comments: `fields.comment.comments[]` — extract `id`, `author.displayName`, `created`, `updated`, `body` (rendered to plain text from ADF).
- Attachments: `fields.attachment[]` — extract `id`, `filename`, `mimeType`, `size`, `content` (the download URL).
- Custom fields: any `fields.customfield_*` entries are collected into `CustomFields` with their field IDs as keys.

### ADF to Plain Text

Jira v3 uses Atlassian Document Format for rich text. For the `get` action, the handler should convert ADF to plain text for downstream consumption. A minimal converter handles the common node types:

```go
func adfToPlainText(doc map[string]any) string {
    // Walk the ADF tree, extracting text nodes.
    // "paragraph" → join text children + newline
    // "heading" → join text children + newline
    // "bulletList" / "orderedList" → prefix items with "- " or "1. "
    // "codeBlock" → wrap in backticks
    // "text" → return the "text" field value
    // Unknown types → recurse into "content" children
}
```

Use `renderedFields.description` (HTML) with a simple HTML-to-text strip as an alternative if ADF parsing is too complex for v1. The HTML renderer is a Jira API feature — request it with `expand=renderedFields`.

---

## Composition

### As a source (upstream)

Jira provides context for downstream pipes. Retrieve a ticket's details and feed them to a draft or review pipe:

```
jira(action=get, id=PROJ-123) → draft(type=pr)
```

Fetch the ticket description and comments, then draft a PR description from the context.

```
jira(action=get, id=PROJ-123) → review(criteria=spec-compliance)
```

Review code against the acceptance criteria in a Jira ticket.

### As a sink (downstream)

Post results back to Jira after a pipeline completes:

```
draft(type=memo) → jira(action=comment, id=PROJ-123)
```

Draft a memo, then post it as a comment on the ticket.

```
review(criteria=correctness) → jira(action=comment, id=PROJ-123)
```

Run a code review, then post the findings as a comment on the ticket.

### As a standalone

Direct user invocation for ticket lookup:

```
jira(action=get, id=PROJ-123, expand=all)
```

### Content type conventions

| Action  | content_type | Content shape           |
| ------- | ------------ | ----------------------- |
| get     | structured   | `Issue`                 |
| comment | structured   | `Comment` (created)     |
| update  | structured   | `Issue` (refreshed)     |

---

## Error Handling

| Scenario                        | Severity | Retryable | Message pattern                                                     |
| ------------------------------- | -------- | --------- | ------------------------------------------------------------------- |
| Missing jira.json               | fatal    | false     | `"cannot read jira.json — create it at {path} with ..."`            |
| Invalid jira.json               | fatal    | false     | `"invalid jira.json: {parse error}"`                                |
| Missing --id flag               | fatal    | false     | `"--id is required: provide a Jira issue key (e.g., PROJ-123)"`    |
| Invalid issue key format        | fatal    | false     | `"invalid issue key: {id} (expected format: PROJ-123)"`            |
| Issue not found (404)           | fatal    | false     | `"issue not found: {id}"`                                          |
| Auth failure (401/403)          | fatal    | false     | `"authentication failed — check jira.json credentials"`            |
| Rate limited (429)              | error    | true      | `"rate limited by Jira — retry after {seconds}s"`                  |
| Network/connection error        | error    | true      | `"cannot reach Jira at {base_url}: {error}"`                       |
| Server error (5xx)              | error    | true      | `"Jira server error ({status}): {body}"`                           |
| Invalid --fields JSON           | fatal    | false     | `"--fields is not valid JSON: {parse error}"`                      |
| No fields to update             | fatal    | false     | `"no fields to update — provide via --fields or envelope content"` |
| Field not editable (400)        | fatal    | false     | Jira's error message verbatim                                       |
| Status transition not available | fatal    | false     | `"no transition to '{status}' — available: {list}"`                |
| Empty comment body              | fatal    | false     | `"comment body is empty — provide via --comment or envelope"`      |

All errors are returned in the envelope — the handler never panics or exits.

---

## Testing

### Test helpers

```go
// mockJiraServer returns an httptest.Server that simulates Jira API responses.
func mockJiraServer(t *testing.T) *httptest.Server {
    mux := http.NewServeMux()
    // GET /rest/api/3/issue/PROJ-123 → valid issue JSON
    // GET /rest/api/3/issue/NOTFOUND-1 → 404
    // POST /rest/api/3/issue/PROJ-123/comment → created comment
    // PUT /rest/api/3/issue/PROJ-123 → 204
    // GET /rest/api/3/issue/PROJ-123/transitions → available transitions
    // POST /rest/api/3/issue/PROJ-123/transitions → 204
    return httptest.NewServer(mux)
}
```

### Test cases

**get action:**

1. Get existing issue → returns Issue with all core fields populated, content_type is `structured`.
2. Get with `expand=comments` → comments array populated.
3. Get with `expand=attachments` → attachments array populated with filenames, URLs, sizes.
4. Get with `expand=all` → both comments and attachments present.
5. Get with `--fields=summary,status` → only requested fields populated.
6. Get nonexistent issue → fatal error with "issue not found".
7. Get with invalid key format (e.g., `lowercase-123`, `123`) → fatal error with format guidance.
8. Get with auth failure → fatal error with credential message.
9. Get with rate limit response → retryable error.

**comment action:**

10. Post comment via `--comment` flag → returns created Comment with id, author, timestamp.
11. Post comment via envelope content (no `--comment` flag) → same result.
12. Post comment with both flag and envelope → flag takes precedence.
13. Post empty comment → fatal error.
14. Post comment on nonexistent issue → fatal error.

**update action:**

15. Update summary field → returns refreshed Issue with new summary.
16. Update multiple fields (priority, labels) → all fields updated in response.
17. Update status via transition → transition called, returns issue with new status.
18. Update status with unavailable transition → fatal error listing available transitions.
19. Update with invalid JSON in `--fields` → fatal error with parse error.
20. Update with no fields → fatal error.
21. Update with non-editable field → fatal error with Jira's message.

**Auth:**

22. Cloud URL (`.atlassian.net`) → Basic auth header used.
23. Server URL (custom domain) → Bearer auth header used.

**Envelope compliance (all actions):**

24. Output has `pipe: "jira"` and correct `action` field.
25. Output has non-zero `Timestamp` and positive `Duration`.
26. Output has `Args` populated with input flags.
27. Output `content_type` is `structured` for all actions.
28. Output `Error` is nil on success.

---

## Checklist

```
File Layout
  ☐ pipe.yaml at internal/pipes/jira/pipe.yaml
  ☐ handler code at internal/pipes/jira/jira.go
  ☐ tests at internal/pipes/jira/jira_test.go
  ☐ entry point at internal/pipes/jira/cmd/main.go
  ☐ no configuration outside the pipe folder

Definition
  ☐ name: jira (unique, lowercase)
  ☐ description is one clear sentence
  ☐ category: dev
  ☐ triggers cover direct invocation phrases
  ☐ flags: action, id, fields, comment, expand — all with descriptions and defaults
  ☐ vocabulary: no conflicts with existing pipes

Handler
  ☐ returns complete envelope with all required fields
  ☐ content_type: structured for all actions
  ☐ errors returned in envelope, never thrown
  ☐ handles missing optional flags gracefully
  ☐ handles empty input gracefully
  ☐ validates issue key format before API call
  ☐ status updates use transitions API
  ☐ ADF-to-text conversion for descriptions and comments

Auth
  ☐ reads credentials from VIRGIL_USER_DIR/jira.json
  ☐ detects Cloud vs Server for auth method
  ☐ clear fatal error when credentials are missing or invalid

Subprocess
  ☐ cmd/main.go with pipehost.Run() wrapper
  ☐ reads user dir from VIRGIL_USER_DIR
  ☐ no streaming (deterministic pipe)

Testing
  ☐ happy path for each action
  ☐ expand modes (comments, attachments, all)
  ☐ comment from flag vs envelope content
  ☐ status transition logic
  ☐ missing/invalid flag tests
  ☐ auth method detection tests
  ☐ HTTP error code mapping tests
  ☐ envelope compliance tests
```
