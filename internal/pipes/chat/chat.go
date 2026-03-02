package chat

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// prepareChat extracts user content and creates the output envelope.
// Returns the output envelope, the user content, and whether the content was empty.
func prepareChat(input envelope.Envelope, flags map[string]string) (envelope.Envelope, string, bool) {
	out := envelope.New("chat", "respond")
	out.Args = flags

	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		out.Content = "I didn't catch that. Could you try again?"
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out, "", true
	}

	// Pipeline synthesis: when an upstream pipe transformed the content,
	// combine the original signal with the pipe output so the model can
	// answer the question using the retrieved context.
	if signal := flags["signal"]; signal != "" && signal != content {
		content = fmt.Sprintf("The user said: %q\n\nContext:\n%s\n\nRespond to the user based on the above context. Be concise and natural.", signal, content)
	}

	return out, content, false
}

// chatError wraps a provider error into an EnvelopeError.
func chatError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("chat failed", err)
}

func NewStreamHandler(provider bridge.StreamingProvider, systemPrompt string, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := provider.CompleteStream(ctx, systemPrompt, content, sink)
		if err != nil {
			logger.Error("chat failed", "error", err)
			out.Error = chatError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("responded")
		logger.Debug("response details", "bytes", len(result))
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func NewHandler(provider bridge.Provider, systemPrompt string, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := provider.Complete(ctx, systemPrompt, content)
		if err != nil {
			logger.Error("chat failed", "error", err)
			out.Error = chatError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("responded")
		logger.Debug("response details", "bytes", len(result))
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
