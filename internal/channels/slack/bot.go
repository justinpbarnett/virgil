package slack

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	slacklib "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/agent"
	"github.com/justinpbarnett/virgil/internal/config"
)

// Bot manages Socket Mode connections for one or more Slack workspaces.
type Bot struct {
	conns []*wsConn
}

type wsConn struct {
	name          string
	api           *slacklib.Client
	socket        *socketmode.Client
	ag            *agent.Agent
	botUserID     string
	authorizedUID string          // only respond to this user ID; empty = respond to all
	watchChanIDs  map[string]bool // resolved channel IDs to watch (besides DMs)
	watchNames    []string        // raw entries from config, resolved at init
}

// NewBot creates Socket Mode connections for every workspace that has app_token_env configured.
// Returns an error if no workspaces are configured with a socket mode token.
func NewBot(cfg *config.Config, ag *agent.Agent) (*Bot, error) {
	var conns []*wsConn

	for name, ws := range cfg.Channels.Slack.Workspaces {
		if ws.AppTokenEnv == "" {
			continue
		}
		appToken := os.Getenv(ws.AppTokenEnv)
		if appToken == "" {
			slog.Warn("slack app token env var set but empty", "workspace", name, "env", ws.AppTokenEnv)
			continue
		}
		botToken := os.Getenv(ws.TokenEnv)
		if botToken == "" {
			slog.Warn("slack socket mode requires a bot token (token_env)", "workspace", name)
			continue
		}

		api := slacklib.New(botToken, slacklib.OptionAppLevelToken(appToken))
		socket := socketmode.New(api)

		conns = append(conns, &wsConn{
			name:          name,
			api:           api,
			socket:        socket,
			ag:            ag,
			authorizedUID: ws.UserID,
			watchChanIDs:  make(map[string]bool),
			watchNames:    ws.WatchChannels,
		})
	}

	if len(conns) == 0 {
		return nil, fmt.Errorf("no slack workspaces with socket mode (app_token_env) configured")
	}
	return &Bot{conns: conns}, nil
}

// Start connects all workspaces and blocks until ctx is cancelled or all connections exit.
func (b *Bot) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for _, conn := range b.conns {
		wg.Add(1)
		go func(c *wsConn) {
			defer wg.Done()
			c.run(ctx)
		}(conn)
	}
	wg.Wait()
}

func (c *wsConn) run(ctx context.Context) {
	if err := c.init(ctx); err != nil {
		slog.Error("slack workspace init failed", "workspace", c.name, "err", err)
		return
	}
	go c.processEvents(ctx)
	if err := c.socket.RunContext(ctx); err != nil && ctx.Err() == nil {
		slog.Error("slack socket mode disconnected", "workspace", c.name, "err", err)
	}
}

func (c *wsConn) init(ctx context.Context) error {
	resp, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("auth test: %w", err)
	}
	c.botUserID = resp.UserID
	slog.Info("slack socket mode ready", "workspace", c.name, "bot_user", resp.User,
		"authorized_user", c.authorizedUID)

	if len(c.watchNames) > 0 {
		c.resolveWatchChannels(ctx)
	}
	return nil
}

func (c *wsConn) resolveWatchChannels(ctx context.Context) {
	channels, _, err := c.api.GetConversationsContext(ctx, &slacklib.GetConversationsParameters{
		Types: []string{"public_channel", "private_channel"},
		Limit: 200,
	})
	if err != nil {
		slog.Warn("slack: could not resolve watch channel names", "workspace", c.name, "err", err)
		return
	}
	nameToID := make(map[string]string, len(channels))
	for _, ch := range channels {
		nameToID[ch.Name] = ch.ID
	}
	for _, entry := range c.watchNames {
		if isChannelID(entry) {
			c.watchChanIDs[entry] = true
			continue
		}
		if id, ok := nameToID[entry]; ok {
			c.watchChanIDs[id] = true
		} else {
			slog.Warn("slack: watch channel not found", "workspace", c.name, "channel", entry)
		}
	}
}

func isChannelID(s string) bool {
	return len(s) >= 9 && (s[0] == 'C' || s[0] == 'D' || s[0] == 'G')
}

func (c *wsConn) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-c.socket.Events:
			if !ok {
				return
			}
			slog.Debug("slack socket event", "workspace", c.name, "type", evt.Type)
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				apiEvt, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					slog.Warn("slack: unexpected EventsAPI data type", "workspace", c.name, "data", evt.Data)
					if evt.Request != nil {
						c.socket.Ack(*evt.Request)
					}
					continue
				}
				c.socket.Ack(*evt.Request)
				slog.Info("slack api event", "workspace", c.name, "type", apiEvt.InnerEvent.Type)
				go c.handleAPIEvent(ctx, apiEvt)
			default:
				if evt.Request != nil {
					c.socket.Ack(*evt.Request)
				}
			}
		}
	}
}

func (c *wsConn) handleAPIEvent(ctx context.Context, evt slackevents.EventsAPIEvent) {
	if evt.InnerEvent.Type != "message" {
		return
	}
	msg, ok := evt.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}
	c.handleMessage(ctx, msg)
}

func (c *wsConn) handleMessage(ctx context.Context, msg *slackevents.MessageEvent) {
	slog.Info("slack message received", "workspace", c.name, "channel", msg.Channel,
		"user", msg.User, "subtype", msg.SubType, "bot_id", msg.BotID)

	// Skip bot messages and subtypes (edits, deletes, join/leave notices, etc.)
	if msg.BotID != "" || msg.SubType != "" {
		slog.Debug("slack: skipping bot message or subtype", "workspace", c.name,
			"subtype", msg.SubType, "bot_id", msg.BotID)
		return
	}

	isDM := strings.HasPrefix(msg.Channel, "D")
	if !isDM && !c.watchChanIDs[msg.Channel] {
		slog.Debug("slack: ignoring message not in DM or watch channel",
			"workspace", c.name, "channel", msg.Channel)
		return
	}

	// Enforce authorization -- silently ignore messages from other users.
	if c.authorizedUID != "" && msg.User != c.authorizedUID {
		slog.Warn("slack: ignoring message from unauthorized user",
			"workspace", c.name, "user", msg.User, "authorized", c.authorizedUID)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if c.botUserID != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, "<@"+c.botUserID+">", ""))
	}
	if text == "" {
		return
	}

	sig := internal.NewSignal("slack", text)
	sig.Account = c.name

	resp, err := c.ag.Run(ctx, sig)
	if err != nil {
		slog.Error("slack agent error", "workspace", c.name, "err", err)
		c.post(msg.Channel, msg.ThreadTimeStamp, "Something went wrong. Check the logs.")
		return
	}
	if resp == "" {
		resp = "Done."
	}
	c.post(msg.Channel, msg.ThreadTimeStamp, resp)
}

func (c *wsConn) post(channel, threadTS, text string) {
	opts := []slacklib.MsgOption{slacklib.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slacklib.MsgOptionTS(threadTS))
	}
	if _, _, err := c.api.PostMessage(channel, opts...); err != nil {
		slog.Error("slack post message failed", "workspace", c.name, "err", err)
	}
}
