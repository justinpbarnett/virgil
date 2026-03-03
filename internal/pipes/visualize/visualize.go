package visualize

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// Renderer abstracts Manim execution for testability.
type Renderer interface {
	Render(ctx context.Context, code string, format string, quality string) (outputPath string, err error)
}

// ManimRenderer implements Renderer using manim CLI.
type ManimRenderer struct {
	Executor  pipeutil.Executor
	OutputDir string // base directory for rendered files
}

type templateData struct {
	Content       string
	Topic         string
	Duration      string
	DurationGuide string
	Style         string
	StyleGuide    string
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

func durationGuide(duration string) string {
	switch duration {
	case "short":
		return "5-15 seconds, 3-5 animation steps"
	case "medium":
		return "15-30 seconds, 5-10 animation steps"
	case "long":
		return "30-60 seconds, 10-20 animation steps"
	default:
		return "5-15 seconds, 3-5 animation steps"
	}
}

func styleGuide(style string) string {
	switch style {
	case "3b1b":
		return "Dark background (#1a1a2e or BLACK), blue/yellow/white color scheme, clean sans-serif typography, smooth transformations"
	case "light":
		return "White background, dark text, muted color palette, professional presentation style"
	case "minimal":
		return "Black background, white objects only, no color, stark geometric aesthetic"
	default:
		return "Dark background (#1a1a2e or BLACK), blue/yellow/white color scheme, clean sans-serif typography, smooth transformations"
	}
}

func qualityFlag(quality string) string {
	switch quality {
	case "low":
		return "-ql"
	case "medium":
		return "-qm"
	case "high":
		return "-qh"
	default:
		return "-ql"
	}
}

func formatFlag(format string) string {
	switch format {
	case "gif":
		return "--format=gif"
	case "png":
		return "--format=png -s"
	default:
		return ""
	}
}

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)
	topic := flags["topic"]
	if topic == "" {
		topic = content
	}
	if topic == "" {
		return "", "", envelope.FatalError("no topic or content provided for visualization")
	}

	systemPrompt = pipeConfig.Prompts.System

	templateKey := flags["type"]
	if templateKey == "" {
		templateKey = "concept"
	}

	duration := pipeutil.FlagOrDefault(flags, "duration", "short")
	style := pipeutil.FlagOrDefault(flags, "style", "3b1b")

	data := templateData{
		Content:       content,
		Topic:         topic,
		Duration:      duration,
		DurationGuide: durationGuide(duration),
		Style:         style,
		StyleGuide:    styleGuide(style),
	}

	rendered, err := pipeutil.ExecuteTemplate(compiled, templateKey, data)
	if err != nil {
		// Fall back to concept template
		rendered, err = pipeutil.ExecuteTemplate(compiled, "concept", data)
		if err != nil {
			return "", "", envelope.FatalError(fmt.Sprintf("template execution failed: %v", err))
		}
	}

	return systemPrompt, rendered, nil
}

// extractCode strips markdown fences from AI response and validates the result
// contains required Manim CE elements.
func extractCode(response string) (string, error) {
	s := pipeutil.StripMarkdownFences(response)

	if !strings.Contains(s, "class GeneratedScene") {
		return "", fmt.Errorf("generated code missing required class: GeneratedScene")
	}
	if !strings.Contains(s, "from manim import") {
		return "", fmt.Errorf("generated code missing required import: from manim import")
	}

	return s, nil
}

// Render executes manim to render the given Python code.
func (r *ManimRenderer) Render(ctx context.Context, code, format, quality string) (string, error) {
	tmpFile, err := os.CreateTemp("", "visualize-*.py")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(code); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("writing code to temp file: %w", err)
	}
	tmpFile.Close()

	mediaDir := filepath.Join(r.OutputDir, "manim")

	var parts []string
	parts = append(parts, "manim", "render")
	parts = append(parts, qualityFlag(quality))
	if ff := formatFlag(format); ff != "" {
		parts = append(parts, strings.Fields(ff)...)
	}
	parts = append(parts, "--media_dir", mediaDir)
	parts = append(parts, tmpFile.Name(), "GeneratedScene")

	cmd := strings.Join(parts, " ")

	stdout, stderr, exitCode, err := r.Executor.Execute(ctx, cmd, "")
	if err != nil {
		return "", fmt.Errorf("manim execution error: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("manim render failed (exit %d): %s", exitCode, stderr)
	}

	return findOutputPath(stdout, mediaDir, format)
}

func findOutputPath(stdout, mediaDir, format string) (string, error) {
	// Try to parse "File ready at" from manim output
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "File ready at") {
			idx := strings.Index(line, "File ready at")
			path := strings.TrimSpace(line[idx+len("File ready at"):])
			if path != "" {
				if _, err := os.Stat(path); err == nil {
					return path, nil
				}
			}
		}
	}

	// Fallback: glob for output files
	ext := format
	if ext == "" || ext == "mp4" {
		ext = "mp4"
	}
	pattern := filepath.Join(mediaDir, "videos", "*", "*", "GeneratedScene."+ext)
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 && format == "png" {
		pattern = filepath.Join(mediaDir, "images", "*", "GeneratedScene."+ext)
		matches, _ = filepath.Glob(pattern)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("manim output file not found in %s", mediaDir)
	}
	return matches[len(matches)-1], nil
}

// NewHandler creates a pipe.Handler that generates Manim code via AI and renders it.
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, renderer Renderer, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("visualize", "render")
		out.Args = flags

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		topic := flags["topic"]
		if topic == "" {
			topic = envelope.ContentToText(input.Content, input.ContentType)
		}
		format := pipeutil.FlagOrDefault(flags, "format", "mp4")
		quality := pipeutil.FlagOrDefault(flags, "quality", "low")

		// Phase 1: AI code generation (60s timeout)
		aiCtx, aiCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer aiCancel()

		logger.Debug("generating manim code", "topic", topic, "format", format)
		response, err := provider.Complete(aiCtx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("code generation failed", "error", err)
			out.Error = envelope.ClassifyError("code generation failed", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		code, err := extractCode(response)
		if err != nil {
			logger.Error("code extraction failed", "error", err)
			out.Error = envelope.FatalError(fmt.Sprintf("AI did not produce valid Manim code: %v", err))
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		// Phase 2: Manim rendering (60s timeout)
		renderCtx, renderCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer renderCancel()

		logger.Debug("rendering visualization", "format", format, "quality", quality)
		outputPath, err := renderer.Render(renderCtx, code, format, quality)
		if err != nil {
			logger.Error("render failed", "error", err)
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("manim render failed: %v", err),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("visualization rendered", "path", outputPath, "format", format)
		out.Content = map[string]any{
			"path":        outputPath,
			"format":      format,
			"quality":     quality,
			"description": "Visualization of: " + topic,
		}
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
