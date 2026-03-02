package draft

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

type templateData struct {
	Content string
	Topic   string
	Tone    string
	Length  string
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
func CompileTemplates(pipeConfig config.PipeConfig) map[string]*template.Template {
	compiled := make(map[string]*template.Template)
	for name, tmplStr := range pipeConfig.Prompts.Templates {
		t, err := template.New(name).Parse(tmplStr)
		if err == nil {
			compiled[name] = t
		}
	}
	return compiled
}

// preparePrompt extracts content from the input, resolves the template, and
// returns the system prompt, user prompt, and an error envelope if the input is empty.
func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		content = flags["topic"]
	}
	if content == "" {
		return "", "", envelope.FatalError("no content or topic provided for drafting")
	}

	systemPrompt = pipeConfig.Prompts.System
	userPrompt, err := executeTemplate(compiled, flags["type"], templateData{
		Content: content,
		Topic:   flags["topic"],
		Tone:    flags["tone"],
		Length:  flags["length"],
	})
	if err != nil {
		userPrompt = content
	}
	return systemPrompt, userPrompt, nil
}

// draftError wraps an error into an EnvelopeError, marking timeouts as retryable.
func draftError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("draft generation failed", err)
}

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("draft", "generate")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		logger.Debug("drafting", "type", flags["type"], "prompt_len", len(userPrompt))
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("draft failed", "error", err)
			out.Error = draftError(err)
			return out
		}

		logger.Info("drafted", "type", flags["type"])
		out.Content = result
		out.ContentType = envelope.ContentText
		return out
	}
}

func NewStreamHandler(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.StreamHandler {
	return NewStreamHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

func NewStreamHandlerWith(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out := envelope.New("draft", "generate")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		logger.Debug("drafting", "type", flags["type"], "prompt_len", len(userPrompt))
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("draft failed", "error", err)
			out.Error = draftError(err)
			return out
		}

		logger.Info("drafted", "type", flags["type"])
		out.Content = result
		out.ContentType = envelope.ContentText
		return out
	}
}

func executeTemplate(compiled map[string]*template.Template, draftType string, data templateData) (string, error) {
	tmpl, ok := compiled[draftType]
	if !ok {
		if data.Content != "" {
			return data.Content, nil
		}
		return "", fmt.Errorf("no template for type: %s", draftType)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

