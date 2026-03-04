package chat

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// CompileSystemPrompts extracts system prompt templates from the pipe config.
// These are plain strings (not Go templates) since system prompts don't need
// variable interpolation — context comes from the user prompt side.
func CompileSystemPrompts(pipeConfig config.PipeConfig) map[string]string {
	return maps.Clone(pipeConfig.Prompts.Templates)
}

// resolveSystemPrompt selects a system prompt based on role and phase flags.
// Resolution order: compound key (role-phase), role alone, then basePrompt.
// The general/default role always returns the basePrompt.
func resolveSystemPrompt(prompts map[string]string, basePrompt string, flags map[string]string) string {
	role := flags["role"]
	if role == "" || role == "general" {
		return basePrompt
	}

	// Try compound key: role-phase
	if phase := flags["phase"]; phase != "" {
		if p, ok := prompts[role+"-"+phase]; ok {
			return p
		}
	}

	// Try role alone
	if p, ok := prompts[role]; ok {
		return p
	}

	// Fall back to base
	return basePrompt
}

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

	signal := flags["signal"]
	hasPipelineSynthesis := signal != "" && signal != content

	// Pipeline synthesis: when an upstream pipe transformed the content,
	// combine the original signal with the pipe output so the model can
	// answer the question using the retrieved context.
	if hasPipelineSynthesis {
		content = fmt.Sprintf("The user said: %q\n\nContext:\n%s\n\nRespond to the user based on the above context. Be concise and natural.", signal, content)
	}

	if len(input.Memory) > 0 {
		var parts []string
		for _, m := range input.Memory {
			parts = append(parts, m.Content)
		}
		memContext := strings.Join(parts, "\n---\n")
		if hasPipelineSynthesis {
			content = "Codebase context:\n" + memContext + "\n\n---\n\n" + content
		} else {
			content = fmt.Sprintf("The user said: %q\n\nRelevant codebase context:\n%s\n\nAnswer the user's question using the context above. Be concise and natural.", content, memContext)
		}
	}

	return out, content, false
}

// chatError wraps a provider error into an EnvelopeError.
func chatError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("chat failed", err)
}

func NewStreamHandler(provider bridge.StreamingProvider, systemPrompt string, prompts map[string]string, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()

		resolved := resolveSystemPrompt(prompts, systemPrompt, flags)
		result, err := provider.CompleteStream(ctx, resolved, content, sink)
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

func NewHandler(provider bridge.Provider, systemPrompt string, prompts map[string]string, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out, content, empty := prepareChat(input, flags)
		if empty {
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()

		resolved := resolveSystemPrompt(prompts, systemPrompt, flags)
		result, err := provider.Complete(ctx, resolved, content)
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
