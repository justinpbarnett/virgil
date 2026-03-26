package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
	vgoogle "github.com/justinpbarnett/virgil/internal/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// RegisterEmailTools registers email_list, email_read, email_send, and email_categorize.
func RegisterEmailTools(reg *Registry, cfg *config.Config) {
	clients := make(map[string]*gmail.Service)
	accounts := make(map[string]config.GoogleAccountConfig)
	labelCache := make(map[string]string) // "account:labelName" -> labelID
	var labelCacheMu sync.Mutex
	var initErrs []string

	for name, acct := range cfg.Channels.Email.Accounts {
		if acct.CredentialsPath == "" {
			continue
		}
		httpClient, err := vgoogle.NewHTTPClient(acct.CredentialsPath)
		if err != nil {
			slog.Warn("skip email account", "account", name, "err", err)
			initErrs = append(initErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		svc, err := gmail.NewService(context.Background(), option.WithHTTPClient(httpClient))
		if err != nil {
			slog.Warn("skip email account", "account", name, "err", err)
			initErrs = append(initErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		clients[name] = svc
		accounts[name] = acct
	}

	reg.Register(&internal.Tool{
		Name:        "email_list",
		Description: "List emails from inbox. Can filter by account, read/unread status, sender, or date range.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account": map[string]any{
					"type":        "string",
					"description": "Email account (personal, keep, enver, passion). Defaults to all.",
				},
				"unread_only": map[string]any{"type": "boolean", "default": false},
				"from": map[string]any{
					"type":        "string",
					"description": "Filter by sender email or name",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Date string (today, yesterday, 2026-03-20)",
				},
				"limit": map[string]any{"type": "integer", "default": 20},
				"query": map[string]any{
					"type":        "string",
					"description": "Gmail search query",
				},
			},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if len(clients) == 0 {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("no email accounts available (init errors: %s)", strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: "no email accounts configured"}, nil
			}

			account, _ := params["account"].(string)
			if err := requireAccount(clients, account); err != nil {
				return &internal.ToolResult{Error: err.Error()}, nil
			}

			unreadOnly, _ := params["unread_only"].(bool)
			from, _ := params["from"].(string)
			since, _ := params["since"].(string)
			query, _ := params["query"].(string)
			limit := intParam(params, "limit", 20)

			q := buildGmailQuery(query, from, since, unreadOnly)

			var results []map[string]any
			var errs []string
			for acctName, svc := range filterClients(clients, account) {
				msgs, err := gmailListMessages(svc, acctName, q, limit-len(results))
				if err != nil {
					slog.Warn("email_list error", "account", acctName, "err", err)
					errs = append(errs, fmt.Sprintf("%s: %s", acctName, err))
					continue
				}
				results = append(results, msgs...)
				if len(results) >= limit {
					break
				}
			}
			if len(results) == 0 && len(errs) > 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("all accounts failed: %s", strings.Join(errs, "; "))}, nil
			}
			if results == nil {
				results = []map[string]any{}
			}
			return partialResult("emails", results, errs), nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "email_read",
		Description: "Read the full content of an email thread.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"thread_id": map[string]any{"type": "string"},
				"account":   map[string]any{"type": "string"},
			},
			"required": []string{"thread_id", "account"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			threadID, _ := params["thread_id"].(string)
			account, _ := params["account"].(string)
			if threadID == "" || account == "" {
				return &internal.ToolResult{Error: "thread_id and account are required"}, nil
			}

			svc, ok := clients[account]
			if !ok {
				return &internal.ToolResult{Error: fmt.Sprintf("account %q not configured", account)}, nil
			}

			thread, err := svc.Users.Threads.Get("me", threadID).Format("full").Do()
			if err != nil {
				return nil, fmt.Errorf("email_read: %w", err)
			}

			messages := make([]map[string]any, 0, len(thread.Messages))
			for _, msg := range thread.Messages {
				messages = append(messages, gmailParseFullMessage(msg, account))
			}

			return &internal.ToolResult{Data: map[string]any{
				"thread_id": threadID,
				"account":   account,
				"messages":  messages,
			}}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "email_send",
		Description: "Send an email or reply to a thread.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account": map[string]any{
					"type":        "string",
					"description": "Which account to send from",
				},
				"to":      map[string]any{"type": "string"},
				"subject": map[string]any{"type": "string"},
				"body":    map[string]any{"type": "string"},
				"reply_to_thread": map[string]any{
					"type":        "string",
					"description": "Thread ID to reply to",
				},
				"cc":  map[string]any{"type": "string"},
				"bcc": map[string]any{"type": "string"},
			},
			"required": []string{"account", "to", "body"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			account, _ := params["account"].(string)
			to, _ := params["to"].(string)
			subject, _ := params["subject"].(string)
			body, _ := params["body"].(string)
			replyTo, _ := params["reply_to_thread"].(string)
			cc, _ := params["cc"].(string)
			bcc, _ := params["bcc"].(string)

			if account == "" || to == "" || body == "" {
				return &internal.ToolResult{Error: "account, to, and body are required"}, nil
			}

			svc, ok := clients[account]
			if !ok {
				return &internal.ToolResult{Error: fmt.Sprintf("account %q not configured", account)}, nil
			}

			fromAddr := ""
			if acct, ok := accounts[account]; ok {
				fromAddr = acct.Address
			}

			var mime strings.Builder
			if fromAddr != "" {
				fmt.Fprintf(&mime, "From: %s\r\n", fromAddr)
			}
			fmt.Fprintf(&mime, "To: %s\r\n", to)
			if cc != "" {
				fmt.Fprintf(&mime, "Cc: %s\r\n", cc)
			}
			if bcc != "" {
				fmt.Fprintf(&mime, "Bcc: %s\r\n", bcc)
			}
			if subject != "" {
				fmt.Fprintf(&mime, "Subject: %s\r\n", subject)
			}

			msg := &gmail.Message{}
			if replyTo != "" {
				msg.ThreadId = replyTo
				thread, err := svc.Users.Threads.Get("me", replyTo).Format("metadata").
					MetadataHeaders("Message-ID", "Subject").Do()
				if err != nil {
					slog.Warn("email_send: failed to fetch thread for reply headers, sending without In-Reply-To",
						"thread_id", replyTo, "err", err)
				} else if len(thread.Messages) > 0 {
					last := thread.Messages[len(thread.Messages)-1]
					for _, h := range last.Payload.Headers {
						if strings.EqualFold(h.Name, "Message-ID") {
							fmt.Fprintf(&mime, "In-Reply-To: %s\r\nReferences: %s\r\n", h.Value, h.Value)
							break
						}
					}
				}
			}

			mime.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			mime.WriteString(body)
			msg.Raw = base64.URLEncoding.EncodeToString([]byte(mime.String()))

			sent, err := svc.Users.Messages.Send("me", msg).Do()
			if err != nil {
				return nil, fmt.Errorf("email_send: %w", err)
			}

			return &internal.ToolResult{Data: map[string]any{
				"id":        sent.Id,
				"thread_id": sent.ThreadId,
				"account":   account,
				"status":    "sent",
			}}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "email_categorize",
		Description: "Categorize an email: imbox (real email), feed (newsletters), paper_trail (receipts/confirmations), noise (unwanted).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message_id": map[string]any{"type": "string"},
				"account":    map[string]any{"type": "string"},
				"category": map[string]any{
					"type": "string",
					"enum": []string{"imbox", "feed", "paper_trail", "noise"},
				},
			},
			"required": []string{"message_id", "account", "category"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			messageID, _ := params["message_id"].(string)
			account, _ := params["account"].(string)
			category, _ := params["category"].(string)

			if messageID == "" || account == "" || category == "" {
				return &internal.ToolResult{Error: "message_id, account, and category are required"}, nil
			}

			svc, ok := clients[account]
			if !ok {
				return &internal.ToolResult{Error: fmt.Sprintf("account %q not configured", account)}, nil
			}

			labelName := "virgil/" + category
			cacheKey := account + ":" + labelName

			labelCacheMu.Lock()
			labelID, ok := labelCache[cacheKey]
			labelCacheMu.Unlock()

			if !ok {
				var err error
				labelID, err = gmailEnsureLabel(svc, labelName)
				if err != nil {
					return nil, fmt.Errorf("email_categorize: %w", err)
				}
				labelCacheMu.Lock()
				labelCache[cacheKey] = labelID
				labelCacheMu.Unlock()
			}

			mod := &gmail.ModifyMessageRequest{
				AddLabelIds: []string{labelID},
			}
			if category != "imbox" {
				mod.RemoveLabelIds = []string{"INBOX"}
			}

			if _, err := svc.Users.Messages.Modify("me", messageID, mod).Do(); err != nil {
				return nil, fmt.Errorf("email_categorize: %w", err)
			}

			return &internal.ToolResult{Data: map[string]any{
				"message_id": messageID,
				"account":    account,
				"category":   category,
				"status":     "categorized",
			}}, nil
		},
	})
}

func buildGmailQuery(query, from, since string, unreadOnly bool) string {
	var parts []string
	if query != "" {
		parts = append(parts, query)
	}
	if from != "" {
		parts = append(parts, "from:"+from)
	}
	if since != "" {
		if t := parseDate(since); !t.IsZero() {
			parts = append(parts, "after:"+t.Format("2006/01/02"))
		}
	}
	if unreadOnly {
		parts = append(parts, "is:unread")
	}
	return strings.Join(parts, " ")
}

func gmailListMessages(svc *gmail.Service, account, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	call := svc.Users.Messages.List("me").MaxResults(int64(limit))
	if query != "" {
		call = call.Q(query)
	}

	resp, err := call.Do()
	if err != nil {
		return nil, err
	}

	results := make([]map[string]any, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		msg, err := svc.Users.Messages.Get("me", m.Id).Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Date").Do()
		if err != nil {
			slog.Warn("skip message", "id", m.Id, "err", err)
			continue
		}
		results = append(results, gmailParseMetaMessage(msg, account))
	}
	return results, nil
}

func gmailBaseMessage(msg *gmail.Message, account string) map[string]any {
	headers := gmailHeaders(msg)
	return map[string]any{
		"id":        msg.Id,
		"thread_id": msg.ThreadId,
		"account":   account,
		"from":      headers["from"],
		"to":        headers["to"],
		"subject":   headers["subject"],
		"date":      headers["date"],
	}
}

func gmailParseMetaMessage(msg *gmail.Message, account string) map[string]any {
	m := gmailBaseMessage(msg, account)
	m["snippet"] = msg.Snippet
	m["labels"] = msg.LabelIds
	m["read"] = !gmailHasLabel(msg, "UNREAD")
	return m
}

func gmailParseFullMessage(msg *gmail.Message, account string) map[string]any {
	m := gmailBaseMessage(msg, account)
	m["body"] = gmailExtractTextBody(msg.Payload)
	return m
}

func gmailHeaders(msg *gmail.Message) map[string]string {
	h := make(map[string]string)
	if msg.Payload != nil {
		for _, hdr := range msg.Payload.Headers {
			h[strings.ToLower(hdr.Name)] = hdr.Value
		}
	}
	return h
}

func gmailHasLabel(msg *gmail.Message, label string) bool {
	for _, l := range msg.LabelIds {
		if l == label {
			return true
		}
	}
	return false
}

func gmailExtractTextBody(payload *gmail.MessagePart) string {
	if payload == nil {
		return ""
	}
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		data, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err != nil {
			slog.Warn("email body base64 decode failed", "mime_type", payload.MimeType, "err", err)
		} else {
			return string(data)
		}
	}
	for _, part := range payload.Parts {
		if body := gmailExtractTextBody(part); body != "" {
			return body
		}
	}
	return ""
}

func gmailEnsureLabel(svc *gmail.Service, name string) (string, error) {
	labels, err := svc.Users.Labels.List("me").Do()
	if err != nil {
		return "", err
	}
	for _, l := range labels.Labels {
		if l.Name == name {
			return l.Id, nil
		}
	}

	label, err := svc.Users.Labels.Create("me", &gmail.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Do()
	if err != nil {
		return "", err
	}
	return label.Id, nil
}
