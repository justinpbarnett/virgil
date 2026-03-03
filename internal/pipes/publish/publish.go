package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// PublishOutput is the structured content returned by the publish handler.
type PublishOutput struct {
	PRURL       string `json:"pr_url"`
	PRNumber    int    `json:"pr_number"`
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
	Created     bool   `json:"created"`
	DiffSummary string `json:"diff_summary"`
}

// Executor is an alias for pipeutil.Executor.
type Executor = pipeutil.Executor

// OSExecutor is an alias for pipeutil.OSExecutor.
type OSExecutor = pipeutil.OSExecutor

// ghPR represents a pull request returned by gh pr list --json.
type ghPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// NewHandler returns a pipe.Handler that commits, pushes, and creates or
// updates a pull request. The handler is purely deterministic -- it runs
// git and gh commands via the provided Executor.
func NewHandler(executor Executor, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("publish", "publish")
		out.Args = flags

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Step 1: Determine working directory
		cwd := flags["cwd"]
		if cwd == "" {
			cwd = extractCwdFromEnvelope(input)
		}
		if cwd != "" {
			info, err := os.Stat(cwd)
			if err != nil || !info.IsDir() {
				out.Duration = time.Since(out.Timestamp)
				out.Error = envelope.FatalError(fmt.Sprintf("invalid working directory: %s", cwd))
				return out
			}
		}

		logger.Debug("publishing", "cwd", cwd)

		// Step 2: Stage changes
		_, stderr, exitCode, err := executor.Execute(ctx, "git add -A", cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git add: %v", err))
			return out
		}
		if exitCode != 0 {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git add failed: %s", strings.TrimSpace(stderr)))
			return out
		}

		// Step 3: Check for changes
		diffStat, _, exitCode, err := executor.Execute(ctx, "git diff --cached --stat", cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git diff: %v", err))
			return out
		}
		if exitCode != 0 || strings.TrimSpace(diffStat) == "" {
			out.Content = "nothing to publish"
			out.ContentType = envelope.ContentText
			out.Error = &envelope.EnvelopeError{
				Message:  "nothing to publish",
				Severity: envelope.SeverityWarn,
			}
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		// Step 4: Generate commit message
		summary := extractSummary(input)
		commitMsg := fmt.Sprintf("feat: %s", summary)
		body := strings.TrimSpace(diffStat)
		fullMsg := fmt.Sprintf("%s\n\n%s", commitMsg, body)

		// Step 5: Commit
		commitOut, commitStderr, exitCode, err := executor.Execute(ctx, fmt.Sprintf("git commit -m %q", fullMsg), cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git commit: %v", err))
			return out
		}
		if exitCode != 0 {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git commit failed: %s", strings.TrimSpace(commitStderr)))
			return out
		}

		commitSHA := extractCommitSHA(commitOut)

		// Get current branch name
		branchOut, _, exitCode, err := executor.Execute(ctx, "git rev-parse --abbrev-ref HEAD", cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("git rev-parse: %v", err))
			return out
		}
		if exitCode != 0 {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError("failed to determine branch name")
			return out
		}
		branch := strings.TrimSpace(branchOut)

		// Step 6: Check gh availability before pushing
		_, ghCheckStderr, exitCode, err := executor.Execute(ctx, "gh --version", cwd)
		if err != nil || exitCode != 0 {
			out.Duration = time.Since(out.Timestamp)
			msg := "gh CLI is not installed or not in PATH"
			if ghCheckStderr != "" {
				msg = fmt.Sprintf("gh CLI not available: %s", strings.TrimSpace(ghCheckStderr))
			}
			out.Error = envelope.FatalError(msg)
			return out
		}

		// Step 7: Push
		strategy := flags["update-strategy"]
		if strategy == "" {
			strategy = "force-push"
		}

		// Check if remote branch exists
		_, _, trackExit, _ := executor.Execute(ctx, fmt.Sprintf("git ls-remote --heads origin %s", branch), cwd)

		var pushCmd string
		if trackExit != 0 {
			pushCmd = fmt.Sprintf("git push -u origin %s", branch)
		} else if strategy == "force-push" {
			pushCmd = fmt.Sprintf("git push --force-with-lease origin %s", branch)
		} else {
			pushCmd = fmt.Sprintf("git push origin %s", branch)
		}

		_, pushStderr, exitCode, err := executor.Execute(ctx, pushCmd, cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("git push: %v", err),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
			return out
		}
		if exitCode != 0 {
			out.Duration = time.Since(out.Timestamp)
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("git push failed: %s", strings.TrimSpace(pushStderr)),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
			return out
		}

		// Step 8: Create or update PR
		base := flags["base"]
		if base == "" {
			base = "main"
		}
		draft := flags["draft"] == "true"

		// Check for existing PR
		prListOut, _, exitCode, err := executor.Execute(ctx, fmt.Sprintf("gh pr list --head %s --json number,url", branch), cwd)
		if err != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("gh pr list: %v", err),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
			return out
		}

		prTitle := commitMsg
		prBody := body

		var prURL string
		var prNumber int
		created := false

		existingPRs := parsePRList(prListOut)

		if len(existingPRs) > 0 {
			// Update existing PR
			pr := existingPRs[0]
			prNumber = pr.Number
			prURL = pr.URL

			_, prEditStderr, exitCode, err := executor.Execute(ctx,
				fmt.Sprintf("gh pr edit %d --title %q --body %q", prNumber, prTitle, prBody), cwd)
			if err != nil {
				out.Duration = time.Since(out.Timestamp)
				out.Error = &envelope.EnvelopeError{
					Message:   fmt.Sprintf("gh pr edit: %v", err),
					Severity:  envelope.SeverityError,
					Retryable: true,
				}
				return out
			}
			if exitCode != 0 {
				out.Duration = time.Since(out.Timestamp)
				out.Error = &envelope.EnvelopeError{
					Message:   fmt.Sprintf("gh pr edit failed: %s", strings.TrimSpace(prEditStderr)),
					Severity:  envelope.SeverityError,
					Retryable: true,
				}
				return out
			}
		} else {
			// Create new PR
			createCmd := fmt.Sprintf("gh pr create --title %q --body %q --base %s", prTitle, prBody, base)
			if draft {
				createCmd += " --draft"
			}

			prCreateOut, prCreateStderr, exitCode, err := executor.Execute(ctx, createCmd, cwd)
			if err != nil {
				out.Duration = time.Since(out.Timestamp)
				out.Error = &envelope.EnvelopeError{
					Message:   fmt.Sprintf("gh pr create: %v", err),
					Severity:  envelope.SeverityError,
					Retryable: true,
				}
				return out
			}
			if exitCode != 0 {
				out.Duration = time.Since(out.Timestamp)
				out.Error = &envelope.EnvelopeError{
					Message:   fmt.Sprintf("gh pr create failed: %s", strings.TrimSpace(prCreateStderr)),
					Severity:  envelope.SeverityError,
					Retryable: true,
				}
				return out
			}

			prURL = strings.TrimSpace(prCreateOut)
			prNumber = extractPRNumber(prURL)
			created = true
		}

		// Step 9: Build output
		output := PublishOutput{
			PRURL:       prURL,
			PRNumber:    prNumber,
			CommitSHA:   commitSHA,
			Branch:      branch,
			Created:     created,
			DiffSummary: strings.TrimSpace(diffStat),
		}

		out.Content = output
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// extractCwdFromEnvelope tries to find a working directory from the input
// envelope's structured content (e.g., a worktree path from a previous pipe).
func extractCwdFromEnvelope(env envelope.Envelope) string {
	if env.Content == nil {
		return ""
	}

	// Try map[string]any (most common after JSON round-trip)
	if m, ok := env.Content.(map[string]any); ok {
		if p, ok := m["worktree_path"].(string); ok && p != "" {
			return p
		}
		if p, ok := m["path"].(string); ok && p != "" {
			return p
		}
		if p, ok := m["cwd"].(string); ok && p != "" {
			return p
		}
	}

	return ""
}

// extractSummary pulls a human-readable summary from the input envelope.
func extractSummary(env envelope.Envelope) string {
	if env.Content == nil {
		return "update"
	}

	switch v := env.Content.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s != "" {
			// Truncate long summaries
			if len(s) > 72 {
				return s[:72]
			}
			return s
		}
	case map[string]any:
		if summary, ok := v["summary"].(string); ok && summary != "" {
			if len(summary) > 72 {
				return summary[:72]
			}
			return summary
		}
		if msg, ok := v["message"].(string); ok && msg != "" {
			if len(msg) > 72 {
				return msg[:72]
			}
			return msg
		}
	}

	return "update"
}

// extractCommitSHA parses the commit SHA from git commit output.
// git commit output typically looks like: "[branch abc1234] commit message"
func extractCommitSHA(output string) string {
	// Look for pattern like [branch SHA] or [branch (root-commit) SHA]
	line := strings.TrimSpace(output)
	if idx := strings.Index(line, "["); idx >= 0 {
		if end := strings.Index(line[idx:], "]"); end >= 0 {
			bracket := line[idx+1 : idx+end]
			parts := strings.Fields(bracket)
			if len(parts) >= 2 {
				return parts[len(parts)-1]
			}
		}
	}

	// Fallback: return first 7+ char hex string
	for _, word := range strings.Fields(line) {
		if len(word) >= 7 && isHex(word) {
			return word
		}
	}

	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// parsePRList parses the JSON output of gh pr list --json number,url.
func parsePRList(output string) []ghPR {
	output = strings.TrimSpace(output)
	if output == "" || output == "[]" {
		return nil
	}

	var prs []ghPR
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return nil
	}
	return prs
}

// extractPRNumber extracts the PR number from a URL like
// https://github.com/owner/repo/pull/42
func extractPRNumber(url string) int {
	last := url[strings.LastIndex(url, "/")+1:]
	var n int
	fmt.Sscanf(last, "%d", &n)
	return n
}

