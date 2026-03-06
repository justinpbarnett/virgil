package slack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

// mockSlackServer creates an httptest.Server with the given path → handler map.
func mockSlackServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	return httptest.NewServer(mux)
}

// slackOKResponse writes a successful Slack API JSON response.
func slackOKResponse(w http.ResponseWriter, payload map[string]any) {
	payload["ok"] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// slackErrResponse writes a failed Slack API JSON response.
func slackErrResponse(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
}

// buildClient creates a SlackClient pointed at the mock server.
func buildClient(srv *httptest.Server, userID string, channels []string) *SlackClient {
	c := NewClient(SlackConfig{
		Token:    "xoxp-test",
		UserID:   userID,
		Channels: channels,
	})
	c.BaseURL = srv.URL
	return c
}

// msgList builds a Slack messages array for use in mock responses.
func msgList(msgs ...map[string]any) []any {
	out := make([]any, len(msgs))
	for i, m := range msgs {
		out[i] = m
	}
	return out
}

// --- parseSinceDuration ---

func TestParseSinceDuration_Days(t *testing.T) {
	ts := ParseSinceDuration("7d")
	n, err := parseInt64(ts)
	if err != nil {
		t.Fatalf("expected numeric timestamp, got %q: %v", ts, err)
	}
	// Should be approximately 7 days ago.
	expected := time.Now().Add(-7 * 24 * time.Hour).Unix()
	if abs(n-expected) > 5 {
		t.Errorf("ParseSinceDuration(7d): got %d, expected ~%d", n, expected)
	}
}

func TestParseSinceDuration_Hours(t *testing.T) {
	ts := ParseSinceDuration("24h")
	n, err := parseInt64(ts)
	if err != nil {
		t.Fatalf("expected numeric timestamp, got %q: %v", ts, err)
	}
	expected := time.Now().Add(-24 * time.Hour).Unix()
	if abs(n-expected) > 5 {
		t.Errorf("ParseSinceDuration(24h): got %d, expected ~%d", n, expected)
	}
}

func TestParseSinceDuration_Fallback(t *testing.T) {
	ts := ParseSinceDuration("invalid")
	n, err := parseInt64(ts)
	if err != nil {
		t.Fatalf("expected numeric timestamp, got %q: %v", ts, err)
	}
	expected := time.Now().Add(-7 * 24 * time.Hour).Unix()
	if abs(n-expected) > 5 {
		t.Errorf("ParseSinceDuration(invalid) fallback: got %d, expected ~%d", n, expected)
	}
}

// --- extractJiraKeys ---

func TestExtractJiraKeys_Found(t *testing.T) {
	keys := ExtractJiraKeys([]string{"see PTP-123 and PTP-456", "also PTP-123"}, "")
	if len(keys) != 2 {
		t.Fatalf("expected 2 unique keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "PTP-123" || keys[1] != "PTP-456" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestExtractJiraKeys_None(t *testing.T) {
	keys := ExtractJiraKeys([]string{"no tickets here", "nothing"}, "")
	if len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
}

// --- mentions action ---

func TestMentions_ChannelWithMention(t *testing.T) {
	const (
		userID    = "U123"
		channelID = "C001"
		parentTS  = "1700000000.000001"
	)

	callCount := 0
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "U999", "text": "hey <@U123> check this", "ts": parentTS},
				),
			})
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "U999", "text": "hey <@U123> check this", "ts": parentTS},
					map[string]any{"user": "U123", "text": "thanks!", "ts": "1700000001.000001"},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"user": map[string]any{
					"profile": map[string]any{"display_name": "Alice"},
				},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, userID, []string{channelID})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{
		"action": "mentions",
		"since":  "7d",
	})

	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if out.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %q", out.ContentType)
	}
	items, ok := out.Content.([]map[string]any)
	if !ok {
		t.Fatalf("content should be []map[string]any, got %T", out.Content)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(items))
	}
	m := items[0]
	if m["channel"] != channelID {
		t.Errorf("channel: got %v, want %v", m["channel"], channelID)
	}
	if m["thread_ts"] != parentTS {
		t.Errorf("thread_ts: got %v, want %v", m["thread_ts"], parentTS)
	}
	if m["author"] != "Alice" {
		t.Errorf("author: got %v, want Alice", m["author"])
	}
}

func TestMentions_NoMentions(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "U999", "text": "just a regular message", "ts": "1700000000.000001"},
				),
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	items, ok := out.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", out.Content)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 mentions, got %d", len(items))
	}
}

func TestMentions_MultipleChannels(t *testing.T) {
	const userID = "U123"

	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			ch := r.URL.Query().Get("channel")
			var msgs []any
			switch ch {
			case "C001":
				msgs = msgList(
					map[string]any{"user": "UA", "text": "hi <@U123>", "ts": "1700000001.000001"},
				)
			case "C002":
				msgs = msgList(
					map[string]any{"user": "UB", "text": "<@U123> ping", "ts": "1700000002.000001"},
				)
			}
			slackOKResponse(w, map[string]any{"messages": msgs})
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			ts := r.URL.Query().Get("ts")
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "reply", "ts": ts},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": "Someone"}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, userID, []string{"C001", "C002"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	items := out.Content.([]map[string]any)
	if len(items) != 2 {
		t.Errorf("expected 2 mentions across 2 channels, got %d", len(items))
	}
}

func TestMentions_JiraKeyExtraction(t *testing.T) {
	const parentTS = "1700000000.000001"

	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "hey <@U123> re PTP-111", "ts": parentTS},
				),
			})
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "hey <@U123> re PTP-111", "ts": parentTS},
					map[string]any{"user": "UB", "text": "also PTP-222 and PTP-111", "ts": "1700000001.000001"},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": "Bob"}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	items := out.Content.([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(items))
	}
	keys, _ := items[0]["jira_keys"].([]string)
	if len(keys) != 2 {
		t.Errorf("expected 2 JIRA keys (PTP-111, PTP-222), got %v", keys)
	}
}

func TestMentions_Deduplication(t *testing.T) {
	// Two messages in the same thread (same thread_ts) should produce one mention.
	const (
		threadTS = "1700000000.000001"
		replyTS  = "1700000001.000001"
	)

	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					// parent message with mention — no thread_ts yet (it IS the parent)
					map[string]any{"user": "UA", "text": "hi <@U123>", "ts": threadTS},
					// reply also mentioning the user, with thread_ts pointing back to parent
					map[string]any{"user": "UB", "text": "<@U123> again", "ts": replyTS, "thread_ts": threadTS},
				),
			})
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "hi <@U123>", "ts": threadTS},
					map[string]any{"user": "UB", "text": "<@U123> again", "ts": replyTS},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": "Carol"}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	items := out.Content.([]map[string]any)
	if len(items) != 1 {
		t.Errorf("expected 1 deduplicated mention, got %d", len(items))
	}
}

// --- thread action ---

func TestThread_FetchesFullThread(t *testing.T) {
	const (
		channelID = "C001"
		threadTS  = "1700000000.000001"
	)

	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("ts") != threadTS {
				http.Error(w, "wrong ts", 400)
				return
			}
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "first message PTP-100", "ts": threadTS},
					map[string]any{"user": "UB", "text": "reply here", "ts": "1700000001.000001"},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			u := r.URL.Query().Get("user")
			name := "UserA"
			if u == "UB" {
				name = "UserB"
			}
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": name}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{channelID})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "thread"), map[string]string{
		"action":    "thread",
		"channel":   channelID,
		"thread_ts": threadTS,
	})

	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if out.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %q", out.ContentType)
	}
	m, ok := out.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any content, got %T", out.Content)
	}
	thread, _ := m["thread"].([]string)
	if len(thread) != 2 {
		t.Errorf("expected 2 thread lines, got %d", len(thread))
	}
	keys, _ := m["jira_keys"].([]string)
	if len(keys) != 1 || keys[0] != "PTP-100" {
		t.Errorf("expected [PTP-100], got %v", keys)
	}
}

func TestThread_MissingChannel(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "thread"), map[string]string{
		"action":    "thread",
		"thread_ts": "1700000000.000001",
	})

	if out.Error == nil {
		t.Fatal("expected error for missing channel")
	}
	if out.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %q", out.Error.Severity)
	}
	if !strings.Contains(out.Error.Message, "channel") {
		t.Errorf("expected message about channel, got %q", out.Error.Message)
	}
}

func TestThread_MissingThreadTS(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "thread"), map[string]string{
		"action":  "thread",
		"channel": "C001",
	})

	if out.Error == nil {
		t.Fatal("expected error for missing thread_ts")
	}
	if out.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %q", out.Error.Severity)
	}
	if !strings.Contains(out.Error.Message, "thread_ts") {
		t.Errorf("expected message about thread_ts, got %q", out.Error.Message)
	}
}

// --- auth errors ---

func TestInvalidToken_FatalError(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackErrResponse(w, "invalid_auth")
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Error == nil {
		t.Fatal("expected error")
	}
	if out.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal, got %q", out.Error.Severity)
	}
	if !strings.Contains(strings.ToLower(out.Error.Message), "authentication") {
		t.Errorf("expected auth error message, got %q", out.Error.Message)
	}
}

func TestNotAuthed_FatalError(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackErrResponse(w, "not_authed")
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Error == nil {
		t.Fatal("expected error")
	}
	if out.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal, got %q", out.Error.Severity)
	}
}

func TestRateLimited_RetryableError(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackErrResponse(w, "ratelimited")
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Error == nil {
		t.Fatal("expected error")
	}
	if !out.Error.Retryable {
		t.Error("expected retryable=true for rate limited error")
	}
}

// --- user cache ---

func TestUserCache_SingleLookup(t *testing.T) {
	lookupCount := 0
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					// Two messages from the same user, both mentioning the target.
					map[string]any{"user": "UA", "text": "hi <@U123>", "ts": "1700000001.000001"},
					map[string]any{"user": "UA", "text": "<@U123> again", "ts": "1700000002.000001"},
				),
			})
		},
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "reply", "ts": "1700000001.000001"},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			lookupCount++
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": "Dave"}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})
	if out.Error != nil {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	// UA is used for both thread fetches but should only be looked up once.
	if lookupCount > 2 {
		t.Errorf("expected at most 2 user lookups (UA + U123), got %d", lookupCount)
	}
}

// --- envelope compliance ---

func TestEnvelopeCompliance_Mentions(t *testing.T) {
	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.history": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{"messages": msgList()})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{"C001"})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "mentions"), map[string]string{"action": "mentions"})

	if out.Pipe != "slack" {
		t.Errorf("pipe: got %q, want slack", out.Pipe)
	}
	if out.Action != "mentions" {
		t.Errorf("action: got %q, want mentions", out.Action)
	}
	if out.ContentType != envelope.ContentList {
		t.Errorf("content_type: got %q, want list", out.ContentType)
	}
	if out.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
	if out.Duration <= 0 {
		t.Error("duration should be positive")
	}
}

func TestEnvelopeCompliance_Thread(t *testing.T) {
	const (
		channelID = "C001"
		threadTS  = "1700000000.000001"
	)

	srv := mockSlackServer(t, map[string]http.HandlerFunc{
		"/conversations.replies": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"messages": msgList(
					map[string]any{"user": "UA", "text": "hello", "ts": threadTS},
				),
			})
		},
		"/users.info": func(w http.ResponseWriter, r *http.Request) {
			slackOKResponse(w, map[string]any{
				"user": map[string]any{"profile": map[string]any{"display_name": "Eve"}},
			})
		},
	})
	defer srv.Close()

	client := buildClient(srv, "U123", []string{channelID})
	handler := NewHandler(client, nil)

	out := handler(envelope.New("slack", "thread"), map[string]string{
		"action":    "thread",
		"channel":   channelID,
		"thread_ts": threadTS,
	})

	if out.Pipe != "slack" {
		t.Errorf("pipe: got %q, want slack", out.Pipe)
	}
	if out.Action != "thread" {
		t.Errorf("action: got %q, want thread", out.Action)
	}
	if out.ContentType != envelope.ContentStructured {
		t.Errorf("content_type: got %q, want structured", out.ContentType)
	}
	if out.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
	if out.Duration <= 0 {
		t.Error("duration should be positive")
	}
}

// --- helpers ---

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

