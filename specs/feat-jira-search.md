# Jira Search Action Spec

## Metadata

type: `feat`
task_id: `jira-search`

## What It Does

Adds a `search` action to the existing jira pipe that queries Jira for multiple issues via JQL (Jira Query Language). This enables bulk retrieval — specifically "all my non-done tickets on the PTP project."

The existing actions (get, comment, update) are unchanged.

This is a deterministic addition — no AI provider needed.

---

## JiraClient Addition

New method on the existing `JiraClient`:

```go
func (c *JiraClient) SearchIssues(ctx context.Context, jql string, expand []string, limit int) ([]Issue, error)
```

- Calls `POST /rest/api/3/search` (POST because JQL can be long).
- Request body:
```json
{
  "jql": "...",
  "maxResults": 50,
  "fields": ["summary", "status", "priority", "assignee", "reporter", "labels", "description", "issuetype", "created", "updated", "comment", "attachment"],
  "expand": ["renderedFields", "names"]
}
```
- Parses the response `issues` array using the existing `parseIssue` function.
- Handles pagination: if `total > startAt + maxResults`, fetch next page with `startAt` incremented. Cap at `limit` total results.
- Returns `[]Issue`.

---

## Handler Changes

Add `search` action to the switch in `NewHandler`. The `search` action does NOT require the `--id` flag, so the id validation that currently gates all actions must be restructured — move it inside the `get`, `comment`, and `update` cases, or check action before validating id.

```go
case "search":
    out = handleSearch(ctx, client, logger, flags, out)
```

### `handleSearch` implementation

1. Read `--jql` flag. If empty, use default: `assignee = currentUser() AND project = PTP AND status NOT IN (Done) ORDER BY updated DESC`.
2. Read `--limit` flag (default `"50"`), parse to int.
3. Read `--expand` flag (default `"comments,attachments"`), split on commas.
4. Call `client.SearchIssues(ctx, jql, expand, limit)`.
5. For each issue, run `detectNeedsAttention` (see below). If true, set a `needs_attention` field on the issue's output representation.
6. Return `[]Issue` as content with `content_type: list`.

**Output envelope:**

```
pipe:         jira
action:       search
content:      []Issue
content_type: list
```

---

## pipe.yaml Changes

### Flags

Add new flags:

```yaml
  jql:
    description: JQL query string for search action. Defaults to assigned non-done PTP issues.
    default: ""

  limit:
    description: Maximum number of issues to return (for search action).
    default: "50"
```

Update action flag values:

```yaml
  action:
    description: What operation to perform.
    values: [get, comment, update, search]
    default: get
```

Update the `id` flag — change `required: true` to `required: false`. The handler validates that id is present for get/comment/update actions:

```yaml
  id:
    description: Jira issue key (e.g., PROJ-123).
    default: ""
    required: false
```

### Triggers

Add exact triggers:

```yaml
  exact:
    # ... existing ...
    - "my jira tickets"
    - "what's assigned to me"
    - "show my tickets"
```

Add keywords:

```yaml
  keywords:
    # ... existing ...
    - assigned
    - mine
    - my
```

### Patterns

Add patterns:

```yaml
  patterns:
    # ... existing ...
    - "search jira for {topic}"
    - "find jira {topic}"
    - "my {topic} tickets"
```

### Vocabulary

Add verbs:

```yaml
  verbs:
    # ... existing ...
    search: [jira.search]
    find: [jira.search]
```

Add sources:

```yaml
  sources:
    # ... existing ...
    assigned: [jira]
```

### Templates

Add template entry:

```yaml
    - requires: [verb]
      plan:
        - pipe: jira
          flags: { action: search }
```

### Format

Add format template for list output:

```yaml
format:
  # ... existing structured format ...
  list: |-
    {{if eq .Count 0}}No issues found.{{else}}{{.Count}} issue{{if gt .Count 1}}s{{end}}:{{range .Items}}
    [{{.key}}] {{.summary}} -- {{.status}}{{if .needs_attention}} !! needs attention{{end}} . {{.priority}}{{end}}{{end}}
```

---

## The "Needs Attention" Detection

For issues in UAT status, detect when they actually need developer work:

- Issue status is "UAT" (case-insensitive match).
- The issue has at least one comment.
- The most recent comment's author is NOT the issue's assignee.
- This implies a client/tester left a comment — the developer needs to look at it.

When detected, add `needs_attention: true` to the issue's output map. This flag is consumed by the sync pipe downstream to set priority and tags.

Implementation: add a helper function:

```go
func detectNeedsAttention(issue *Issue) bool {
    if !strings.EqualFold(issue.Status, "UAT") {
        return false
    }
    if len(issue.Comments) == 0 {
        return false
    }
    lastComment := issue.Comments[len(issue.Comments)-1]
    return lastComment.Author != issue.Assignee
}
```

The `handleSearch` function calls this for each issue and stores the result. The output representation for each issue in the list includes the `needs_attention` boolean so the format template and downstream pipes can consume it.

---

## Composition

### As a source (upstream)

Search provides bulk issue context for downstream pipes:

```
jira(action=search) -> draft(type=report)
```

Pull all assigned non-done tickets, then draft a status report from them.

```
jira(action=search, jql="project = PTP AND status = UAT") -> todo(action=store)
```

Find all UAT tickets and create local todo items from them.

### As a standalone

Direct user invocation for bulk ticket retrieval:

```
jira(action=search)
jira(action=search, jql="project = PTP AND sprint in openSprints()")
jira(action=search, limit=10)
```

### Content type conventions

| Action  | content_type | Content shape           |
| ------- | ------------ | ----------------------- |
| get     | structured   | `*Issue`                |
| comment | structured   | `*Comment` (created)    |
| update  | structured   | `*Issue` (refreshed)    |
| search  | list         | `[]Issue`               |

---

## Error Handling

| Scenario                 | Severity | Retryable | Message                                                    |
| ------------------------ | -------- | --------- | ---------------------------------------------------------- |
| Invalid JQL syntax (400) | fatal    | false     | Jira's error message verbatim                              |
| Auth failure (401/403)   | fatal    | false     | "authentication failed -- check jira.json credentials"     |
| Rate limited (429)       | error    | true      | "rate limited by Jira -- retry after {seconds}s"           |
| Server error (5xx)       | error    | true      | "Jira server error ({status}): {body}"                     |
| Empty results            | success  | n/a       | Returns empty list, no error                               |

Error classification reuses the existing `classifyJiraError` function. For search, the `id` parameter is not relevant, so the "issue not found" path does not apply — an empty JQL result set is a success with an empty list.

---

## Testing

### Test helpers

Extend `mockJiraServer` with:

```go
// POST /rest/api/3/search -> returns search results
// POST /rest/api/3/search with bad JQL -> returns 400
// POST /rest/api/3/search with pagination -> returns multi-page results
```

### Test cases

**search action:**

1. Search with default JQL -- returns list of issues, content_type is `list`.
2. Search with custom JQL -- custom query string passed through to API.
3. Search with limit -- respects max results cap.
4. Search with expand -- expand params passed to API call.
5. Needs attention detection -- UAT status + non-self comment = `needs_attention: true`.
6. Needs attention -- UAT status + self comment = `needs_attention: false`.
7. Needs attention -- non-UAT status = `needs_attention: false`.
8. Needs attention -- no comments = `needs_attention: false`.
9. Empty search results -- returns empty list, no error.
10. Search does not require `--id` flag (no error when id is empty).
11. Get/comment/update still require `--id` flag (no regression).
12. JQL syntax error from API -- fatal error with Jira's error message.
13. Pagination -- mock multi-page response, verify all pages fetched up to limit.
14. Envelope compliance -- pipe is `jira`, action is `search`, content_type is `list`, timestamp and duration are set, args are populated, error is nil on success.

### `detectNeedsAttention` unit tests

Standalone tests for the helper function:

```go
func TestDetectNeedsAttention(t *testing.T) {
    // UAT + non-self comment -> true
    // UAT + self comment -> false
    // non-UAT status -> false
    // no comments -> false
    // case-insensitive "uat" vs "UAT" -> true
}
```

---

## Checklist

```
File Layout
  [ ] no new files — changes are in existing jira.go, jira_test.go, pipe.yaml
  [ ] no configuration outside the pipe folder

Definition
  [ ] action flag values updated: [get, comment, update, search]
  [ ] id flag changed to required: false
  [ ] new flags: jql, limit
  [ ] new triggers cover search invocation phrases
  [ ] new vocabulary: search, find verbs; assigned source
  [ ] new template entry for verb-only search routing
  [ ] format template added for list content_type

Handler
  [ ] search action added to switch
  [ ] id validation moved inside get/comment/update cases (not gating search)
  [ ] handleSearch reads jql, limit, expand flags with defaults
  [ ] handleSearch calls client.SearchIssues
  [ ] handleSearch runs detectNeedsAttention on each result
  [ ] returns []Issue with content_type=list
  [ ] errors returned in envelope, never thrown

JiraClient
  [ ] SearchIssues method calls POST /rest/api/3/search
  [ ] request body includes jql, maxResults, fields, expand
  [ ] pagination: fetches all pages up to limit
  [ ] reuses existing parseIssue for response parsing
  [ ] reuses existing checkResponse for error handling

Needs Attention
  [ ] detectNeedsAttention checks UAT status (case-insensitive)
  [ ] checks that comments exist
  [ ] checks last comment author != assignee
  [ ] sets needs_attention on matching issues

Testing
  [ ] search happy path with default JQL
  [ ] search with custom JQL
  [ ] search with limit
  [ ] search with expand
  [ ] needs attention: all four conditions covered
  [ ] empty results return empty list, no error
  [ ] search without --id succeeds
  [ ] get/comment/update still require --id (no regression)
  [ ] JQL syntax error returns fatal error
  [ ] pagination with multi-page mock
  [ ] envelope compliance for search action
```
