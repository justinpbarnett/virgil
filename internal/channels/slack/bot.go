package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	slacklib "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/agent"
	"github.com/justinpbarnett/virgil/internal/config"
)

const agentTimeout = 5 * time.Minute

type workspace struct {
	name          string
	api           *slacklib.Client
	botUserID     string
	authorizedUID string
	signingSecret string
	watchChanIDs  map[string]bool
	watchNames    []string
}

// RegisterHandlers adds a /slack/events HTTP handler to mux for all configured workspaces.
func RegisterHandlers(mux *http.ServeMux, cfg *config.Config, ag *agent.Agent) {
	var wss []*workspace
	for name, ws := range cfg.Channels.Slack.Workspaces {
		if ws.SigningSecretEnv == "" {
			continue
		}
		secret := os.Getenv(ws.SigningSecretEnv)
		if secret == "" {
			slog.Warn("slack signing secret env var set but empty", "workspace", name, "env", ws.SigningSecretEnv)
			continue
		}
		botToken := os.Getenv(ws.TokenEnv)
		if botToken == "" {
			slog.Warn("slack webhook: no bot token", "workspace", name)
			continue
		}

		w := &workspace{
			name:          name,
			api:           slacklib.New(botToken),
			authorizedUID: ws.UserID,
			signingSecret: secret,
			watchChanIDs:  make(map[string]bool),
			watchNames:    ws.WatchChannels,
		}

		resp, err := w.api.AuthTest()
		if err != nil {
			slog.Error("slack webhook: auth test failed, skipping workspace", "workspace", name, "err", err)
			continue
		}
		w.botUserID = resp.UserID
		slog.Info("slack webhook ready", "workspace", name, "bot_user", resp.User, "authorized_user", ws.UserID)

		if len(ws.WatchChannels) > 0 {
			w.resolveWatchChannels()
		}

		wss = append(wss, w)
	}

	if len(wss) == 0 {
		if len(cfg.Channels.Slack.Workspaces) > 0 {
			slog.Warn("slack: no workspaces passed validation, webhook not registered")
		}
		return
	}

	mux.HandleFunc("/slack/events", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Skip Slack retries -- we always respond 200 on first delivery.
		if r.Header.Get("X-Slack-Retry-Num") != "" {
			rw.WriteHeader(http.StatusOK)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			slog.Warn("slack: failed to read request body", "err", err, "remote_addr", r.RemoteAddr)
			http.Error(rw, "read body", http.StatusBadRequest)
			return
		}

		var matched *workspace
		for _, w := range wss {
			if w.verifySignature(r.Header, body) {
				matched = w
				break
			}
		}
		if matched == nil {
			slog.Warn("slack: no workspace matched request signature",
				"remote_addr", r.RemoteAddr,
				"workspaces_checked", len(wss),
				"has_signature", r.Header.Get("X-Slack-Signature") != "",
				"has_timestamp", r.Header.Get("X-Slack-Request-Timestamp") != "",
			)
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}

		matched.dispatch(rw, body, ag)
	})

	slog.Info("slack webhook registered", "path", "/slack/events", "workspaces", len(wss))
}

func (w *workspace) verifySignature(header http.Header, body []byte) bool {
	ts := header.Get("X-Slack-Request-Timestamp")
	sig := header.Get("X-Slack-Signature")
	if ts == "" || sig == "" {
		return false
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	elapsed := time.Since(time.Unix(t, 0))
	if elapsed > 5*time.Minute || elapsed < -30*time.Second {
		return false
	}
	mac := hmac.New(sha256.New, []byte(w.signingSecret))
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (w *workspace) dispatch(rw http.ResponseWriter, body []byte, ag *agent.Agent) {
	var envelope struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(rw, "parse body", http.StatusBadRequest)
		return
	}

	switch envelope.Type {
	case "url_verification":
		rw.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(rw).Encode(map[string]string{"challenge": envelope.Challenge}); err != nil {
			slog.Error("slack: failed to write url_verification challenge", "workspace", w.name, "err", err)
		}

	case "event_callback":
		rw.WriteHeader(http.StatusOK)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("slack: panic in event handler", "workspace", w.name, "panic", r)
				}
			}()
			evt, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
			if err != nil {
				slog.Error("slack: parse event", "workspace", w.name, "err", err)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
			defer cancel()
			w.handleAPIEvent(ctx, evt, ag)
		}()

	default:
		rw.WriteHeader(http.StatusOK)
	}
}

func (w *workspace) handleAPIEvent(ctx context.Context, evt slackevents.EventsAPIEvent, ag *agent.Agent) {
	if evt.InnerEvent.Type != "message" {
		return
	}
	msg, ok := evt.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}
	w.handleMessage(ctx, msg, ag)
}

func (w *workspace) handleMessage(ctx context.Context, msg *slackevents.MessageEvent, ag *agent.Agent) {
	slog.Info("slack message received", "workspace", w.name, "channel", msg.Channel,
		"user", msg.User, "subtype", msg.SubType, "bot_id", msg.BotID)

	if msg.BotID != "" || msg.SubType != "" {
		return
	}

	isDM := strings.HasPrefix(msg.Channel, "D")
	if !isDM && !w.watchChanIDs[msg.Channel] {
		slog.Debug("slack: ignoring message not in DM or watch channel",
			"workspace", w.name, "channel", msg.Channel, "watch_channels_loaded", len(w.watchChanIDs))
		return
	}

	if w.authorizedUID != "" && msg.User != w.authorizedUID {
		slog.Warn("slack: ignoring message from unauthorized user",
			"workspace", w.name, "user", msg.User, "authorized", w.authorizedUID)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if w.botUserID != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, "<@"+w.botUserID+">", ""))
	}
	if text == "" {
		return
	}

	sig := internal.NewSignal("slack", text)
	sig.Account = w.name

	resp, err := ag.Run(ctx, sig)
	if err != nil {
		slog.Error("slack agent error", "workspace", w.name, "err", err)
		w.post(msg.Channel, msg.ThreadTimeStamp, "Something went wrong. Check the logs.")
		return
	}
	if resp == "" {
		resp = "Done."
	}
	w.post(msg.Channel, msg.ThreadTimeStamp, resp)
}

func (w *workspace) post(channel, threadTS, text string) {
	opts := []slacklib.MsgOption{slacklib.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slacklib.MsgOptionTS(threadTS))
	}
	if _, _, err := w.api.PostMessage(channel, opts...); err != nil {
		slog.Error("slack post message failed", "workspace", w.name, "err", err)
	}
}

func (w *workspace) resolveWatchChannels() {
	channels, _, err := w.api.GetConversations(&slacklib.GetConversationsParameters{
		Types: []string{"public_channel", "private_channel"},
		Limit: 200,
	})
	if err != nil {
		slog.Warn("slack: could not resolve watch channel names", "workspace", w.name, "err", err)
		return
	}
	nameToID := make(map[string]string, len(channels))
	for _, ch := range channels {
		nameToID[ch.Name] = ch.ID
	}
	for _, entry := range w.watchNames {
		if isChannelID(entry) {
			w.watchChanIDs[entry] = true
			continue
		}
		if id, ok := nameToID[entry]; ok {
			w.watchChanIDs[id] = true
		} else {
			slog.Warn("slack: watch channel not found", "workspace", w.name, "channel", entry)
		}
	}
}

func isChannelID(s string) bool {
	return len(s) >= 9 && (s[0] == 'C' || s[0] == 'D' || s[0] == 'G')
}
