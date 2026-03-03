package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// FixOutput is the structured output of the fix pipe.
type FixOutput struct {
	Summary string `json:"summary"`
	// FilesModified and FixesApplied are populated when the provider
	// returns structured change information. Currently empty because
	// the provider modifies files directly via tool use.
	FilesModified []string `json:"files_modified"`
	FixesApplied  int      `json:"fixes_applied"`
	Attempt       int      `json:"attempt"`
}

// fixPromptData is the template rendering context for fix prompts.
type fixPromptData struct {
	TestFailures []TestFailure
	LintErrors   []LintError
	Attempt      int
}

// TestFailure represents a single test failure from the verify pipe output.
// Redefined here to maintain pipe isolation (pipes don't import each other).
type TestFailure struct {
	File     string `json:"file"`
	Test     string `json:"test"`
	Package  string `json:"package"`
	Error    string `json:"error"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// LintError represents a single lint error from the verify pipe output.
// Redefined here to maintain pipe isolation (pipes don't import each other).
type LintError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

// parseVerifyOutput extracts TestFailures and LintErrors from the input
// envelope's structured content. The content is expected to be a map[string]any
// (from JSON deserialization) containing test_result and lint_result sub-objects.
func parseVerifyOutput(content any) ([]TestFailure, []LintError, error) {
	if content == nil {
		return nil, nil, fmt.Errorf("input content is nil")
	}

	// The content comes through JSON, so it will be map[string]any.
	contentMap, ok := content.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("input content is not a structured object (got %T)", content)
	}

	var failures []TestFailure
	var lintErrors []LintError

	// Extract test_result.failures
	if testResult, ok := contentMap["test_result"]; ok && testResult != nil {
		if testResultMap, ok := testResult.(map[string]any); ok {
			if failuresRaw, ok := testResultMap["failures"]; ok && failuresRaw != nil {
				failures = parseTestFailures(failuresRaw)
			}
		}
	}

	// Extract lint_result.errors
	if lintResult, ok := contentMap["lint_result"]; ok && lintResult != nil {
		if lintResultMap, ok := lintResult.(map[string]any); ok {
			if errorsRaw, ok := lintResultMap["errors"]; ok && errorsRaw != nil {
				lintErrors = parseLintErrors(errorsRaw)
			}
		}
	}

	return failures, lintErrors, nil
}

// parseTestFailures converts a raw []any into typed TestFailure slices
// by round-tripping through JSON.
func parseTestFailures(raw any) []TestFailure {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var failures []TestFailure
	if err := json.Unmarshal(data, &failures); err != nil {
		return nil
	}
	return failures
}

// parseLintErrors converts a raw []any into typed LintError slices
// by round-tripping through JSON.
func parseLintErrors(raw any) []LintError {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var result []LintError
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

func parseAttempt(flags map[string]string) int {
	if s, ok := flags["attempt"]; ok {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	// Parse the structured verify output from the input envelope.
	failures, lintErrors, err := parseVerifyOutput(input.Content)
	if err != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("failed to parse verify output: %v", err))
	}

	// If nothing to fix, signal early return.
	if len(failures) == 0 && len(lintErrors) == 0 {
		return "", "", nil
	}

	attempt := parseAttempt(flags)

	scope := pipeutil.FlagOrDefault(flags, "scope", "targeted")

	systemPrompt = pipeConfig.Prompts.System

	userPrompt, renderErr := pipeutil.ExecuteTemplate(compiled, scope, fixPromptData{
		TestFailures: failures,
		LintErrors:   lintErrors,
		Attempt:      attempt,
	})
	if renderErr != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("failed to render fix prompt: %v", renderErr))
	}

	return systemPrompt, userPrompt, nil
}

func fixError(err error) *envelope.EnvelopeError {
	return envelope.ClassifyError("fix failed", err)
}

func fixOutput(summary string, attempt int) FixOutput {
	return FixOutput{
		Summary:       summary,
		FilesModified: []string{},
		Attempt:       attempt,
	}
}

// NewHandler creates a fix handler using the default compiled templates.
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewHandlerWith creates a fix handler with pre-compiled templates.
func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("fix", "apply")
		out.Args = flags
		attempt := parseAttempt(flags)

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if systemPrompt == "" && userPrompt == "" {
			out.Content = fixOutput("Nothing to fix", attempt)
			out.ContentType = envelope.ContentStructured
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		logger.Debug("applying fixes", "scope", flags["scope"], "attempt", attempt)
		result, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("fix failed", "error", err)
			out.Error = fixError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("fixes applied", "scope", flags["scope"], "attempt", attempt)
		out.Content = fixOutput(result, attempt)
		out.ContentType = envelope.ContentStructured
		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// NewStreamHandler creates a streaming fix handler using the default compiled templates.
func NewStreamHandler(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.StreamHandler {
	return NewStreamHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewStreamHandlerWith creates a streaming fix handler with pre-compiled templates.
func NewStreamHandlerWith(provider bridge.StreamingProvider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.StreamHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		out := envelope.New("fix", "apply")
		out.Args = flags
		attempt := parseAttempt(flags)

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if systemPrompt == "" && userPrompt == "" {
			out.Content = fixOutput("Nothing to fix", attempt)
			out.ContentType = envelope.ContentStructured
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		logger.Debug("applying fixes", "scope", flags["scope"], "attempt", attempt)
		result, err := provider.CompleteStream(ctx, systemPrompt, userPrompt, sink)
		if err != nil {
			logger.Error("fix failed", "error", err)
			out.Error = fixError(err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		logger.Info("fixes applied", "scope", flags["scope"], "attempt", attempt)
		out.Content = fixOutput(result, attempt)
		out.ContentType = envelope.ContentStructured
		if result == "" {
			out.Error = envelope.WarnError("provider returned empty response")
		}
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

