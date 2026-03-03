package build

import (
	"context"
	"encoding/json"
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

// BuildOutput is the structured output from the build pipe.
type BuildOutput struct {
	Summary       string   `json:"summary"`
	FilesCreated  []string `json:"files_created"`
	FilesModified []string `json:"files_modified"`
	TestsWritten  int      `json:"tests_written"`
	Style         string   `json:"style"`
	CycleNumber   int      `json:"cycle_number"`
}

// ReviewFinding represents a single finding from the review pipe.
type ReviewFinding struct {
	Category string `json:"category"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Action   string `json:"action"`
}

type templateData struct {
	Spec     string
	Context  string
	Style    string
	Findings []ReviewFinding
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	spec := pipeutil.FlagOrDefault(flags, "spec", "")
	if spec == "" {
		spec = envelope.ContentToText(input.Content, input.ContentType)
	}
	if spec == "" {
		return "", "", envelope.FatalError("no spec provided for build")
	}

	style := pipeutil.FlagOrDefault(flags, "style", "tdd")
	cxt := envelope.ContentToText(input.Content, input.ContentType)

	var findings []ReviewFinding
	if findingsJSON := flags["findings"]; findingsJSON != "" {
		if err := json.Unmarshal([]byte(findingsJSON), &findings); err != nil {
			return "", "", envelope.FatalError(fmt.Sprintf("invalid findings JSON: %v", err))
		}
	}

	tmplName := "initial"
	if len(findings) > 0 {
		tmplName = "rework"
	}

	systemPrompt = pipeConfig.Prompts.System

	userPrompt, err := pipeutil.ExecuteTemplate(compiled, tmplName, templateData{
		Spec:     spec,
		Context:  cxt,
		Style:    style,
		Findings: findings,
	})
	if err != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("template execution failed: %v", err))
	}

	return systemPrompt, userPrompt, nil
}

func buildError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("build failed", err)
}

func buildOutput(result string, flags map[string]string) BuildOutput {
	style := pipeutil.FlagOrDefault(flags, "style", "tdd")
	cycleNumber := 1
	if flags["findings"] != "" {
		cycleNumber = 2
	}
	return BuildOutput{
		Summary:       result,
		FilesCreated:  []string{},
		FilesModified: []string{},
		Style:         style,
		CycleNumber:   cycleNumber,
	}
}

// NewHandler creates a build pipe Handler that compiles templates on the fly.
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewHandlerWith creates a build pipe Handler with pre-compiled templates.
func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("build", "build")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()

		style := pipeutil.FlagOrDefault(flags, "style", "tdd")

		logger.Debug("building", "style", style, "prompt_len", len(userPrompt))
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("build failed", "error", err)
			out.Error = buildError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		output := buildOutput(result, flags)
		logger.Info("build complete", "style", style, "cycle", output.CycleNumber)
		out.Content = output
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// NewStreamHandler creates a streaming build pipe handler that compiles templates on the fly.
func NewStreamHandler(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.StreamHandler {
	return NewStreamHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewStreamHandlerWith creates a streaming build pipe handler with pre-compiled templates.
func NewStreamHandlerWith(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out := envelope.New("build", "build")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 300*time.Second)
		defer cancel()

		style := pipeutil.FlagOrDefault(flags, "style", "tdd")

		logger.Debug("building (stream)", "style", style, "prompt_len", len(userPrompt))
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("build failed", "error", err)
			out.Error = buildError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		output := buildOutput(result, flags)
		logger.Info("build complete (stream)", "style", style, "cycle", output.CycleNumber)
		out.Content = output
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
