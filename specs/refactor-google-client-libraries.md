# Refactor: Replace Hand-Rolled Google API Calls with Official Go Client Libraries

## Metadata

type: `refactor`
task_id: `google-client-libraries`
prompt: `Swap calendar and Gmail pipes from hand-rolled REST calls to Google's official Go client libraries (google.golang.org/api/calendar/v3, google.golang.org/api/gmail/v1). Full read/write capabilities for both pipes.`

## Refactor Description

The calendar and mail pipes currently make direct HTTP calls to Google's REST APIs using `net/http` + manual JSON encoding/decoding. This works but requires maintaining URL construction, query parameter encoding, response struct definitions, pagination logic, and error mapping by hand. Google publishes official Go client libraries that provide typed structs, automatic pagination, token refresh, and complete API coverage — eliminating ~400 lines of HTTP plumbing.

The refactor also expands calendar from read-only to full read/write, adding event creation, update, and deletion. Mail gains a `save` action to persist content as Gmail drafts — enabling pipelines like `draft → mail --action=save` where the draft pipe creates content and the mail pipe handles transport.

A shared `internal/googleauth` package provides token loading with automatic refresh for both pipes, replacing the duplicated credential-reading code in each.

## Current State

### Calendar Pipe (`internal/pipes/calendar/calendar.go`)

- `GoogleCalendarClient` struct wraps `*http.Client` from oauth2
- `GetEvents()` manually builds URL, sets query params, makes HTTP request, decodes anonymous JSON structs, maps to `Event`
- Read-only: only fetches events, cannot create/update/delete
- OAuth scope: `calendar.readonly`
- No token refresh — reads token file once at startup, will fail silently when token expires

### Mail Pipe (`internal/pipes/mail/mail.go`)

- `GmailClient` struct wraps `*http.Client` from oauth2
- `doJSON()` helper handles all HTTP request/response plumbing
- Manual URL construction for every endpoint (`gmailBase + "/messages"`, etc.)
- Manual base64url encoding for send, manual MIME part extraction for read
- Two-step list pattern: GET message IDs, then loop fetching metadata one-by-one (no batching)
- `fetchMessageMetadata()` makes N sequential HTTP calls for N messages
- OAuth scopes: `gmail.readonly`, `gmail.send`, `gmail.modify`
- Same token refresh issue as calendar

### Auth (`cmd/auth/main.go`)

- Saves raw `oauth2.Token` to `google-token.json`
- Pipes load token and create `config.Client()` — no `TokenSource` wrapping, so refresh tokens are never used

## Target State

### Calendar Pipe

- `GoogleCalendarClient` wraps `*calendar.Service` from `google.golang.org/api/calendar/v3`
- `GetEvents()` uses `service.Events.List(calendarID).TimeMin(...).TimeMax(...).SingleEvents(true).OrderBy("startTime").Do()`
- New methods via expanded `CalendarClient` interface:
  - `CreateEvent(ctx, calendarID, title, start, end, location, description) (*Event, error)`
  - `UpdateEvent(ctx, calendarID, eventID, title, start, end, location, description) (*Event, error)`
  - `DeleteEvent(ctx, calendarID, eventID) error`
- OAuth scope upgraded: `calendar` (read/write) replaces `calendar.readonly`
- Maps `calendar.Event` → local `Event` struct (keeping existing fields + adding `ID` and `Description`)

### Mail Pipe

- `GmailClient` wraps `*gmail.Service` from `google.golang.org/api/gmail/v1`
- All methods use typed service calls:
  - `ListMessages()` → `service.Users.Messages.List("me").LabelIds(label).MaxResults(limit).Do()` + metadata fetch
  - `GetMessage()` → `service.Users.Messages.Get("me", id).Format("full").Do()`
  - `SearchMessages()` → `service.Users.Messages.List("me").Q(query).MaxResults(limit).Do()`
  - `SendMessage()` → `service.Users.Messages.Send("me", &gmail.Message{Raw: encoded}).Do()`
  - `ModifyLabels()` → `service.Users.Messages.Modify("me", id, &gmail.ModifyMessageRequest{...}).Do()`
  - `TrashMessage()` → `service.Users.Messages.Trash("me", id).Do()`
- New method for Gmail draft persistence:
  - `SaveDraft(ctx, to, cc, subject, body, threadID string) (string, error)` — saves content as a Gmail draft, returns `draft_id`. This is a transport action: the mail pipe receives finished content (from the draft pipe or another content pipe) in the input envelope and persists it to Gmail. The draft pipe owns content creation; the mail pipe owns Gmail operations.
- OAuth scopes unchanged (already has readonly + send + modify)

### Auth

- Token loading wrapped with `oauth2.ReuseTokenSource(token, config.TokenSource(ctx))` so refresh tokens work
- Token file updated after refresh (write-back on token change)
- Scope updated to include `calendar` (read/write) — users must re-auth once

### Shared Auth (`internal/googleauth`)

- New package `internal/googleauth` provides `NewHTTPClient(configDir string, scopes ...string) (*http.Client, error)`
  - Reads `google-credentials.json` and `google-token.json` from `configDir`
  - Creates `oauth2.Config` from credentials with provided scopes
  - Wraps token with `oauth2.ReuseTokenSource` for automatic refresh
  - Returns authenticated `*http.Client`
- Both calendar and mail pipes import this package — it's a build-time dependency only (each pipe still compiles to its own binary)
- New dependency: `google.golang.org/api` (covers both calendar/v3 and gmail/v1)

## Relevant Files

- `internal/pipes/calendar/calendar.go` — Replace `GoogleCalendarClient` implementation, expand `CalendarClient` interface, add `ID`/`Description` to `Event` struct
- `internal/pipes/calendar/calendar_test.go` — Add tests for new create/update/delete actions
- `internal/pipes/calendar/cmd/main.go` — Update client initialization to use new constructor
- `internal/pipes/calendar/pipe.yaml` — Add triggers, flags, and vocabulary for create/update/delete actions
- `internal/pipes/mail/mail.go` — Replace `GmailClient` implementation with `gmail.Service`-based calls
- `internal/pipes/mail/mail_test.go` — Add tests for save action
- `internal/pipes/mail/cmd/main.go` — Update client initialization to use new constructor
- `internal/pipes/mail/pipe.yaml` — Add trigger and flag for save action
- `cmd/auth/main.go` — Update calendar scope from `calendar.readonly` to `calendar` (read/write)
- `go.mod` / `go.sum` — Add `google.golang.org/api` dependency

### New Files

- `internal/googleauth/auth.go` — Shared Google OAuth2 helper with token refresh

## Migration Strategy

This is a breaking change to the Google API client internals only. The `CalendarClient` and `MailClient` interfaces change (new methods added), but the handler functions and envelope contracts remain identical. Since you're the sole user:

1. Add the `google.golang.org/api` dependency
2. Rewrite `GoogleCalendarClient` and `GmailClient` in-place
3. Expand interfaces with new methods
4. Add handler cases for new actions
5. Update `pipe.yaml` files with new triggers/flags
6. Update auth scope and re-run `just auth`

No backwards compatibility shims needed.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add Google API dependency

- Run `go get google.golang.org/api/calendar/v3 google.golang.org/api/gmail/v1`

### 2. Create shared auth package

- Create `internal/googleauth/auth.go` with `NewHTTPClient(configDir string, scopes ...string) (*http.Client, error)`:
  - Reads `google-credentials.json` and `google-token.json` from `configDir`
  - Creates `oauth2.Config` from credentials with provided scopes
  - Unmarshals token, wraps with `oauth2.ReuseTokenSource(token, config.TokenSource(ctx))` for automatic refresh
  - Returns `config.Client(ctx, tokenSource)`
- This replaces the duplicated credential-reading code in both `NewGoogleClient()` and `NewGmailClient()`

### 3. Rewrite calendar client

- Add `ID` and `Description` fields to the `Event` struct
- Expand `CalendarClient` interface:
  ```go
  type CalendarClient interface {
      GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error)
      CreateEvent(ctx context.Context, calendarID, title string, start, end time.Time, location, description string) (*Event, error)
      UpdateEvent(ctx context.Context, calendarID, eventID, title string, start, end time.Time, location, description string) (*Event, error)
      DeleteEvent(ctx context.Context, calendarID, eventID string) error
  }
  ```
- Replace `GoogleCalendarClient` internals:
  - Store `*calendar.Service` instead of `*http.Client`
  - `NewGoogleClient(configDir)` creates service via `calendar.NewService(ctx, option.WithHTTPClient(httpClient))`
  - `GetEvents()` uses `svc.Events.List(calendarID).TimeMin(...).TimeMax(...).SingleEvents(true).OrderBy("startTime").Do()`
  - Map `calendar.Event` fields to local `Event` struct (summary→Title, start.dateTime/date→Start, location→Location, id→ID, description→Description)
  - `CreateEvent()` builds `calendar.Event` struct → `svc.Events.Insert(calendarID, event).Do()`
  - `UpdateEvent()` builds `calendar.Event` struct → `svc.Events.Update(calendarID, eventID, event).Do()`
  - `DeleteEvent()` → `svc.Events.Delete(calendarID, eventID).Do()`
- Remove the manual URL construction and JSON decoding code

### 4. Add calendar handler actions

- In `NewHandler()`, change from single-action handler to action-dispatched (like mail pipe):
  - `action` flag defaults to `"list"` (preserves current behavior)
  - Add `case "create"`: requires `title`, `start` flags; optional `end`, `location`, `description`; parse start/end with `whenParser`
  - Add `case "update"`: requires `event_id`, plus any fields to change
  - Add `case "delete"`: requires `event_id`
- Return `ContentStructured` for create/update/delete with status + event details

### 5. Update calendar pipe.yaml

- Add new triggers:
  - Exact: "create an event", "add to my calendar", "schedule a meeting"
  - Keywords: add `create`, `schedule`, `add`, `cancel`, `delete`, `update`, `move`, `reschedule`
  - Patterns: "schedule {topic} for {modifier}", "cancel my {topic}", "move my {topic} to {modifier}"
- Add new flags:
  - `action`: values `[list, create, update, delete]`, default `list`
  - `event_id`: for update/delete
  - `title`: event title for create/update
  - `start`: start time for create/update
  - `end`: end time for create/update (optional, defaults to start + 1 hour)
  - `location`: event location for create/update
  - `description`: event description for create/update
- Add vocabulary:
  - Verbs: `schedule → calendar`, `cancel → calendar`, `reschedule → calendar`
  - Modifiers: keep existing, add `next week → next-week`
- Add format templates for `structured` content type (create/update/delete confirmations)

### 6. Rewrite Gmail client

- Replace `GmailClient` internals:
  - Store `*gmail.Service` instead of `*http.Client`
  - `NewGmailClient(configDir)` creates service via `gmail.NewService(ctx, option.WithHTTPClient(httpClient))`
  - `ListMessages()` → `svc.Users.Messages.List("me").LabelIds(label).MaxResults(int64(limit)).Do()`, then fetch metadata with `svc.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("From", "Subject", "Date").Do()`
  - `GetMessage()` → `svc.Users.Messages.Get("me", id).Format("full").Do()`, extract body from `gmail.MessagePart`
  - `SearchMessages()` → `svc.Users.Messages.List("me").Q(query).MaxResults(int64(limit)).Do()`
  - `SendMessage()` → build RFC 2822 message, base64url encode → `svc.Users.Messages.Send("me", &gmail.Message{Raw: encoded}).Do()`
  - `ModifyLabels()` → `svc.Users.Messages.Modify("me", id, &gmail.ModifyMessageRequest{AddLabelIds: add, RemoveLabelIds: remove}).Do()`
  - `TrashMessage()` → `svc.Users.Messages.Trash("me", id).Do()`
- Remove `doJSON()` helper and all manual HTTP code
- Add new method:
  - `SaveDraft(ctx, to, cc, subject, body, threadID)` — builds RFC 2822 message, base64url encodes → `svc.Users.Drafts.Create("me", &gmail.Draft{Message: &gmail.Message{Raw: encoded}}).Do()`, returns draft ID

### 7. Add mail save handler action

- Add `case "save"` to action dispatch in `NewHandler()`:
  - Extracts body from input envelope via `envelope.ContentToText()`
  - Requires `to` flag (fatal error if missing), body must be non-empty (fatal error if missing)
  - Optional: `cc`, `subject`, `thread_id` flags
  - Calls `client.SaveDraft(ctx, to, cc, subject, body, threadID)`
  - Returns `ContentStructured` with `{"status": "saved", "draft_id": "..."}`
- This action is the transport endpoint for pipelines like `draft --type=email | mail --action=save`

### 8. Update mail pipe.yaml

- Add new triggers:
  - Exact: "save email as draft", "save as draft"
  - Patterns: "save email to {topic} as draft"
- Add new flag value:
  - `action` values: add `save` to existing list `[list, read, search, send, archive, label, trash, save]`
- No new vocabulary — "save" is handled by the planner when building pipelines, not by direct user trigger. The `save` action is primarily a pipeline target, not a top-level user command.

### 9. Update auth scopes

- In `cmd/auth/main.go`, change `calendar.readonly` scope to `https://www.googleapis.com/auth/calendar`
- After deploying, re-run `just auth` to get a new token with the expanded scope

### 10. Update calendar and mail cmd/main.go

- Update `NewGoogleClient()` and `NewGmailClient()` calls if constructor signatures changed
- Both should pass `configDir` as before — internal constructor handles `calendar.NewService` / `gmail.NewService` setup

## Testing Strategy

### Existing tests must still pass

The `CalendarClient` and `MailClient` interfaces are used by mock implementations in tests. Since we're expanding the interfaces (not changing existing method signatures), existing mock implementations need the new methods added but all existing test cases remain valid.

### Calendar tests to update (`calendar_test.go`)

- Add `CreateEvent`, `UpdateEvent`, `DeleteEvent` to `mockCalendarClient`
- Add tests:
  - `TestCalendarCreateEvent` — happy path, verify returned event
  - `TestCalendarCreateEventMissingTitle` — fatal error
  - `TestCalendarUpdateEvent` — happy path
  - `TestCalendarUpdateEventMissingID` — fatal error
  - `TestCalendarDeleteEvent` — happy path
  - `TestCalendarDeleteEventMissingID` — fatal error
  - `TestCalendarDefaultActionIsList` — no action flag defaults to list (backwards compat)
  - Envelope compliance tests for each new action

### Mail tests to update (`mail_test.go`)

- Add `SaveDraft` to `mockMailClient`
- Add tests:
  - `TestSaveDraft` — happy path with to/subject/body, verify returned draft_id
  - `TestSaveDraftMissingTo` — fatal error
  - `TestSaveDraftEmptyBody` — fatal error (body comes from input envelope)
  - `TestSaveDraftWithThread` — thread_id flag passed through
  - `TestSaveDraftEnvelopeCompliance` — verify pipe="mail", action="save", ContentType=structured

## Risk Assessment

- **Token refresh**: The current code doesn't handle token refresh. Wrapping with `ReuseTokenSource` fixes this but requires the refresh token to be present in `google-token.json` — it should be, since `cmd/auth` uses `AccessTypeOffline` + `ApprovalForce`.
- **Scope upgrade**: Changing from `calendar.readonly` to `calendar` requires re-running `just auth`. Existing token will fail for write operations until re-authed.
- **No routing conflicts**: The mail `save` action is a pipeline target, not a direct user verb. The draft pipe owns all content-creation language ("write", "draft", "compose"). The mail pipe owns all Gmail transport operations. No vocabulary overlap.
- **Pipeline confirmation gate**: The ideal flow for "write an email" is `draft → mail --action=save → [confirm] → mail --action=send`. The confirmation gate doesn't exist yet in the pipeline layer — this refactor doesn't add it, but the `save` action is designed to support it (returns `draft_id` which the `send` step can reference). Confirmation should be specced separately.
- **google.golang.org/api dependency size**: This is a large module. It will increase binary size and compile time. Acceptable tradeoff for maintained, typed API coverage.

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
just test
just lint
just build
```

## Open Questions (Unresolved)

None — all decisions resolved.

## Sub-Tasks

Single task — no decomposition needed. The refactor touches two pipe implementations, their tests, their configs, and the auth tool. All changes are tightly coupled and should land together.
