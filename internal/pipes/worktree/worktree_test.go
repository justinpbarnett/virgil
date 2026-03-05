package worktree

import (
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// mockCall records a single invocation of GitExecutor.Run.
type mockCall struct {
	stdout string
	err    error
}

// mockGitExecutor records calls and returns preset responses based on a
// sequence of expected invocations.
type mockGitExecutor struct {
	calls    []mockCall
	callIdx  int
	recorded [][]string
}

func (m *mockGitExecutor) Run(args ...string) (string, error) {
	m.recorded = append(m.recorded, args)
	if m.callIdx >= len(m.calls) {
		return "", fmt.Errorf("unexpected call: git %s", strings.Join(args, " "))
	}
	c := m.calls[m.callIdx]
	m.callIdx++
	return c.stdout, c.err
}

// porcelainOutput builds a git worktree list --porcelain output block.
func porcelainOutput(entries ...struct{ path, sha, branch string }) string {
	var blocks []string
	for _, e := range entries {
		block := fmt.Sprintf("worktree %s\nHEAD %s\nbranch refs/heads/%s", e.path, e.sha, e.branch)
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func TestWorktreeCreate(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (no existing worktree)
			{stdout: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n"},
			// 3. rev-parse HEAD (resolve base commit)
			{stdout: "abc123def456\n"},
			// 4. rev-parse --verify refs/heads/feat/oauth-login (branch doesn't exist)
			{err: fmt.Errorf("exit status 128: fatal: Needed a single revision")},
			// 5. worktree add -b feat/oauth-login /repo/.worktrees/oauth-login HEAD
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")
	input.Content = "OAuth Login"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{})

	testutil.AssertEnvelope(t, result, "worktree", "create")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	content, ok := result.Content.(WorktreeOutput)
	if !ok {
		t.Fatalf("expected WorktreeOutput, got %T", result.Content)
	}
	if content.Path != "/repo/.worktrees/oauth-login" {
		t.Errorf("expected path=/repo/.worktrees/oauth-login, got %s", content.Path)
	}
	if content.Branch != "feat/oauth-login" {
		t.Errorf("expected branch=feat/oauth-login, got %s", content.Branch)
	}
	if content.BaseCommit != "abc123def456" {
		t.Errorf("expected base_commit=abc123def456, got %s", content.BaseCommit)
	}
	if !content.Created {
		t.Error("expected created=true")
	}
}

func TestWorktreeIdempotent(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (worktree exists at expected path on expected branch)
			{stdout: porcelainOutput(
				struct{ path, sha, branch string }{"/repo", "aaa", "main"},
				struct{ path, sha, branch string }{"/repo/.worktrees/my-feature", "bbb", "feat/my-feature"},
			)},
			// 3. rev-parse HEAD (resolve base commit for output)
			{stdout: "aaa111bbb222\n"},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/my-feature"})

	testutil.AssertEnvelope(t, result, "worktree", "create")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	content, ok := result.Content.(WorktreeOutput)
	if !ok {
		t.Fatalf("expected WorktreeOutput, got %T", result.Content)
	}
	if content.Path != "/repo/.worktrees/my-feature" {
		t.Errorf("expected path=/repo/.worktrees/my-feature, got %s", content.Path)
	}
	if content.Branch != "feat/my-feature" {
		t.Errorf("expected branch=feat/my-feature, got %s", content.Branch)
	}
	if content.Created {
		t.Error("expected created=false for reused worktree")
	}
}

func TestWorktreeBranchMismatch(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (worktree exists but on different branch)
			{stdout: porcelainOutput(
				struct{ path, sha, branch string }{"/repo", "aaa", "main"},
				struct{ path, sha, branch string }{"/repo/.worktrees/my-feature", "bbb", "fix/other-thing"},
			)},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/my-feature"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "fix/other-thing") {
		t.Errorf("expected error to mention existing branch, got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "feat/my-feature") {
		t.Errorf("expected error to mention expected branch, got: %s", result.Error.Message)
	}
}

func TestWorktreeDetachedHead(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (worktree exists but in detached HEAD — no branch line)
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n\nworktree /repo/.worktrees/my-feature\nHEAD abc123\n\n"},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/my-feature"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "detached HEAD state") {
		t.Errorf("expected 'detached HEAD state' error, got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "feat/my-feature") {
		t.Errorf("expected error to mention expected branch, got: %s", result.Error.Message)
	}
}

func TestWorktreeBranchFromInput(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse HEAD
			{stdout: "sha123\n"},
			// 4. rev-parse --verify refs/heads/feat/add-user-auth (doesn't exist)
			{err: fmt.Errorf("not found")},
			// 5. worktree add -b feat/add-user-auth /repo/.worktrees/add-user-auth HEAD
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")
	input.Content = "Add User Auth"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	content := result.Content.(WorktreeOutput)
	if content.Branch != "feat/add-user-auth" {
		t.Errorf("expected branch=feat/add-user-auth, got %s", content.Branch)
	}
	if content.Path != "/repo/.worktrees/add-user-auth" {
		t.Errorf("expected path to contain add-user-auth, got %s", content.Path)
	}
}

func TestWorktreeSlugify(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"simple words", "OAuth Login", "feat/oauth-login"},
		{"special chars", "fix: auth bug #123", "feat/fix-auth-bug-123"},
		{"consecutive hyphens", "hello---world", "feat/hello-world"},
		{"leading trailing special", "  --hello--  ", "feat/hello"},
		{"empty string", "", ""},
		{"only special chars", "!@#$%", ""},
		{"already has prefix", "fix/something", "fix/something"},
		{"uppercase mixed", "My GREAT Feature", "feat/my-great-feature"},
		{"numbers only", "123", "feat/123"},
		{"single word", "refactor", "feat/refactor"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := slugify(tc.input)
			if got != tc.expect {
				t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestWorktreeExistingBranch(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (no matching worktree)
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse HEAD
			{stdout: "abc999\n"},
			// 4. rev-parse --verify refs/heads/feat/existing (branch exists!)
			{stdout: "def888\n"},
			// 5. worktree add /repo/.worktrees/existing feat/existing (no -b flag)
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/existing"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	content := result.Content.(WorktreeOutput)
	if !content.Created {
		t.Error("expected created=true for new worktree on existing branch")
	}
	if content.Branch != "feat/existing" {
		t.Errorf("expected branch=feat/existing, got %s", content.Branch)
	}

	// Verify that worktree add was called WITHOUT -b
	if len(mock.recorded) < 5 {
		t.Fatalf("expected 5 git calls, got %d", len(mock.recorded))
	}
	addArgs := mock.recorded[4]
	for _, arg := range addArgs {
		if arg == "-b" {
			t.Error("expected worktree add without -b flag for existing branch")
		}
	}
}

func TestWorktreeGitError(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse HEAD
			{stdout: "sha111\n"},
			// 4. rev-parse --verify refs/heads/feat/new-thing (doesn't exist)
			{err: fmt.Errorf("not found")},
			// 5. worktree add fails
			{err: fmt.Errorf("exit status 128: fatal: '/repo/.worktrees/new-thing' already exists")},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/new-thing"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "git worktree add failed") {
		t.Errorf("expected worktree add error message, got: %s", result.Error.Message)
	}
}

func TestWorktreeEmptyInput(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")
	// No content and no branch flag

	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "no branch name") {
		t.Errorf("expected 'no branch name' error, got: %s", result.Error.Message)
	}
}

func TestWorktreeNotGitRepo(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel fails
			{err: fmt.Errorf("exit status 128: fatal: not a git repository")},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/something"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %s", result.Error.Message)
	}
}

func TestWorktreeExplicitPath(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel (path validation)
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain (no matching worktree)
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse HEAD
			{stdout: "sha999\n"},
			// 4. rev-parse --verify refs/heads/feat/custom (doesn't exist)
			{err: fmt.Errorf("not found")},
			// 5. worktree add -b feat/custom /repo/custom-wt HEAD
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{
		"branch": "feat/custom",
		"path":   "/repo/custom-wt",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	content := result.Content.(WorktreeOutput)
	if content.Path != "/repo/custom-wt" {
		t.Errorf("expected path=/repo/custom-wt, got %s", content.Path)
	}
}

func TestWorktreeExplicitPathOutsideRepo(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel (path validation)
			{stdout: "/repo\n"},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{
		"branch": "feat/custom",
		"path":   "/tmp/custom-wt",
	})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "worktree path must be within the repository directory tree") {
		t.Errorf("expected path validation error, got: %s", result.Error.Message)
	}
}

func TestWorktreeCustomBase(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse v1.0.0 (resolve custom base)
			{stdout: "tag-sha-123\n"},
			// 4. rev-parse --verify refs/heads/feat/from-tag (doesn't exist)
			{err: fmt.Errorf("not found")},
			// 5. worktree add -b feat/from-tag /repo/.worktrees/from-tag v1.0.0
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{
		"branch": "feat/from-tag",
		"base":   "v1.0.0",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	content := result.Content.(WorktreeOutput)
	if content.BaseCommit != "tag-sha-123" {
		t.Errorf("expected base_commit=tag-sha-123, got %s", content.BaseCommit)
	}

	// Verify the base ref was passed to worktree add
	addArgs := mock.recorded[4]
	if addArgs[len(addArgs)-1] != "v1.0.0" {
		t.Errorf("expected base ref v1.0.0 in worktree add args, got: %v", addArgs)
	}
}

func TestWorktreeBaseRefInvalid(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			// 1. rev-parse --show-toplevel
			{stdout: "/repo\n"},
			// 2. worktree list --porcelain
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			// 3. rev-parse nonexistent-ref fails
			{err: fmt.Errorf("exit status 128: fatal: bad revision 'nonexistent-ref'")},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{
		"branch": "feat/test",
		"base":   "nonexistent-ref",
	})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "cannot resolve base ref") {
		t.Errorf("expected 'cannot resolve base ref' error, got: %s", result.Error.Message)
	}
}

func TestWorktreeEnvelopeCompliance(t *testing.T) {
	mock := &mockGitExecutor{
		calls: []mockCall{
			{stdout: "/repo\n"},
			{stdout: "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"},
			{stdout: "sha123\n"},
			{err: fmt.Errorf("not found")},
			{stdout: ""},
		},
	}

	handler := NewHandler(mock, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"branch": "feat/test"})

	testutil.AssertEnvelope(t, result, "worktree", "create")
	if result.Args == nil {
		t.Error("expected args to be non-nil")
	}
	if result.Content == nil {
		t.Error("expected content to be non-nil")
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
}
