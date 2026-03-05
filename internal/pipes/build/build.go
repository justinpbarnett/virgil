package build

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"text/template"
	"time"

	gogit "github.com/go-git/go-git/v5"
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

// worktreeChanges uses go-git to diff the working tree and split changed files
// into created (untracked/added) and modified lists. Falls back to empty lists
// if cwd is not a git repository.
func worktreeChanges(cwd string) (created, modified []string) {
	repo, err := gogit.PlainOpen(cwd)
	if err != nil {
		return nil, nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, nil
	}
	status, err := wt.Status()
	if err != nil {
		return nil, nil
	}
	for path, s := range status {
		switch s.Worktree {
		case gogit.Untracked, gogit.Added:
			created = append(created, path)
		case gogit.Modified, gogit.Renamed, gogit.Copied:
			modified = append(modified, path)
		}
		// Also check staging area.
		switch s.Staging {
		case gogit.Added:
			created = append(created, path)
		case gogit.Modified, gogit.Renamed, gogit.Copied:
			modified = append(modified, path)
		}
	}
	return dedupe(created), dedupe(modified)
}

func dedupe(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

const defaultMaxTurns = 20

// runBuild contains the shared logic for both sync and stream handlers.
func runBuild(ctx context.Context, provider bridge.AgenticProvider, compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string, logger *slog.Logger, sink func(string)) envelope.Envelope {
	out := envelope.New("build", "build")
	out.Args = flags

	systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
	if errEnv != nil {
		out.Error = errEnv
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	cwd := flags["cwd"]
	if cwd == "" {
		out.Error = envelope.FatalError("cwd flag is required for agentic build")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	maxTurns := defaultMaxTurns
	if s := flags["max_turns"]; s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			maxTurns = v
		}
	}

	style := pipeutil.FlagOrDefault(flags, "style", "tdd")
	cycleNumber := 1
	if flags["findings"] != "" {
		cycleNumber = 2
	}

	ctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()

	tools := BuildTools(cwd)
	logger.Debug("building", "style", style, "max_turns", maxTurns, "prompt_len", len(userPrompt))

	result, err := bridge.RunAgenticLoop(ctx, provider, systemPrompt, userPrompt, tools, maxTurns, sink)
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

	created, modified := worktreeChanges(cwd)
	output := BuildOutput{
		Summary:       result,
		FilesCreated:  orEmpty(created),
		FilesModified: orEmpty(modified),
		Style:         style,
		CycleNumber:   cycleNumber,
	}
	logger.Info("build complete", "style", style, "cycle", cycleNumber)
	out.Content = output
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

// NewHandler creates a build pipe Handler that compiles templates on the fly.
func NewHandler(provider bridge.AgenticProvider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewHandlerWith creates a build pipe Handler with pre-compiled templates.
func NewHandlerWith(provider bridge.AgenticProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		ctx := context.Background()
		return runBuild(ctx, provider, compiled, pipeConfig, input, flags, logger, nil)
	}
}

// NewStreamHandler creates a streaming build pipe handler that compiles templates on the fly.
func NewStreamHandler(provider bridge.AgenticProvider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.StreamHandler {
	return NewStreamHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewStreamHandlerWith creates a streaming build pipe handler with pre-compiled templates.
func NewStreamHandlerWith(provider bridge.AgenticProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		return runBuild(ctx, provider, compiled, pipeConfig, input, flags, logger, sink)
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
