package spec

import (
	"context"
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
	"github.com/justinpbarnett/virgil/internal/slug"
)

// SpecStore provides working-state persistence for the active spec file.
type SpecStore interface {
	GetState(namespace, key string) (string, bool, error)
	PutState(namespace, key, content string) error
}

type promptData struct {
	Signal          string // user's original text
	State           string // existing spec content (empty on create)
	CodebaseContext string // upstream study output
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

// resolveSpecFile determines the target spec file path and whether it already exists.
// Resolution order: explicit path flag → topic substring match → spec/active working state → new from topic.
func resolveSpecFile(specsDir string, flags map[string]string, store SpecStore) (path string, exists bool) {
	// 1. Explicit path flag — use directly
	if p := flags["path"]; p != "" {
		full := filepath.Join(specsDir, p)
		_, err := os.Stat(full)
		return full, err == nil
	}

	// 2. Topic flag — substring match against specs/ filenames
	topic := flags["topic"]
	if topic == "" {
		topic = flags["signal"]
	}

	if topic != "" {
		if match := findSpec(specsDir, topic); match != "" {
			return match, true
		}
	}

	// 3. Fallback to spec/active working_state
	if store != nil {
		if active, ok, _ := store.GetState("spec", "active"); ok && active != "" {
			full := filepath.Join(specsDir, active)
			if _, err := os.Stat(full); err == nil {
				return full, true
			}
		}
	}

	// 4. New spec — generate filename from topic
	if topic != "" {
		name := "feat-" + slug.Slugify(topic) + ".md"
		return filepath.Join(specsDir, name), false
	}

	return "", false
}

// findSpec searches specsDir for a .md file whose name contains the slugified topic.
// Returns the shortest match to avoid over-broad hits.
func findSpec(specsDir, topic string) string {
	slugged := slug.Slugify(topic)
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return ""
	}
	var best string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if strings.Contains(e.Name(), slugged) {
			if best == "" || len(e.Name()) < len(best) {
				best = e.Name()
			}
		}
	}
	if best != "" {
		return filepath.Join(specsDir, best)
	}
	return ""
}

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string, store SpecStore, specsDir string) (systemPrompt, userPrompt, specPath string, errEnv *envelope.EnvelopeError) {
	specPath, exists := resolveSpecFile(specsDir, flags, store)
	if specPath == "" {
		return "", "", "", envelope.FatalError("could not determine spec file path: provide a topic or signal")
	}

	var state string
	if exists {
		if data, err := os.ReadFile(specPath); err == nil {
			state = string(data)
		}
	}

	templateKey := "create"
	if exists {
		templateKey = "update"
	}

	signal := flags["signal"]
	if signal == "" {
		signal = flags["topic"]
	}

	data := promptData{
		Signal:          signal,
		State:           state,
		CodebaseContext: envelope.ContentToText(input.Content, input.ContentType),
	}

	systemPrompt = pipeConfig.Prompts.System
	result, err := pipeutil.ExecuteTemplate(compiled, templateKey, data)
	if err != nil {
		return "", "", "", envelope.FatalError("template error: " + err.Error())
	}

	return systemPrompt, result, specPath, nil
}

func writeSpec(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, store SpecStore, specsDir string, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), store, specsDir, logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, store SpecStore, specsDir string, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("spec", "generate")
		out.Args = flags

		systemPrompt, userPrompt, specPath, errEnv := preparePrompt(compiled, pipeConfig, input, flags, store, specsDir)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()

		logger.Debug("generating spec", "path", specPath, "prompt_len", len(userPrompt))
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("spec generation failed", "error", err)
			out.Error = envelope.ClassifyError("spec generation failed", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if err := writeSpec(specPath, result); err != nil {
			logger.Error("failed to write spec", "path", specPath, "error", err)
			out.Error = envelope.FatalError("failed to write spec: " + err.Error())
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if store != nil {
			_ = store.PutState("spec", "active", filepath.Base(specPath))
		}

		logger.Info("spec written", "path", specPath)
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func NewStreamHandler(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, store SpecStore, specsDir string, logger *slog.Logger) pipe.StreamHandler {
	return NewStreamHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), store, specsDir, logger)
}

func NewStreamHandlerWith(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, store SpecStore, specsDir string, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out := envelope.New("spec", "generate")
		out.Args = flags

		systemPrompt, userPrompt, specPath, errEnv := preparePrompt(compiled, pipeConfig, input, flags, store, specsDir)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()

		logger.Debug("generating spec", "path", specPath, "prompt_len", len(userPrompt))
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("spec generation failed", "error", err)
			out.Error = envelope.ClassifyError("spec generation failed", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if err := writeSpec(specPath, result); err != nil {
			logger.Error("failed to write spec", "path", specPath, "error", err)
			out.Error = envelope.FatalError("failed to write spec: " + err.Error())
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if store != nil {
			_ = store.PutState("spec", "active", filepath.Base(specPath))
		}

		logger.Info("spec written", "path", specPath)
		out.Content = result
		out.ContentType = envelope.ContentText
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
