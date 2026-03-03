# Feature: Mail Pipe

## Metadata

type: `feat`
task_id: `mail-pipe`
prompt: `Add a deterministic mail pipe that connects to Gmail via OAuth2 and supports reading, listing, searching, sending, archiving, labeling, and trashing emails. This pipe handles email operations only — it does not write or summarize email content (those are handled by the draft and other non-deterministic pipes).`

## Feature Description

The `mail` pipe is a deterministic pipe that interacts with Gmail via the Gmail API. It follows the same pattern as the `calendar` pipe: OAuth2 credentials stored in `~/.config/virgil/`, a `MailClient` interface for testability, and action-based dispatch for multiple operations.

This pipe is the email analog of the calendar pipe — it provides raw data access and operations. It does not compose email bodies or summarize threads. Content creation is handled upstream by pipes like `draft` (with `--type=email`), and the mail pipe receives the finished content in its input envelope for sending. Similarly, when reading emails, it returns structured data that downstream pipes (chat, draft) can summarize or act on.

Operations: list (inbox or label), read (single message), search (query), send (from envelope content), archive, label, and trash.

## User Story

As a user
I want to read, search, manage, and send emails through Virgil
So that I can handle email without leaving my workflow

## Relevant Files

### Existing Files (Reference)

- `internal/pipes/calendar/calendar.go` — reference deterministic pipe with Google API
- `internal/pipes/calendar/pipe.yaml` — reference pipe definition
- `internal/pipes/calendar/cmd/main.go` — reference subprocess entry point
- `internal/pipes/memory/memory.go` — reference action-based dispatch pattern
- `cmd/auth/main.go` — existing OAuth2 flow (needs Gmail scope addition)
- `SETUP.md` — setup documentation (needs Gmail section)

### New Files

- `internal/pipes/mail/pipe.yaml` — pipe definition
- `internal/pipes/mail/mail.go` — handler implementation + Gmail client
- `internal/pipes/mail/mail_test.go` — handler tests
- `internal/pipes/mail/cmd/main.go` — subprocess entry point

### Modified Files

- `cmd/auth/main.go` — add Gmail scopes to OAuth2 flow
- `SETUP.md` — add Gmail API setup instructions

## Implementation Plan

### Phase 1: Gmail API Setup

Update the OAuth2 auth flow to request Gmail scopes alongside calendar scopes. Update `SETUP.md` with Gmail-specific setup instructions. The same `google-credentials.json` and `google-token.json` files are reused — Gmail is an additional scope on the same Google OAuth2 consent.

### Phase 2: Pipe Definition

Create `pipe.yaml` with mail pipe identity, triggers, flags, vocabulary, templates, and format templates.

### Phase 3: Gmail Client

Implement `MailClient` interface and `GmailClient` struct. The client wraps raw Gmail API calls (list messages, get message, send message, modify labels). Follows the same pattern as `GoogleCalendarClient`.

### Phase 4: Handler

Implement action-based dispatch: list, read, search, send, archive, label, trash. Each action is a function that receives the client, input envelope, and flags, and returns an envelope.

### Phase 5: Tests

Test all actions with a mock client. Test error handling, missing flags, empty input, envelope compliance.

## Step by Step Tasks

### 1. Create `pipe.yaml`

Create `internal/pipes/mail/pipe.yaml`:

```yaml
name: mail
description: Reads, searches, sends, and manages emails from Gmail.
category: comms

triggers:
  exact:
    - "check my email"
    - "check my mail"
    - "check my inbox"
    - "what's in my inbox"
  keywords:
    - email
    - mail
    - inbox
    - gmail
    - message
  patterns:
    - "check my email {modifier}"
    - "read email from {topic}"
    - "send email to {topic}"
    - "search email for {topic}"
    - "archive email {topic}"

flags:
  action:
    description: Which email operation to perform.
    values: [list, read, search, send, archive, label, trash]
    default: list

  label:
    description: Gmail label to list or apply.
    default: INBOX

  query:
    description: Gmail search query (same syntax as Gmail search bar).
    default: ""

  message_id:
    description: Gmail message ID for read/archive/label/trash operations.
    default: ""

  limit:
    description: Maximum number of messages to return.
    default: "10"

  to:
    description: Recipient email address (for send action).
    default: ""

  subject:
    description: Email subject line (for send action).
    default: ""

  cc:
    description: CC recipient email address (for send action).
    default: ""

  thread_id:
    description: Thread ID for reading a thread or replying within a thread.
    default: ""

format:
  list: |
    {{if eq .Count 0}}No messages.{{else}}{{.Count}} message{{if gt .Count 1}}s{{end}}:{{range .Items}}
    - {{if not .read}}* {{end}}{{.from}} — {{.subject}} ({{.date}}){{end}}{{end}}
  structured: |
    {{if .status}}{{.status}}{{end}}{{if .message_id}} [{{.message_id}}]{{end}}

vocabulary:
  verbs:
    email: mail
    mail: mail
  types:
    email: email
    mail: email
  sources:
    email: mail
    mail: mail
    inbox: mail
  modifiers: {}

templates:
  priority: 50
  entries:
    - requires: [verb, type, source]
      plan:
        - pipe: "{source}"
          flags: { action: read }
        - pipe: "{verb}"
          flags: { type: "{type}" }

    - requires: [verb, source]
      plan:
        - pipe: "{source}"
          flags: { action: list }

    - requires: [verb, type]
      plan:
        - pipe: "{verb}"
          flags: { type: "{type}" }
```

### 2. Define data types

In `internal/pipes/mail/mail.go`, define:

```go
type Message struct {
    ID      string `json:"id"`
    From    string `json:"from"`
    To      string `json:"to"`
    Subject string `json:"subject"`
    Snippet string `json:"snippet"`
    Body    string `json:"body,omitempty"`
    Date    string `json:"date"`
    Read    bool   `json:"read"`
    Labels  []string `json:"labels"`
    ThreadID string `json:"thread_id"`
}

type MailClient interface {
    ListMessages(ctx context.Context, label string, maxResults int) ([]Message, error)
    GetMessage(ctx context.Context, messageID string) (*Message, error)
    SearchMessages(ctx context.Context, query string, maxResults int) ([]Message, error)
    SendMessage(ctx context.Context, to, cc, subject, body, threadID string) (string, error)
    ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error
    TrashMessage(ctx context.Context, messageID string) error
}
```

### 3. Implement the Gmail client

In `internal/pipes/mail/mail.go`, implement `GmailClient`:

```go
type GmailClient struct {
    httpClient *http.Client
}

func NewGmailClient(configDir string) (*GmailClient, error) {
    credPath := filepath.Join(configDir, "google-credentials.json")
    credData, err := os.ReadFile(credPath)
    if err != nil {
        return nil, fmt.Errorf("reading credentials: %w (see SETUP.md)", err)
    }

    config, err := google.ConfigFromJSON(credData,
        "https://www.googleapis.com/auth/gmail.readonly",
        "https://www.googleapis.com/auth/gmail.send",
        "https://www.googleapis.com/auth/gmail.modify",
    )
    if err != nil {
        return nil, fmt.Errorf("parsing credentials: %w", err)
    }

    tokenPath := filepath.Join(configDir, "google-token.json")
    tokenData, err := os.ReadFile(tokenPath)
    if err != nil {
        return nil, fmt.Errorf("reading token: %w (run token flow first, see SETUP.md)", err)
    }

    var token oauth2.Token
    if err := json.Unmarshal(tokenData, &token); err != nil {
        return nil, fmt.Errorf("parsing token: %w", err)
    }

    return &GmailClient{
        httpClient: config.Client(context.Background(), &token),
    }, nil
}
```

**Client methods** — each method calls the Gmail REST API directly (no SDK dependency, same pattern as calendar pipe):

- `ListMessages` — `GET /gmail/v1/users/me/messages?labelIds={label}&maxResults={limit}`, then batch-fetch headers for each message ID returned.
- `GetMessage` — `GET /gmail/v1/users/me/messages/{id}?format=full`, decode MIME parts to extract plain text body.
- `SearchMessages` — `GET /gmail/v1/users/me/messages?q={query}&maxResults={limit}`, then batch-fetch headers.
- `SendMessage` — `POST /gmail/v1/users/me/messages/send` with base64url-encoded RFC 2822 message.
- `ModifyLabels` — `POST /gmail/v1/users/me/messages/{id}/modify` with `addLabelIds` / `removeLabelIds`.
- `TrashMessage` — `POST /gmail/v1/users/me/messages/{id}/trash`.

Note: `ListMessages` and `SearchMessages` require two API calls — the list endpoint returns only message IDs, then each message's headers (From, Subject, Date) must be fetched separately. Use `format=metadata&metadataHeaders=From&metadataHeaders=Subject&metadataHeaders=Date` for efficiency.

### 4. Implement the handler

Following the memory pipe's action dispatch pattern:

```go
func NewHandler(client MailClient, logger *slog.Logger) pipe.Handler {
    if logger == nil {
        logger = slog.Default()
    }
    return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
        if client == nil {
            out := envelope.New("mail", "error")
            out.Args = flags
            out.Duration = time.Since(out.Timestamp)
            out.Error = envelope.FatalError("no mail client configured — see SETUP.md for Gmail API setup")
            return out
        }

        action := flags["action"]
        if action == "" {
            action = "list"
        }

        switch action {
        case "list":
            return handleList(client, input, flags, logger)
        case "read":
            return handleRead(client, input, flags, logger)
        case "search":
            return handleSearch(client, input, flags, logger)
        case "send":
            return handleSend(client, input, flags, logger)
        case "archive":
            return handleArchive(client, input, flags, logger)
        case "label":
            return handleLabel(client, input, flags, logger)
        case "trash":
            return handleTrash(client, input, flags, logger)
        default:
            out := envelope.New("mail", action)
            out.Args = flags
            out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s", action))
            out.Duration = time.Since(out.Timestamp)
            return out
        }
    }
}
```

**Action handlers:**

**`handleList`** — Lists messages from a label (default INBOX).
- Reads `label` flag (default `INBOX`), `limit` flag (default `10`, parse to int).
- Calls `client.ListMessages(ctx, label, limit)`.
- Returns envelope with `content_type: list`, content is `[]Message` (without body — snippets only).
- Action name: `list`.

**`handleRead`** — Reads a single message by ID.
- Requires `message_id` flag. Returns fatal error if missing.
- Calls `client.GetMessage(ctx, messageID)`.
- Returns envelope with `content_type: structured`, content is `*Message` (with full body).
- Action name: `read`.

**`handleSearch`** — Searches messages by Gmail query syntax.
- Reads `query` flag. Returns fatal error if empty.
- Reads `limit` flag (default `10`).
- Calls `client.SearchMessages(ctx, query, limit)`.
- Returns envelope with `content_type: list`, content is `[]Message` (without body).
- Action name: `search`.

**`handleSend`** — Sends an email.
- Reads `to` flag. Returns fatal error if empty.
- Reads `subject` flag. Defaults to empty string if not provided.
- Reads `cc` flag (optional).
- Reads `thread_id` flag (optional, for replies).
- Email body comes from `input.Content` (the output of a draft pipe or user-provided text). Returns fatal error if content is empty.
- Calls `client.SendMessage(ctx, to, cc, subject, body, threadID)`.
- Returns envelope with `content_type: structured`, content is a map with `status`, `message_id`.
- Action name: `send`.

**`handleArchive`** — Archives a message (removes INBOX label).
- Requires `message_id` flag. Returns fatal error if missing.
- Calls `client.ModifyLabels(ctx, messageID, nil, []string{"INBOX"})`.
- Returns envelope with `content_type: structured`, content is a map with `status`, `message_id`.
- Action name: `archive`.

**`handleLabel`** — Adds a label to a message.
- Requires `message_id` flag. Returns fatal error if missing.
- Requires `label` flag. Returns fatal error if missing.
- Calls `client.ModifyLabels(ctx, messageID, []string{label}, nil)`.
- Returns envelope with `content_type: structured`, content is a map with `status`, `message_id`, `label`.
- Action name: `label`.

**`handleTrash`** — Moves a message to trash.
- Requires `message_id` flag. Returns fatal error if missing.
- Calls `client.TrashMessage(ctx, messageID)`.
- Returns envelope with `content_type: structured`, content is a map with `status`, `message_id`.
- Action name: `trash`.

### 5. Create subprocess entry point

Create `internal/pipes/mail/cmd/main.go`:

```go
package main

import (
    "os"

    "github.com/justinpbarnett/virgil/internal/pipehost"
    "github.com/justinpbarnett/virgil/internal/pipes/mail"
)

func main() {
    logger := pipehost.NewPipeLogger("mail")

    userDir := os.Getenv(pipehost.EnvUserDir)

    client, err := mail.NewGmailClient(userDir)
    if err != nil {
        logger.Warn("mail client unavailable", "error", err)
        pipehost.Run(mail.NewHandler(nil, logger), nil)
        return
    }

    logger.Info("initialized")
    pipehost.Run(mail.NewHandler(client, logger), nil)
}
```

### 6. Update OAuth2 auth flow

Modify `cmd/auth/main.go` to request Gmail scopes alongside calendar scopes:

```go
// Before:
config, err := google.ConfigFromJSON(credData, "https://www.googleapis.com/auth/calendar.readonly")

// After:
config, err := google.ConfigFromJSON(credData,
    "https://www.googleapis.com/auth/calendar.readonly",
    "https://www.googleapis.com/auth/gmail.readonly",
    "https://www.googleapis.com/auth/gmail.send",
    "https://www.googleapis.com/auth/gmail.modify",
)
```

Users with existing tokens need to re-run `just auth` to grant the new scopes. The existing token file is overwritten.

### 7. Update SETUP.md

Add a Gmail API section to `SETUP.md` (see Modified Files section below for exact content).

### 8. Write tests

Create `internal/pipes/mail/mail_test.go`:

- **TestListMessages** — happy path: mock client returns messages, verify envelope fields and content type.
- **TestListMessagesEmpty** — mock client returns no messages, verify empty list.
- **TestListMessagesCustomLabel** — pass `label: SENT`, verify client receives correct label.
- **TestReadMessage** — happy path: mock client returns full message, verify content type is structured.
- **TestReadMessageMissingID** — no `message_id` flag, verify fatal error.
- **TestSearchMessages** — happy path with query, verify envelope.
- **TestSearchMessagesEmptyQuery** — no query flag, verify fatal error.
- **TestSendMessage** — happy path: input envelope has content, `to` flag set, verify client called correctly.
- **TestSendMessageMissingTo** — no `to` flag, verify fatal error.
- **TestSendMessageEmptyBody** — empty input content, verify fatal error.
- **TestSendMessageWithThread** — `thread_id` flag set, verify client receives thread ID.
- **TestArchiveMessage** — happy path, verify client called with correct label removal.
- **TestArchiveMessageMissingID** — no `message_id`, verify fatal error.
- **TestLabelMessage** — happy path with `message_id` and `label` flags.
- **TestLabelMessageMissingLabel** — `message_id` present but no `label`, verify fatal error.
- **TestTrashMessage** — happy path, verify client called.
- **TestTrashMessageMissingID** — no `message_id`, verify fatal error.
- **TestUnknownAction** — action flag set to invalid value, verify fatal error.
- **TestNilClient** — nil client, verify fatal error with setup instructions.
- **TestAPIError** — mock client returns error, verify classified error envelope.
- **TestEnvelopeCompliance** — for each action, verify all required envelope fields are present.
- **TestDefaultAction** — no action flag, verify defaults to list.
- **TestDefaultLimit** — no limit flag, verify defaults to 10.

## Testing Strategy

### Unit Tests
- All action handlers tested with mock `MailClient` implementation
- Template resolution and prompt construction not needed (deterministic pipe)
- Gmail API response parsing tested separately

### Edge Cases
- Message with no subject (empty string)
- Message with no body (empty MIME parts)
- Very long email bodies (truncation is the caller's concern, not this pipe's)
- HTML-only emails (extract plain text or return HTML with a note in content)
- Rate limiting from Gmail API (retryable error)
- Expired OAuth token (fatal error with re-auth instructions)
- Unicode in subject/body/sender names

## Risk Assessment

- **Low risk — follows established patterns.** The calendar pipe already proved the Google API + OAuth2 + deterministic handler pattern. Mail is a larger surface area (7 actions vs 1) but same architecture.
- **Medium risk — Gmail API has a two-step list pattern.** Listing messages returns only IDs, requiring batch-fetch for metadata. This is more complex than the calendar API and adds latency. Mitigate with `format=metadata` to fetch only headers.
- **Low risk — scope escalation.** Adding Gmail scopes to the existing OAuth2 flow means users must re-authorize. The auth flow already handles this — `ApprovalForce` triggers a fresh consent screen.
- **Medium risk — send action safety.** Sending email is a side effect with real-world impact. The pipe should be clear about what it's sending and to whom. The `to` flag is required, and the body comes from the envelope (never generated by this pipe).

## Validation Commands

```bash
go test ./internal/pipes/mail/... -v -count=1
go build ./internal/pipes/mail/cmd/
```

## Sub-Tasks

Single task — no decomposition needed.
