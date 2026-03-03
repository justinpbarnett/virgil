package worktree

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// WorktreeOutput is the structured content returned on success.
type WorktreeOutput struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit"`
	Created    bool   `json:"created"`
}

// GitExecutor abstracts git command execution for testability.
type GitExecutor interface {
	// Run executes a git command and returns its combined stdout, or an error
	// containing stderr context.
	Run(args ...string) (string, error)
}

// OSGitExecutor implements GitExecutor by shelling out to the git binary.
type OSGitExecutor struct{}

func (e *OSGitExecutor) Run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// NewHandler returns a pipe.Handler that creates or reuses git worktrees.
func NewHandler(executor GitExecutor, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("worktree", "create")
		out.Args = flags

		// 1. Derive branch name
		branch := flags["branch"]
		if branch == "" {
			text := envelope.ContentToText(input.Content, input.ContentType)
			branch = slugify(text)
		}
		if branch == "" {
			out.Error = envelope.FatalError("no branch name: provide a branch flag or input content")
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		// 2. Base ref
		base := flags["base"]
		if base == "" {
			base = "HEAD"
		}

		// 3. Determine worktree path
		repoRoot, err := executor.Run("rev-parse", "--show-toplevel")
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("not a git repository: %v", err))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
		repoRoot = strings.TrimSpace(repoRoot)

		wtPath := flags["path"]
		if wtPath == "" {
			// Use the leaf part of the branch for the directory name
			dirName := branch
			if idx := strings.LastIndex(branch, "/"); idx >= 0 {
				dirName = branch[idx+1:]
			}
			wtPath = filepath.Join(repoRoot, ".worktrees", dirName)
		} else {
			// Validate explicit path is within the repository tree
			absPath, _ := filepath.Abs(wtPath)
			absRoot, _ := filepath.Abs(repoRoot)
			if !strings.HasPrefix(absPath, absRoot) {
				out.Error = envelope.FatalError("worktree path must be within the repository directory tree")
				out.Duration = time.Since(out.Timestamp)
				return out
			}
		}

		// 4. Check if worktree already exists
		existingBranch, exists := findWorktree(executor, wtPath)
		if exists {
			if existingBranch != branch {
				var msg string
				if existingBranch == "" {
					msg = fmt.Sprintf(
						"worktree at %s is in detached HEAD state, expected branch %s",
						wtPath, branch,
					)
				} else {
					msg = fmt.Sprintf(
						"worktree at %s is on branch %s, expected %s",
						wtPath, existingBranch, branch,
					)
				}
				out.Error = envelope.FatalError(msg)
				out.Duration = time.Since(out.Timestamp)
				return out
			}

			// Reuse existing worktree
			logger.Info("reusing existing worktree", "path", wtPath, "branch", branch)
			baseCommit, _ := resolveCommit(executor, base)
			out.Content = WorktreeOutput{
				Path:       wtPath,
				Branch:     branch,
				BaseCommit: baseCommit,
				Created:    false,
			}
			out.ContentType = envelope.ContentStructured
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		// 5. Resolve base commit SHA before creating
		baseCommit, err := resolveCommit(executor, base)
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("cannot resolve base ref %q: %v", base, err))
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		// 6. Check if branch already exists
		_, branchErr := executor.Run("rev-parse", "--verify", "refs/heads/"+branch)
		if branchErr == nil {
			// Branch exists, create worktree on existing branch
			logger.Info("creating worktree on existing branch", "path", wtPath, "branch", branch)
			if _, err := executor.Run("worktree", "add", wtPath, branch); err != nil {
				out.Error = envelope.FatalError(fmt.Sprintf("git worktree add failed: %v", err))
				out.Duration = time.Since(out.Timestamp)
				return out
			}
		} else {
			// Create new branch and worktree
			logger.Info("creating new worktree", "path", wtPath, "branch", branch, "base", base)
			if _, err := executor.Run("worktree", "add", "-b", branch, wtPath, base); err != nil {
				out.Error = envelope.FatalError(fmt.Sprintf("git worktree add failed: %v", err))
				out.Duration = time.Since(out.Timestamp)
				return out
			}
		}

		out.Content = WorktreeOutput{
			Path:       wtPath,
			Branch:     branch,
			BaseCommit: baseCommit,
			Created:    true,
		}
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

// findWorktree checks if a worktree exists at the given path by parsing
// `git worktree list --porcelain`. Returns the branch name and whether the
// worktree was found.
func findWorktree(executor GitExecutor, wtPath string) (branch string, exists bool) {
	output, err := executor.Run("worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}

	absPath, _ := filepath.Abs(wtPath)

	// Parse porcelain output: blocks separated by blank lines.
	// Each block has lines like:
	//   worktree /absolute/path
	//   HEAD abc123
	//   branch refs/heads/branch-name
	blocks := strings.Split(output, "\n\n")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		var blockPath, blockBranch string
		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				blockPath = strings.TrimPrefix(line, "worktree ")
			}
			if strings.HasPrefix(line, "branch ") {
				ref := strings.TrimPrefix(line, "branch ")
				blockBranch = strings.TrimPrefix(ref, "refs/heads/")
			}
		}
		blockAbs, _ := filepath.Abs(blockPath)
		if blockAbs == absPath {
			return blockBranch, true
		}
	}
	return "", false
}

// resolveCommit returns the full SHA for a ref.
func resolveCommit(executor GitExecutor, ref string) (string, error) {
	out, err := executor.Run("rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// nonAlphaNum matches any character that is not a lowercase letter, digit, or forward slash.
var nonAlphaNum = regexp.MustCompile(`[^a-z0-9/]+`)

// multiHyphen matches two or more consecutive hyphens.
var multiHyphen = regexp.MustCompile(`-{2,}`)

// slugify converts arbitrary text into a branch-safe slug.
// - Lowercases the input
// - Replaces spaces and special characters with hyphens
// - Collapses consecutive hyphens
// - Trims leading/trailing hyphens
// - Prefixes with "feat/" if no slash-prefix is present
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if s == "" {
		return ""
	}

	if !strings.Contains(s, "/") {
		s = "feat/" + s
	}

	return s
}
