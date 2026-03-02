package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

func NewHandler(provider bridge.Provider) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("chat", "respond")
		out.Args = flags

		content := envelope.ContentToText(input.Content, input.ContentType)
		if content == "" {
			out.Content = "I didn't catch that. Could you try again?"
			out.ContentType = "text"
			return out
		}

		systemPrompt := "You are Virgil, a personal assistant. Respond helpfully and concisely."

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := provider.Complete(ctx, systemPrompt, content)
		if err != nil {
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("chat failed: %v", err),
				Severity:  "fatal",
				Retryable: false,
			}
			return out
		}

		out.Content = result
		out.ContentType = "text"
		return out
	}
}
