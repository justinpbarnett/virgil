package chat

import (
	"context"
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
		return out, "", true
	}
	return out, content, false
}

// chatError wraps a provider error into an EnvelopeError.
func chatError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("chat failed", err)
}

func NewStreamHandler(provider bridge.StreamingProvider, systemPrompt string) pipe.StreamHandler {
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := provider.CompleteStream(ctx, systemPrompt, content, sink)
		if err != nil {
			out.Error = chatError(err)
			return out
		}

		out.Content = result
		out.ContentType = envelope.ContentText
		return out
	}
}

func NewHandler(provider bridge.Provider, systemPrompt string) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := provider.Complete(ctx, systemPrompt, content)
		if err != nil {
			out.Error = chatError(err)
			return out
		}

		out.Content = result
		out.ContentType = envelope.ContentText
		return out
	}
}
