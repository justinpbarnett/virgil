package mail

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/googleauth"
	"github.com/justinpbarnett/virgil/internal/pipe"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Message struct {
	ID       string   `json:"id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Subject  string   `json:"subject"`
	Snippet  string   `json:"snippet"`
	Body     string   `json:"body,omitempty"`
	Date     string   `json:"date"`
	Read     bool     `json:"read"`
	Labels   []string `json:"labels"`
	ThreadID string   `json:"thread_id"`
}

type MailClient interface {
	ListMessages(ctx context.Context, label string, maxResults int) ([]Message, error)
	GetMessage(ctx context.Context, messageID string) (*Message, error)
	SearchMessages(ctx context.Context, query string, maxResults int) ([]Message, error)
	SendMessage(ctx context.Context, to, cc, subject, body, threadID string) (string, error)
	ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error
	TrashMessage(ctx context.Context, messageID string) error
	SaveDraft(ctx context.Context, to, cc, subject, body, threadID string) (string, error)
}

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
		case "save":
			return handleSave(client, input, flags, logger)
		default:
			out := envelope.New("mail", action)
			out.Args = flags
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s", action))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	}
}

func handleList(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "list")
	out.Args = flags

	label := flags["label"]
	if label == "" {
		label = "INBOX"
	}

	limit := 10
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	} else if m := flags["modifier"]; m == "latest" || m == "last" || m == "newest" {
		limit = 1
	}

	logger.Debug("listing messages", "label", label, "limit", limit)
	messages, err := client.ListMessages(context.Background(), label, limit)
	if err != nil {
		logger.Error("list failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("listed", "count", len(messages))
	out.Content = messages
	out.ContentType = envelope.ContentList
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleRead(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "read")
	out.Args = flags

	messageID := flags["message_id"]
	if messageID == "" {
		out.Error = envelope.FatalError("message_id is required for read action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Debug("reading message", "message_id", messageID)
	msg, err := client.GetMessage(context.Background(), messageID)
	if err != nil {
		logger.Error("read failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("read", "message_id", messageID)
	out.Content = msg
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleSearch(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "search")
	out.Args = flags

	query := flags["query"]
	if query == "" {
		out.Error = envelope.FatalError("query is required for search action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	limit := 10
	if l, err := strconv.Atoi(flags["limit"]); err == nil && l > 0 {
		limit = l
	}

	logger.Debug("searching messages", "query", query, "limit", limit)
	messages, err := client.SearchMessages(context.Background(), query, limit)
	if err != nil {
		logger.Error("search failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("searched", "query", query, "count", len(messages))
	out.Content = messages
	out.ContentType = envelope.ContentList
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleSend(client MailClient, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "send")
	out.Args = flags

	to := flags["to"]
	if to == "" {
		out.Error = envelope.FatalError("to is required for send action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	body := envelope.ContentToText(input.Content, input.ContentType)
	if body == "" {
		out.Error = envelope.FatalError("email body is required (pass content in input envelope)")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	subject := flags["subject"]
	cc := flags["cc"]
	threadID := flags["thread_id"]

	logger.Debug("sending message", "to", to, "subject", subject)
	msgID, err := client.SendMessage(context.Background(), to, cc, subject, body, threadID)
	if err != nil {
		logger.Error("send failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("sent", "message_id", msgID)
	out.Content = map[string]string{
		"status":     "sent",
		"message_id": msgID,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleArchive(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "archive")
	out.Args = flags

	messageID := flags["message_id"]
	if messageID == "" {
		out.Error = envelope.FatalError("message_id is required for archive action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Debug("archiving message", "message_id", messageID)
	err := client.ModifyLabels(context.Background(), messageID, nil, []string{"INBOX"})
	if err != nil {
		logger.Error("archive failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("archived", "message_id", messageID)
	out.Content = map[string]string{
		"status":     "archived",
		"message_id": messageID,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleLabel(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "label")
	out.Args = flags

	messageID := flags["message_id"]
	if messageID == "" {
		out.Error = envelope.FatalError("message_id is required for label action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	label := flags["label"]
	if label == "" {
		out.Error = envelope.FatalError("label is required for label action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Debug("labeling message", "message_id", messageID, "label", label)
	err := client.ModifyLabels(context.Background(), messageID, []string{label}, nil)
	if err != nil {
		logger.Error("label failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("labeled", "message_id", messageID, "label", label)
	out.Content = map[string]string{
		"status":     "labeled",
		"message_id": messageID,
		"label":      label,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleTrash(client MailClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "trash")
	out.Args = flags

	messageID := flags["message_id"]
	if messageID == "" {
		out.Error = envelope.FatalError("message_id is required for trash action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Debug("trashing message", "message_id", messageID)
	err := client.TrashMessage(context.Background(), messageID)
	if err != nil {
		logger.Error("trash failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("trashed", "message_id", messageID)
	out.Content = map[string]string{
		"status":     "trashed",
		"message_id": messageID,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleSave(client MailClient, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("mail", "save")
	out.Args = flags

	to := flags["to"]
	if to == "" {
		out.Error = envelope.FatalError("to is required for save action")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	body := envelope.ContentToText(input.Content, input.ContentType)
	if body == "" {
		out.Error = envelope.FatalError("email body is required (pass content in input envelope)")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	subject := flags["subject"]
	cc := flags["cc"]
	threadID := flags["thread_id"]

	logger.Debug("saving draft", "to", to, "subject", subject)
	draftID, err := client.SaveDraft(context.Background(), to, cc, subject, body, threadID)
	if err != nil {
		logger.Error("save draft failed", "error", err)
		out.Error = envelope.ClassifyError("mail API", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("draft saved", "draft_id", draftID)
	out.Content = map[string]string{
		"status":   "saved",
		"draft_id": draftID,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

// GmailClient implements MailClient using the Gmail API.
type GmailClient struct {
	svc *gmailapi.Service
}

func NewGmailClient(configDir string) (*GmailClient, error) {
	httpClient, err := googleauth.NewHTTPClient(configDir,
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/gmail.send",
		"https://www.googleapis.com/auth/gmail.modify",
	)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	svc, err := gmailapi.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating gmail service: %w", err)
	}

	return &GmailClient{svc: svc}, nil
}

func (g *GmailClient) ListMessages(ctx context.Context, label string, maxResults int) ([]Message, error) {
	result, err := g.svc.Users.Messages.List("me").
		LabelIds(label).
		MaxResults(int64(maxResults)).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	return g.fetchMessageMetadata(ctx, result.Messages)
}

func (g *GmailClient) GetMessage(ctx context.Context, messageID string) (*Message, error) {
	msg, err := g.svc.Users.Messages.Get("me", messageID).
		Format("full").
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}
	m := mapMessageFull(msg)
	return &m, nil
}

func (g *GmailClient) SearchMessages(ctx context.Context, query string, maxResults int) ([]Message, error) {
	result, err := g.svc.Users.Messages.List("me").
		Q(query).
		MaxResults(int64(maxResults)).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	return g.fetchMessageMetadata(ctx, result.Messages)
}

func (g *GmailClient) fetchMessageMetadata(ctx context.Context, msgs []*gmailapi.Message) ([]Message, error) {
	messages := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		msg, err := g.svc.Users.Messages.Get("me", m.Id).
			Format("metadata").
			MetadataHeaders("From", "Subject", "Date").
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("fetching message %s: %w", m.Id, err)
		}
		messages = append(messages, mapMessage(msg))
	}
	return messages, nil
}

func (g *GmailClient) SendMessage(ctx context.Context, to, cc, subject, body, threadID string) (string, error) {
	msg := buildGmailMessage(to, cc, subject, body, threadID)
	sent, err := g.svc.Users.Messages.Send("me", msg).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return sent.Id, nil
}

func (g *GmailClient) ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error {
	req := &gmailapi.ModifyMessageRequest{
		AddLabelIds:    addLabels,
		RemoveLabelIds: removeLabels,
	}
	_, err := g.svc.Users.Messages.Modify("me", messageID, req).Context(ctx).Do()
	return err
}

func (g *GmailClient) TrashMessage(ctx context.Context, messageID string) error {
	_, err := g.svc.Users.Messages.Trash("me", messageID).Context(ctx).Do()
	return err
}

func (g *GmailClient) SaveDraft(ctx context.Context, to, cc, subject, body, threadID string) (string, error) {
	msg := buildGmailMessage(to, cc, subject, body, threadID)
	draft, err := g.svc.Users.Drafts.Create("me", &gmailapi.Draft{Message: msg}).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return draft.Id, nil
}

func buildGmailMessage(to, cc, subject, body, threadID string) *gmailapi.Message {
	raw := buildRFC2822(to, cc, subject, body)
	encoded := base64.URLEncoding.EncodeToString([]byte(raw))
	msg := &gmailapi.Message{Raw: encoded}
	if threadID != "" {
		msg.ThreadId = threadID
	}
	return msg
}

func buildRFC2822(to, cc, subject, body string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		sb.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}

func mapMessage(m *gmailapi.Message) Message {
	msg := Message{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		Snippet:  m.Snippet,
		Labels:   m.LabelIds,
		Read:     true,
	}

	for _, h := range m.Payload.Headers {
		switch h.Name {
		case "From":
			msg.From = cleanAddress(h.Value)
		case "To":
			msg.To = h.Value
		case "Subject":
			msg.Subject = h.Value
		case "Date":
			msg.Date = cleanDate(h.Value)
		}
	}

	for _, l := range m.LabelIds {
		if l == "UNREAD" {
			msg.Read = false
			break
		}
	}

	return msg
}

func mapMessageFull(m *gmailapi.Message) Message {
	msg := mapMessage(m)
	msg.Body = extractBody(m.Payload)
	return msg
}

func extractBody(payload *gmailapi.MessagePart) string {
	if payload == nil {
		return ""
	}

	if payload.Body != nil && payload.Body.Data != "" {
		if decoded, err := base64.URLEncoding.DecodeString(payload.Body.Data); err == nil {
			return string(decoded)
		}
	}

	for _, part := range payload.Parts {
		if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
			if decoded, err := base64.URLEncoding.DecodeString(part.Body.Data); err == nil {
				return string(decoded)
			}
		}
	}

	for _, part := range payload.Parts {
		if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
			if decoded, err := base64.URLEncoding.DecodeString(part.Body.Data); err == nil {
				return string(decoded)
			}
		}
	}

	return ""
}

// cleanDate parses a RFC 2822 date header and formats it simply.
func cleanDate(raw string) string {
	t, err := mail.ParseDate(raw)
	if err != nil {
		return raw
	}
	t = t.Local()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.Add(-24 * time.Hour)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())

	switch day {
	case today:
		return "Today " + t.Format("3:04 PM")
	case yesterday:
		return "Yesterday " + t.Format("3:04 PM")
	default:
		if t.Year() == now.Year() {
			return t.Format("Jan 2, 3:04 PM")
		}
		return t.Format("Jan 2 2006, 3:04 PM")
	}
}

// cleanAddress extracts a display name from a RFC 2822 address header.
func cleanAddress(raw string) string {
	if addr, err := mail.ParseAddress(raw); err == nil {
		if addr.Name != "" {
			return addr.Name
		}
		return addr.Address
	}
	return raw
}
