package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

type ReviewResult struct {
	Outcome  string    `json:"outcome"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

type Finding struct {
	Severity    string `json:"severity"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
}

type DevReviewResult struct {
	Outcome  string       `json:"outcome"`
	Summary  string       `json:"summary"`
	Findings []DevFinding `json:"findings"`
}

type DevFinding struct {
	Category string `json:"category"` // architecture, logic, testing, style, security
	Severity string `json:"severity"` // major, minor, nit
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Action   string `json:"action"`
}

type templateData struct {
	Content    string
	Topic      string
	Strictness string
}

// DiffFetcher abstracts fetching PR diffs for testability.
type DiffFetcher interface {
	FetchPRDiff(ctx context.Context, prIdentifier string) (string, error)
}

// GHDiffFetcher fetches PR diffs via the gh CLI.
type GHDiffFetcher struct{}

func (f *GHDiffFetcher) FetchPRDiff(ctx context.Context, prIdentifier string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", prIdentifier)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr diff: %w", err)
	}
	return string(out), nil
}

// CompileTemplates pre-parses all prompt templates from the pipe config.
var CompileTemplates = pipeutil.CompileTemplates

func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), &GHDiffFetcher{}, logger)
}

func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, fetcher DiffFetcher, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("review", "review")
		out.Args = flags

		// If source is pr-diff, fetch the diff and use it as content
		source := flags["source"]
		if source == "pr-diff" {
			prID := extractPRIdentifier(input, flags)
			if prID == "" {
				out.Error = envelope.FatalError("source=pr-diff but no PR identifier found")
				out.Duration = time.Since(out.Timestamp)
				return out
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			diffContent, err := fetcher.FetchPRDiff(ctx, prID)
			if err != nil {
				out.Error = envelope.ClassifyError("fetch PR diff", err)
				out.Duration = time.Since(out.Timestamp)
				return out
			}
			// Override input content with the fetched diff
			input.Content = diffContent
			input.ContentType = envelope.ContentText
		}

		systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
		if errEnv != nil {
			out.Error = errEnv
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		logger.Debug("reviewing", "criteria", flags["criteria"], "prompt_len", len(userPrompt))
		raw, err := provider.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			logger.Error("review failed", "error", err)
			out.Error = envelope.ClassifyError("review failed", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		criteria := flags["criteria"]
		var content any
		var outcome string
		var parseErr error
		if criteria == "dev-review" {
			result, err := parseDevReviewResult(raw)
			content, outcome, parseErr = result, result.Outcome, err
		} else {
			result, err := parseReviewResult(raw)
			content, outcome, parseErr = result, result.Outcome, err
		}
		if parseErr != nil {
			logger.Error("review parse failed", "error", parseErr)
			out.Error = envelope.FatalError(fmt.Sprintf("%s — raw response: %s", parseErr.Error(), raw))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
		logger.Info("reviewed", "criteria", criteria, "outcome", outcome)
		out.Content = content

		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// extractPRIdentifier extracts the PR number or URL from flags or envelope content.
func extractPRIdentifier(input envelope.Envelope, flags map[string]string) string {
	// Check flags first
	if pr := flags["pr"]; pr != "" {
		return pr
	}
	// Check input envelope for structured content with pr_url or pr_number
	if m, ok := input.Content.(map[string]any); ok {
		if url, ok := m["pr_url"].(string); ok && url != "" {
			return url
		}
		if num, ok := m["pr_number"]; ok {
			return fmt.Sprintf("%v", num)
		}
	}
	return ""
}

func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	content := envelope.ContentToText(input.Content, input.ContentType)
	if content == "" {
		content = flags["topic"]
	}
	if content == "" {
		return "", "", envelope.FatalError("no content provided for review")
	}

	criteria := pipeutil.FlagOrDefault(flags, "criteria", "correctness")
	if _, ok := compiled[criteria]; !ok {
		return "", "", envelope.FatalError(fmt.Sprintf("unknown criteria: %q", criteria))
	}

	strictness := pipeutil.FlagOrDefault(flags, "strictness", "normal")
	systemPrompt = buildSystemPrompt(pipeConfig.Prompts.System, criteria, strictness, flags["format"])

	rendered, err := pipeutil.ExecuteTemplate(compiled, criteria, templateData{
		Content:    content,
		Topic:      flags["topic"],
		Strictness: strictness,
	})
	if err != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("template execution failed: %v", err))
	}

	return systemPrompt, rendered, nil
}

func buildSystemPrompt(base, criteria, strictness, format string) string {
	var sb strings.Builder
	// dev-review defines its own JSON schema in the template, so only use
	// the persona paragraph from the system prompt to avoid conflicting schemas.
	if criteria == "dev-review" {
		if idx := strings.Index(base, "\n\n"); idx != -1 {
			sb.WriteString(strings.TrimSpace(base[:idx]))
		} else {
			sb.WriteString(strings.TrimSpace(base))
		}
	} else {
		sb.WriteString(base)
	}

	switch strictness {
	case "lenient":
		sb.WriteString("\n\nStrictness: lenient — only report clear errors. Ignore style nitpicks and minor warnings.")
	case "strict":
		sb.WriteString("\n\nStrictness: strict — report everything including minor style issues, potential improvements, and nitpicks.")
	default:
		sb.WriteString("\n\nStrictness: normal — report errors and significant warnings. Ignore minor style nitpicks.")
	}

	if format == "summary" {
		sb.WriteString("\n\nFormat: summary — limit findings to the most important issues. Keep the findings list short and focused.")
	}

	return sb.String()
}

func parseReviewResult(raw string) (ReviewResult, error) {
	cleaned := stripMarkdownFences(raw)
	var result ReviewResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return ReviewResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	switch result.Outcome {
	case "pass", "fail", "needs-revision":
		// valid
	default:
		return ReviewResult{}, fmt.Errorf("invalid outcome: %q (must be pass, fail, or needs-revision)", result.Outcome)
	}
	if result.Findings == nil {
		result.Findings = []Finding{}
	}
	return result, nil
}

func parseDevReviewResult(raw string) (DevReviewResult, error) {
	cleaned := stripMarkdownFences(raw)
	var result DevReviewResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return DevReviewResult{}, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	if result.Outcome != "pass" && result.Outcome != "fail" {
		return DevReviewResult{}, fmt.Errorf("invalid outcome: %q", result.Outcome)
	}
	if result.Findings == nil {
		result.Findings = []DevFinding{}
	}
	// Validate finding fields
	for i, f := range result.Findings {
		if !isValidCategory(f.Category) {
			return DevReviewResult{}, fmt.Errorf("finding %d: invalid category %q", i, f.Category)
		}
		if !isValidDevSeverity(f.Severity) {
			return DevReviewResult{}, fmt.Errorf("finding %d: invalid severity %q", i, f.Severity)
		}
	}
	return result, nil
}

func isValidCategory(c string) bool {
	switch c {
	case "architecture", "logic", "testing", "style", "security":
		return true
	}
	return false
}

func isValidDevSeverity(s string) bool {
	switch s {
	case "major", "minor", "nit":
		return true
	}
	return false
}

var stripMarkdownFences = pipeutil.StripMarkdownFences
