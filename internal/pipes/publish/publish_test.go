package publish

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// mockResponse defines a preset response for a command pattern.
type mockResponse struct {
	pattern  string
	stdout   string
	stderr   string
	exitCode int
	err      error
}

// mockExecutor records executed commands and returns responses based on
// pattern matching. Commands are matched in order; first match wins.
type mockExecutor struct {
	responses []mockResponse
	commands  []string
}

func (m *mockExecutor) Execute(_ context.Context, cmd string, _ string) (string, string, int, error) {
	m.commands = append(m.commands, cmd)
	for _, r := range m.responses {
		if strings.Contains(cmd, r.pattern) {
			return r.stdout, r.stderr, r.exitCode, r.err
		}
	}
	// Default: success with empty output
	return "", "", 0, nil
}

func (m *mockExecutor) assertCommandOrder(t *testing.T, patterns ...string) {
	t.Helper()
	idx := 0
	for _, cmd := range m.commands {
		if idx < len(patterns) && strings.Contains(cmd, patterns[idx]) {
			idx++
		}
	}
	if idx != len(patterns) {
		t.Errorf("expected command order %v, but only matched %d/%d\nactual commands: %v",
			patterns, idx, len(patterns), m.commands)
	}
}

func (m *mockExecutor) assertCommandContains(t *testing.T, substr string) {
	t.Helper()
	for _, cmd := range m.commands {
		if strings.Contains(cmd, substr) {
			return
		}
	}
	t.Errorf("no command contained %q\nactual commands: %v", substr, m.commands)
}

func (m *mockExecutor) assertNoCommandContains(t *testing.T, substr string) {
	t.Helper()
	for _, cmd := range m.commands {
		if strings.Contains(cmd, substr) {
			t.Errorf("unexpected command containing %q: %s", substr, cmd)
			return
		}
	}
}

// defaultResponses returns mock responses for a typical happy-path create-PR flow.
func defaultResponses(branch string) []mockResponse {
	return []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " file.go | 10 ++++++++++\n 1 file changed, 10 insertions(+)\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s abc1234] feat: update\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "gh version 2.40.0\n", exitCode: 0},
		{pattern: "git ls-remote --heads origin", stdout: "", exitCode: 1},
		{pattern: "git push", stdout: "", exitCode: 0},
		{pattern: "gh pr list", stdout: "[]", exitCode: 0},
		{pattern: "gh pr create", stdout: "https://github.com/owner/repo/pull/42\n", exitCode: 0},
	}
}

func TestPublishCreatePR(t *testing.T) {
	branch := "feat/my-feature"
	executor := &mockExecutor{responses: defaultResponses(branch)}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")
	input.Content = map[string]any{"summary": "add login page"}

	result := handler(input, map[string]string{"base": "main"})

	testutil.AssertEnvelope(t, result, "publish", "publish")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	output, ok := result.Content.(PublishOutput)
	if !ok {
		t.Fatalf("expected PublishOutput, got %T", result.Content)
	}
	if output.PRURL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("unexpected PR URL: %s", output.PRURL)
	}
	if output.PRNumber != 42 {
		t.Errorf("unexpected PR number: %d", output.PRNumber)
	}
	if output.CommitSHA != "abc1234" {
		t.Errorf("unexpected commit SHA: %s", output.CommitSHA)
	}
	if output.Branch != branch {
		t.Errorf("unexpected branch: %s", output.Branch)
	}
	if !output.Created {
		t.Error("expected Created=true")
	}
	if output.DiffSummary == "" {
		t.Error("expected non-empty diff summary")
	}

	// Verify command order: gh --version must come before git push
	executor.assertCommandOrder(t,
		"git add -A",
		"git diff --cached --stat",
		"git commit",
		"git rev-parse",
		"gh --version",
		"git push",
		"gh pr list",
		"gh pr create",
	)

	// First push should use -u
	executor.assertCommandContains(t, "git push -u origin")
}

func TestPublishUpdatePR(t *testing.T) {
	branch := "feat/update-feature"
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " main.go | 5 +++--\n 1 file changed\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s def5678] feat: update\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "gh version 2.40.0\n", exitCode: 0},
		{pattern: "git ls-remote --heads origin", stdout: "abc123\trefs/heads/" + branch + "\n", exitCode: 0},
		{pattern: "git push", stdout: "", exitCode: 0},
		{pattern: "gh pr list", stdout: `[{"number":17,"url":"https://github.com/owner/repo/pull/17"}]`, exitCode: 0},
		{pattern: "gh pr edit", stdout: "", exitCode: 0},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")
	input.Content = "fix tests"

	result := handler(input, map[string]string{"base": "main"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	output, ok := result.Content.(PublishOutput)
	if !ok {
		t.Fatalf("expected PublishOutput, got %T", result.Content)
	}
	if output.Created {
		t.Error("expected Created=false for update")
	}
	if output.PRNumber != 17 {
		t.Errorf("expected PR number 17, got %d", output.PRNumber)
	}
	if output.PRURL != "https://github.com/owner/repo/pull/17" {
		t.Errorf("unexpected PR URL: %s", output.PRURL)
	}

	// Should use force-push for existing remote branch
	executor.assertCommandContains(t, "git push --force-with-lease origin")

	// Should edit, not create
	executor.assertCommandContains(t, "gh pr edit 17")
	executor.assertNoCommandContains(t, "gh pr create")
}

func TestPublishDraft(t *testing.T) {
	branch := "feat/draft-pr"
	executor := &mockExecutor{responses: defaultResponses(branch)}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")
	input.Content = "draft feature"

	result := handler(input, map[string]string{"draft": "true", "base": "main"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	executor.assertCommandContains(t, "--draft")
}

func TestPublishNoChanges(t *testing.T) {
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: "", exitCode: 0},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected warning error")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
	if result.Error.Message != "nothing to publish" {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}

	// Should not attempt commit, push, or PR
	executor.assertNoCommandContains(t, "git commit")
	executor.assertNoCommandContains(t, "git push")
	executor.assertNoCommandContains(t, "gh pr")
}

func TestPublishPushFails(t *testing.T) {
	branch := "feat/push-fail"
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " file.go | 1 +\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s aaa1111] feat: update\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "gh version 2.40.0\n", exitCode: 0},
		{pattern: "git ls-remote --heads origin", stdout: "", exitCode: 1},
		{pattern: "git push", stdout: "", stderr: "fatal: could not read from remote repository", exitCode: 128},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for push failure")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true for push failure")
	}
	if result.Error.Severity != envelope.SeverityError {
		t.Errorf("expected severity=error, got %s", result.Error.Severity)
	}
}

func TestPublishGHNotInstalled(t *testing.T) {
	branch := "feat/no-gh"
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " file.go | 1 +\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s bbb2222] feat: update\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "", stderr: "", exitCode: 0, err: fmt.Errorf("exec: \"gh\": executable file not found in $PATH")},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected fatal error for missing gh")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}

	// gh check happens before push, so push should NOT be executed
	executor.assertNoCommandContains(t, "git push")
}

func TestPublishForceWithLease(t *testing.T) {
	branch := "feat/force-lease"
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " file.go | 1 +\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s ccc3333] feat: update\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "gh version 2.40.0\n", exitCode: 0},
		// Remote branch exists
		{pattern: "git ls-remote --heads origin", stdout: "abc123\trefs/heads/" + branch + "\n", exitCode: 0},
		{pattern: "git push", stdout: "", exitCode: 0},
		{pattern: "gh pr list", stdout: "[]", exitCode: 0},
		{pattern: "gh pr create", stdout: "https://github.com/owner/repo/pull/99\n", exitCode: 0},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{"update-strategy": "force-push"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Verify --force-with-lease is used, NOT --force
	executor.assertCommandContains(t, "--force-with-lease")
	for _, cmd := range executor.commands {
		if strings.Contains(cmd, "git push") && strings.Contains(cmd, "--force") && !strings.Contains(cmd, "--force-with-lease") {
			t.Errorf("expected --force-with-lease, got bare --force in: %s", cmd)
		}
	}
}

func TestPublishCommitMessage(t *testing.T) {
	branch := "feat/commit-msg"
	responses := []mockResponse{
		{pattern: "git add -A", stdout: "", exitCode: 0},
		{pattern: "git diff --cached --stat", stdout: " file.go | 1 +\n", exitCode: 0},
		{pattern: "git commit", stdout: fmt.Sprintf("[%s ddd4444] feat: implement auth\n", branch), exitCode: 0},
		{pattern: "git rev-parse --abbrev-ref HEAD", stdout: branch + "\n", exitCode: 0},
		{pattern: "gh --version", stdout: "gh version 2.40.0\n", exitCode: 0},
		{pattern: "git ls-remote --heads origin", stdout: "", exitCode: 1},
		{pattern: "git push", stdout: "", exitCode: 0},
		{pattern: "gh pr list", stdout: "[]", exitCode: 0},
		{pattern: "gh pr create", stdout: "https://github.com/owner/repo/pull/1\n", exitCode: 0},
	}
	executor := &mockExecutor{responses: responses}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")
	input.Content = map[string]any{"summary": "implement auth"}

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Find the git commit command and verify format
	found := false
	for _, cmd := range executor.commands {
		if strings.Contains(cmd, "git commit") {
			if !strings.Contains(cmd, "feat: implement auth") {
				t.Errorf("commit message should contain 'feat: implement auth', got: %s", cmd)
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("no git commit command found")
	}
}

func TestPublishCwdFromFlags(t *testing.T) {
	branch := "feat/cwd-flag"
	cwd := t.TempDir()
	executor := &mockExecutor{responses: defaultResponses(branch)}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{"cwd": cwd})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	output, ok := result.Content.(PublishOutput)
	if !ok {
		t.Fatalf("expected PublishOutput, got %T", result.Content)
	}
	if output.Branch != branch {
		t.Errorf("unexpected branch: %s", output.Branch)
	}
}

func TestPublishCwdFromEnvelope(t *testing.T) {
	branch := "feat/cwd-envelope"
	cwd := t.TempDir()

	executor := &mockExecutor{responses: defaultResponses(branch)}

	handler := NewHandler(executor, nil)
	input := envelope.New("worktree", "create")
	input.Content = map[string]any{"worktree_path": cwd}

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	output, ok := result.Content.(PublishOutput)
	if !ok {
		t.Fatalf("expected PublishOutput, got %T", result.Content)
	}
	if output.Branch != branch {
		t.Errorf("unexpected branch: %s", output.Branch)
	}
}

func TestPublishInvalidCwd(t *testing.T) {
	executor := &mockExecutor{}

	handler := NewHandler(executor, nil)
	input := envelope.New("verify", "verify")

	result := handler(input, map[string]string{"cwd": "/nonexistent/dir/path"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "invalid working directory") {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}
}

func TestExtractCommitSHA(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"[feat/x abc1234] some message", "abc1234"},
		{"[main (root-commit) def5678] initial", "def5678"},
		{"abc1234def something", "abc1234def"},
		{"no sha here", ""},
	}

	for _, tt := range tests {
		got := extractCommitSHA(tt.input)
		if got != tt.expected {
			t.Errorf("extractCommitSHA(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractSummary(t *testing.T) {
	tests := []struct {
		name     string
		content  any
		expected string
	}{
		{"nil content", nil, "update"},
		{"string content", "add login page", "add login page"},
		{"map with summary", map[string]any{"summary": "fix bug"}, "fix bug"},
		{"map with message", map[string]any{"message": "refactor code"}, "refactor code"},
		{"empty string", "", "update"},
		{"long string", strings.Repeat("a", 100), strings.Repeat("a", 72)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := envelope.New("test", "test")
			env.Content = tt.content
			got := extractSummary(env)
			if got != tt.expected {
				t.Errorf("extractSummary() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParsePRList(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"[]", 0},
		{"", 0},
		{`[{"number":42,"url":"https://github.com/owner/repo/pull/42"}]`, 1},
		{`[{"number":1,"url":"u1"},{"number":2,"url":"u2"}]`, 2},
		{"invalid json", 0},
	}

	for _, tt := range tests {
		got := parsePRList(tt.input)
		if len(got) != tt.expected {
			t.Errorf("parsePRList(%q) returned %d PRs, want %d", tt.input, len(got), tt.expected)
		}
	}
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		url      string
		expected int
	}{
		{"https://github.com/owner/repo/pull/42", 42},
		{"https://github.com/owner/repo/pull/1", 1},
		{"no-number", 0},
	}

	for _, tt := range tests {
		got := extractPRNumber(tt.url)
		if got != tt.expected {
			t.Errorf("extractPRNumber(%q) = %d, want %d", tt.url, got, tt.expected)
		}
	}
}
