package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.in/telebot.v4"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/agent"
	"github.com/justinpbarnett/virgil/internal/config"
)

// Bot handles Telegram polling and message routing.
type Bot struct {
	bot    *telebot.Bot
	agent  *agent.Agent
	chatID int64
}

// NewBot creates a Telegram bot from config. Returns an error if the token is missing or agent is nil.
func NewBot(cfg *config.Config, ag *agent.Agent) (*Bot, error) {
	if ag == nil {
		return nil, fmt.Errorf("agent is required")
	}
	token := os.Getenv(cfg.Channels.Telegram.BotTokenEnv)
	if token == "" {
		return nil, fmt.Errorf("telegram bot token not set (env: %s)", cfg.Channels.Telegram.BotTokenEnv)
	}

	var chatID int64
	if env := cfg.Channels.Telegram.ChatIDEnv; env != "" {
		raw := os.Getenv(env)
		if _, err := fmt.Sscanf(raw, "%d", &chatID); err != nil {
			slog.Warn("telegram chat ID parse failed, push notifications disabled", "env", env, "value", raw, "err", err)
		}
	}

	b, err := telebot.NewBot(telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}

	bot := &Bot{bot: b, agent: ag, chatID: chatID}
	bot.registerHandlers()
	return bot, nil
}

func (b *Bot) registerHandlers() {
	b.bot.Handle("/start", func(c telebot.Context) error {
		return c.Send("Hello, I'm Virgil. Send me a message and I'll get to work.")
	})

	b.bot.Handle("/help", func(c telebot.Context) error {
		return c.Send("Send me any message. I can check email, calendar, Slack, run skills, and more.")
	})

	b.bot.Handle(telebot.OnText, func(c telebot.Context) error {
		if b.chatID != 0 && c.Chat().ID != b.chatID {
			slog.Warn("telegram: ignoring message from unauthorized chat", "chat_id", c.Chat().ID)
			return nil
		}
		ctx := context.Background()
		sig := internal.NewSignal("telegram", c.Text())
		resp, err := b.agent.Run(ctx, sig)
		if err != nil {
			slog.Error("telegram agent error", "err", err)
			return c.Send("Something went wrong. Check the logs.")
		}
		if resp == "" {
			resp = "Done."
		}
		return sendFormatted(c, resp)
	})
}

func sendFormatted(c telebot.Context, text string) error {
	opts := &telebot.SendOptions{ParseMode: telebot.ModeHTML}
	chunks := splitHTML(mdToHTML(text))
	for i, chunk := range chunks {
		if err := c.Send(chunk, opts); err != nil {
			if i > 0 {
				slog.Error("telegram partial send", "sent", i, "total", len(chunks), "err", err)
				_ = c.Send(fmt.Sprintf("(Message cut short at chunk %d/%d. Try again.)", i+1, len(chunks)))
			}
			return err
		}
	}
	return nil
}

// Start begins polling for updates. Blocks until Stop is called.
func (b *Bot) Start() {
	slog.Info("telegram bot started")
	b.bot.Start()
}

// Stop halts polling.
func (b *Bot) Stop() {
	b.bot.Stop()
}

// Push sends a message to the configured chat ID. No-ops if chat ID is unset.
func (b *Bot) Push(text string) error {
	if b.chatID == 0 {
		return nil
	}
	chat := &telebot.Chat{ID: b.chatID}
	opts := &telebot.SendOptions{ParseMode: telebot.ModeHTML}
	chunks := splitHTML(mdToHTML(text))
	for i, chunk := range chunks {
		if _, err := b.bot.Send(chat, chunk, opts); err != nil {
			if i > 0 {
				slog.Warn("telegram push partial send", "delivered", i, "total", len(chunks), "err", err)
			}
			return err
		}
	}
	return nil
}
