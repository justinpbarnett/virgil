package draft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
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

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig) pipe.Handler {
	// Pre-compile templates at init time
	compiled := make(map[string]*template.Template)
	for name, tmplStr := range pipeConfig.Prompts.Templates {
		t, err := template.New(name).Parse(tmplStr)
		if err == nil {
			compiled[name] = t
		}
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("draft", "generate")
		out.Args = flags

		content := envelope.ContentToText(input.Content, input.ContentType)
		if content == "" {
			content = flags["topic"]
		}
		if content == "" {
			out.Error = &envelope.EnvelopeError{
				Message:  "no content or topic provided for drafting",
				Severity: "fatal",
			}
			return out
		}

		draftType := flags["type"]
		systemPrompt := pipeConfig.Prompts.System

		userPrompt, err := executeTemplate(compiled, draftType, templateData{
			Content: content,
			Topic:   flags["topic"],
			Tone:    flags["tone"],
			Length:  flags["length"],
		})
		if err != nil {
			// Fall back to raw content if template fails
			userPrompt = content
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			severity := "fatal"
			retryable := false
			if isTimeout(err) {
				severity = "error"
				retryable = true
			}
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("draft generation failed: %v", err),
				Severity:  severity,
				Retryable: retryable,
			}
			return out
		}

		out.Content = result
		out.ContentType = "text"
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

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
