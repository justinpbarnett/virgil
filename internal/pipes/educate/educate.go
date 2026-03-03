package educate

import (
	"context"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

type templateData struct {
	Content string
	Topic   string
	Level   string
	Style   string
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

// resolvePrompt selects and renders the appropriate template based on flags.
// Returns the system prompt and rendered user prompt.
func resolvePrompt(compiled map[string]*template.Template, pc config.PipeConfig, input envelope.Envelope, flags map[string]string) (string, string, *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)

	// Pipeline synthesis: when upstream pipe provided context, combine it
	// with the original signal so the model sees both.
	if signal := flags["signal"]; signal != "" && signal != content {
		content = fmt.Sprintf("The user said: %q\n\nContext:\n%s", signal, content)
	}

	topic := flags["topic"]
	if topic == "" {
		topic = content
	}

	phase := flags["phase"]
	if phase == "" {
		phase = "assess"
	}

	data := templateData{
		Content: content,
		Topic:   topic,
		Level:   flags["level"],
		Style:   flags["style"],
	}

	userPrompt, err := executeTemplate(compiled, phase, data)
	if err != nil {
		// Fall back to raw content with system prompt
		userPrompt = content
	}

	return pc.Prompts.System, userPrompt, nil
}

func executeTemplate(compiled map[string]*template.Template, key string, data templateData) (string, error) {
	return pipeutil.ExecuteTemplate(compiled, key, data)
}

func educateError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("educate failed", err)
}

func NewHandler(provider bridge.Provider, pc config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("educate", "teach")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := resolvePrompt(compiled, pc, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		logger.Debug("educating", "phase", flags["phase"], "prompt_len", len(userPrompt))
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("educate failed", "error", err)
			out.Error = educateError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("educated", "phase", flags["phase"])
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func NewStreamHandler(provider bridge.StreamingProvider, pc config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out := envelope.New("educate", "teach")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := resolvePrompt(compiled, pc, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		logger.Debug("educating", "phase", flags["phase"], "prompt_len", len(userPrompt))
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("educate failed", "error", err)
			out.Error = educateError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("educated", "phase", flags["phase"])
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
