package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

var defaultJiraKeyRe = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)

// SlackConfig holds credentials for connecting to Slack.
type SlackConfig struct {
	Token    string   `yaml:"token"`
	UserID   string   `yaml:"user_id"`
	Channels []string `yaml:"channels"`
}

// SlackMessage represents a single Slack message.
type SlackMessage struct {
	User     string `json:"user"`
	Text     string `json:"text"`
	Timestamp string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

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

// slackAPIError represents a logical error from the Slack API (ok: false).
type slackAPIError struct {
	code string
}

func (e *slackAPIError) Error() string { return e.code }

// SlackClient encapsulates auth, channel list, and HTTP transport for Slack API calls.
type SlackClient struct {
	Token      string
	UserID     string
	Channels   []string
	HTTPClient *http.Client
	BaseURL    string
	userCache  map[string]string
}

// NewClient creates a SlackClient from config.
func NewClient(cfg SlackConfig) *SlackClient {
	return &SlackClient{
		Token:      cfg.Token,
		UserID:     cfg.UserID,
		Channels:   cfg.Channels,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://slack.com/api",
		userCache:  make(map[string]string),
	}
}

func (c *SlackClient) get(ctx context.Context, endpoint string, params url.Values) (map[string]any, error) {
	u := c.BaseURL + "/" + endpoint + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Slack API: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Slack API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("cannot reach Slack API: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("cannot reach Slack API: invalid JSON response")
	}

	ok, _ := result["ok"].(bool)
	if !ok {
		code, _ := result["error"].(string)
		if code == "" {
			code = "unknown_error"
		}
		return nil, &slackAPIError{code: code}
	}

	return result, nil
}

// GetChannelHistory fetches recent messages from a channel.
func (c *SlackClient) GetChannelHistory(ctx context.Context, channelID, oldest string, limit int) ([]SlackMessage, error) {
	params := url.Values{
		"channel": {channelID},
		"oldest":  {oldest},
		"limit":   {strconv.Itoa(limit)},
	}
	result, err := c.get(ctx, "conversations.history", params)
	if err != nil {
		return nil, err
	}

	return parseMessages(result, "messages")
}

// GetThreadReplies fetches all replies in a thread.
func (c *SlackClient) GetThreadReplies(ctx context.Context, channelID, threadTS string) ([]SlackMessage, error) {
	params := url.Values{
		"channel": {channelID},
		"ts":      {threadTS},
	}
	result, err := c.get(ctx, "conversations.replies", params)
	if err != nil {
		return nil, err
	}

	return parseMessages(result, "messages")
}

// GetUserInfo resolves a Slack user ID to a display name. Results are cached
// for the lifetime of the client to avoid redundant API calls.
func (c *SlackClient) GetUserInfo(ctx context.Context, userID string) (string, error) {
	if name, ok := c.userCache[userID]; ok {
		return name, nil
	}

	params := url.Values{"user": {userID}}
	result, err := c.get(ctx, "users.info", params)
	if err != nil {
		return userID, err
	}

	name := userID
	if user, ok := result["user"].(map[string]any); ok {
		if profile, ok := user["profile"].(map[string]any); ok {
			if dn, ok := profile["display_name"].(string); ok && dn != "" {
				name = dn
			} else if rn, ok := profile["real_name"].(string); ok && rn != "" {
				name = rn
			}
		}
		// fallback to real_name at top level
		if name == userID {
			if rn, ok := user["real_name"].(string); ok && rn != "" {
				name = rn
			}
		}
	}

	c.userCache[userID] = name
	return name, nil
}

func parseMessages(result map[string]any, key string) ([]SlackMessage, error) {
	raw, ok := result[key].([]any)
	if !ok {
		return nil, nil
	}

	msgs := make([]SlackMessage, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		msg := SlackMessage{
			User:     strVal(m, "user"),
			Text:     strVal(m, "text"),
			Timestamp: strVal(m, "ts"),
			ThreadTS: strVal(m, "thread_ts"),
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// ExtractJiraKeys returns a deduplicated list of JIRA issue keys found in texts.
// When project is non-empty, only keys matching that project are returned (e.g.
// "PTP" matches "PTP-123"). When empty, any JIRA-style key is matched.
func ExtractJiraKeys(texts []string, project string) []string {
	re := defaultJiraKeyRe
	if project != "" {
		re = regexp.MustCompile(regexp.QuoteMeta(project) + `-\d+`)
	}
	seen := make(map[string]struct{})
	var keys []string
	for _, t := range texts {
		for _, k := range re.FindAllString(t, -1) {
			if _, exists := seen[k]; !exists {
				seen[k] = struct{}{}
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// ParseSinceDuration parses a duration string like "7d" or "24h" and returns
// a Unix timestamp string (seconds since epoch) representing that far in the past.
// Defaults to 7 days if the input cannot be parsed.
func ParseSinceDuration(since string) string {
	since = strings.TrimSpace(since)
	if len(since) < 2 {
		return defaultSince()
	}

	unit := since[len(since)-1]
	numStr := since[:len(since)-1]
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil || n <= 0 {
		return defaultSince()
	}

	var dur time.Duration
	switch unit {
	case 'd':
		dur = time.Duration(n * float64(24*time.Hour))
	case 'h':
		dur = time.Duration(n * float64(time.Hour))
	default:
		return defaultSince()
	}

	return strconv.FormatInt(time.Now().Add(-dur).Unix(), 10)
}

func defaultSince() string {
	return strconv.FormatInt(time.Now().Add(-7*24*time.Hour).Unix(), 10)
}

// classifySlackError maps a slackAPIError to an EnvelopeError.
func classifySlackError(err error) *envelope.EnvelopeError {
	var se *slackAPIError
	if !errors.As(err, &se) {
		return &envelope.EnvelopeError{
			Message:   fmt.Sprintf("cannot reach Slack API: %v", err),
			Severity:  envelope.SeverityError,
			Retryable: true,
		}
	}
	switch se.code {
	case "invalid_auth", "not_authed":
		return envelope.FatalError("Slack authentication failed — check slack.yaml token")
	case "channel_not_found":
		return envelope.FatalError(fmt.Sprintf("channel not found: %s", se.code))
	case "ratelimited":
		return &envelope.EnvelopeError{
			Message:   "rate limited by Slack — retry after a few seconds",
			Severity:  envelope.SeverityError,
			Retryable: true,
		}
	default:
		return &envelope.EnvelopeError{
			Message:   fmt.Sprintf("Slack API error: %s", se.code),
			Severity:  envelope.SeverityError,
			Retryable: true,
		}
	}
}

// NewHandler returns a pipe.Handler for the slack pipe.
func NewHandler(client *SlackClient, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		if action == "" {
			action = "mentions"
		}

		out := envelope.New("slack", action)
		out.Args = flags

		ctx := context.Background()

		switch action {
		case "mentions":
			out = handleMentions(ctx, client, logger, flags, out)
		case "thread":
			out = handleThread(ctx, client, logger, flags, out)
		default:
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s (expected: mentions, thread)", action))
		}

		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// ScanMentions scans configured channels for mentions of the client's user
// since the given oldest timestamp. jiraProject filters extracted JIRA keys to
// a single project (e.g. "PTP"); pass empty to match any JIRA-style key.
// Returns all found mentions and any non-fatal errors encountered during scanning.
func (c *SlackClient) ScanMentions(ctx context.Context, oldest string, jiraProject string) ([]SlackMention, []error) {
	type key struct{ channel, threadTS string }
	seen := make(map[key]bool)
	var mentions []SlackMention
	var errs []error

	for _, channelID := range c.Channels {
		msgs, err := c.GetChannelHistory(ctx, channelID, oldest, 200)
		if err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", channelID, err))
			continue
		}

		mentionTag := "<@" + c.UserID + ">"
		for _, msg := range msgs {
			if !strings.Contains(msg.Text, mentionTag) {
				continue
			}

			threadTS := msg.ThreadTS
			if threadTS == "" {
				threadTS = msg.Timestamp
			}

			k := key{channelID, threadTS}
			if seen[k] {
				continue
			}
			seen[k] = true

			thread, err := c.GetThreadReplies(ctx, channelID, threadTS)
			if err != nil {
				errs = append(errs, fmt.Errorf("thread %s/%s: %w", channelID, threadTS, err))
				continue
			}

			threadTexts := make([]string, 0, len(thread))
			allTexts := make([]string, 0, len(thread))
			for _, tm := range thread {
				name, _ := c.GetUserInfo(ctx, tm.User)
				threadTexts = append(threadTexts, name+": "+tm.Text)
				allTexts = append(allTexts, tm.Text)
			}

			jiraKeys := ExtractJiraKeys(allTexts, jiraProject)
			author, _ := c.GetUserInfo(ctx, msg.User)

			mentions = append(mentions, SlackMention{
				Channel:   channelID,
				ThreadTS:  threadTS,
				Author:    author,
				Text:      msg.Text,
				Thread:    threadTexts,
				JiraKeys:  jiraKeys,
				Timestamp: msg.Timestamp,
			})
		}
	}

	return mentions, errs
}

func handleMentions(ctx context.Context, client *SlackClient, logger *slog.Logger, flags map[string]string, out envelope.Envelope) envelope.Envelope {
	since := flags["since"]
	if since == "" {
		since = "7d"
	}
	oldest := ParseSinceDuration(since)

	logger.Debug("scanning for mentions", "channels", len(client.Channels), "oldest", oldest)

	mentions, errs := client.ScanMentions(ctx, oldest, "")
	if len(errs) > 0 {
		out.Error = classifySlackError(errs[0])
		return out
	}

	sort.Slice(mentions, func(i, j int) bool {
		return mentions[i].Timestamp > mentions[j].Timestamp
	})

	items := make([]map[string]any, 0, len(mentions))
	for i := range mentions {
		items = append(items, mentionToMap(&mentions[i]))
	}

	out.Content = items
	out.ContentType = envelope.ContentList
	return out
}

func handleThread(ctx context.Context, client *SlackClient, logger *slog.Logger, flags map[string]string, out envelope.Envelope) envelope.Envelope {
	channelID := flags["channel"]
	if channelID == "" {
		out.Error = envelope.FatalError("channel is required for thread action")
		return out
	}

	threadTS := flags["thread_ts"]
	if threadTS == "" {
		out.Error = envelope.FatalError("thread_ts is required for thread action")
		return out
	}

	logger.Debug("fetching thread", "channel", channelID, "thread_ts", threadTS)

	msgs, err := client.GetThreadReplies(ctx, channelID, threadTS)
	if err != nil {
		out.Error = classifySlackError(err)
		return out
	}

	threadLines := make([]string, 0, len(msgs))
	allTexts := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		name, _ := client.GetUserInfo(ctx, msg.User)
		threadLines = append(threadLines, name+": "+msg.Text)
		allTexts = append(allTexts, msg.Text)
	}

	jiraKeys := ExtractJiraKeys(allTexts, "")

	author := ""
	if len(msgs) > 0 {
		author, _ = client.GetUserInfo(ctx, msgs[0].User)
	}

	mention := &SlackMention{
		Channel:   channelID,
		ThreadTS:  threadTS,
		Author:    author,
		Text:      "",
		Thread:    threadLines,
		JiraKeys:  jiraKeys,
		Timestamp: threadTS,
	}

	out.Content = mentionToMap(mention)
	out.ContentType = envelope.ContentStructured
	return out
}

func mentionToMap(m *SlackMention) map[string]any {
	result := map[string]any{
		"channel":   m.Channel,
		"thread_ts": m.ThreadTS,
		"author":    m.Author,
		"text":      m.Text,
		"thread":    m.Thread,
		"timestamp": m.Timestamp,
	}
	if len(m.JiraKeys) > 0 {
		result["jira_keys"] = m.JiraKeys
	}
	return result
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
