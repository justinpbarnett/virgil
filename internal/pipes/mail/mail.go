package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
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

// GmailClient implements MailClient using the Gmail REST API.
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

const gmailBase = "https://www.googleapis.com/gmail/v1/users/me"

func (g *GmailClient) ListMessages(ctx context.Context, label string, maxResults int) ([]Message, error) {
	u, _ := url.Parse(gmailBase + "/messages")
	q := u.Query()
	q.Set("labelIds", label)
	q.Set("maxResults", strconv.Itoa(maxResults))
	u.RawQuery = q.Encode()

	var list struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := g.doJSON(ctx, "GET", u.String(), nil, &list); err != nil {
		return nil, err
	}

	return g.fetchMessageMetadata(ctx, list.Messages)
}

func (g *GmailClient) GetMessage(ctx context.Context, messageID string) (*Message, error) {
	u := fmt.Sprintf("%s/messages/%s?format=full", gmailBase, messageID)

	var raw gmailMessage
	if err := g.doJSON(ctx, "GET", u, nil, &raw); err != nil {
		return nil, err
	}

	return raw.toMessage(), nil
}

func (g *GmailClient) SearchMessages(ctx context.Context, query string, maxResults int) ([]Message, error) {
	u, _ := url.Parse(gmailBase + "/messages")
	q := u.Query()
	q.Set("q", query)
	q.Set("maxResults", strconv.Itoa(maxResults))
	u.RawQuery = q.Encode()

	var list struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := g.doJSON(ctx, "GET", u.String(), nil, &list); err != nil {
		return nil, err
	}

	return g.fetchMessageMetadata(ctx, list.Messages)
}

func (g *GmailClient) SendMessage(ctx context.Context, to, cc, subject, body, threadID string) (string, error) {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		msg.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	encoded := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	payload := map[string]string{"raw": encoded}
	if threadID != "" {
		payload["threadId"] = threadID
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := g.doJSON(ctx, "POST", gmailBase+"/messages/send", payload, &result); err != nil {
		return "", err
	}

	return result.ID, nil
}

func (g *GmailClient) ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error {
	u := fmt.Sprintf("%s/messages/%s/modify", gmailBase, messageID)
	payload := map[string][]string{
		"addLabelIds":    addLabels,
		"removeLabelIds": removeLabels,
	}
	return g.doJSON(ctx, "POST", u, payload, nil)
}

func (g *GmailClient) TrashMessage(ctx context.Context, messageID string) error {
	u := fmt.Sprintf("%s/messages/%s/trash", gmailBase, messageID)
	return g.doJSON(ctx, "POST", u, nil, nil)
}

// doJSON performs an HTTP request, optionally encoding a JSON body, and decoding the response.
func (g *GmailClient) doJSON(ctx context.Context, method, rawURL string, body any, dest any) error {
	var reqBody *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reqBody = strings.NewReader(string(b))
	}

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(ctx, method, rawURL, reqBody)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequestWithContext(ctx, method, rawURL, nil)
	}
	if err != nil {
		return err
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Gmail API returned %d", resp.StatusCode)
	}

	if dest != nil {
		if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
}

// fetchMessageMetadata fetches metadata for a list of message IDs.
func (g *GmailClient) fetchMessageMetadata(ctx context.Context, ids []struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}) ([]Message, error) {
	messages := make([]Message, 0, len(ids))
	for _, m := range ids {
		u := fmt.Sprintf("%s/messages/%s?format=metadata&metadataHeaders=From&metadataHeaders=Subject&metadataHeaders=Date", gmailBase, m.ID)

		var raw gmailMessage
		if err := g.doJSON(ctx, "GET", u, nil, &raw); err != nil {
			return nil, fmt.Errorf("fetching message %s: %w", m.ID, err)
		}

		msg := raw.toMessage()
		messages = append(messages, *msg)
	}
	return messages, nil
}

// gmailMessage represents the raw Gmail API message structure.
type gmailMessage struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	LabelIDs []string `json:"labelIds"`
	Snippet  string   `json:"snippet"`
	Payload  struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		MimeType string `json:"mimeType"`
		Body     struct {
			Data string `json:"data"`
		} `json:"body"`
		Parts []struct {
			MimeType string `json:"mimeType"`
			Body     struct {
				Data string `json:"data"`
			} `json:"body"`
		} `json:"parts"`
	} `json:"payload"`
}

func (m *gmailMessage) toMessage() *Message {
	msg := &Message{
		ID:       m.ID,
		ThreadID: m.ThreadID,
		Snippet:  m.Snippet,
		Labels:   m.LabelIDs,
		Read:     true,
	}

	for _, h := range m.Payload.Headers {
		switch h.Name {
		case "From":
			msg.From = h.Value
		case "To":
			msg.To = h.Value
		case "Subject":
			msg.Subject = h.Value
		case "Date":
			msg.Date = h.Value
		}
	}

	// Check if UNREAD label is present
	for _, l := range m.LabelIDs {
		if l == "UNREAD" {
			msg.Read = false
			break
		}
	}

	// Extract body from payload
	msg.Body = m.extractBody()

	return msg
}

func (m *gmailMessage) extractBody() string {
	// Try direct body first (simple messages)
	if m.Payload.Body.Data != "" {
		if decoded, err := base64.URLEncoding.DecodeString(m.Payload.Body.Data); err == nil {
			return string(decoded)
		}
	}

	// Try parts (multipart messages) — prefer text/plain
	for _, part := range m.Payload.Parts {
		if part.MimeType == "text/plain" && part.Body.Data != "" {
			if decoded, err := base64.URLEncoding.DecodeString(part.Body.Data); err == nil {
				return string(decoded)
			}
		}
	}

	// Fall back to text/html
	for _, part := range m.Payload.Parts {
		if part.MimeType == "text/html" && part.Body.Data != "" {
			if decoded, err := base64.URLEncoding.DecodeString(part.Body.Data); err == nil {
				return string(decoded)
			}
		}
	}

	return ""
}
