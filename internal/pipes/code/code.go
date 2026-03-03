package code

import (
	"context"
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
	Lang    string
	Style   string
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		content = flags["topic"]
	}
	if content == "" {
		return "", "", envelope.FatalError("no content or topic provided for code generation")
	}

	codeType := pipeutil.FlagOrDefault(flags, "type", "function")

	systemPrompt = pipeConfig.Prompts.System
	userPrompt = executeTemplate(compiled, codeType, templateData{
		Content: content,
		Topic:   flags["topic"],
		Lang:    flags["lang"],
		Style:   flags["style"],
	})
	return systemPrompt, userPrompt, nil
}

func codeError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("code generation failed", err)
}

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("code", "generate")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		logger.Debug("generating code", "type", flags["type"], "prompt_len", len(userPrompt))
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("code generation failed", "error", err)
			out.Error = codeError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("code generated", "type", flags["type"])
		out.Content = result
		out.ContentType = envelope.ContentText
		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
		}
		out.Duration = time.Since(out.Timestamp)
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
		out := envelope.New("code", "generate")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		logger.Debug("generating code", "type", flags["type"], "prompt_len", len(userPrompt))
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("code generation failed", "error", err)
			out.Error = codeError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("code generated", "type", flags["type"])
		out.Content = result
		out.ContentType = envelope.ContentText
		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func executeTemplate(compiled map[string]*template.Template, codeType string, data templateData) string {
	result, err := pipeutil.ExecuteTemplate(compiled, codeType, data)
	if err != nil {
		return data.Content
	}
	return result
}
