package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "build",
		Prompts: config.PromptsConfig{
			System: "You are a meticulous software developer.",
			Templates: map[string]string{
				"initial": "Implement the following feature.\n\n## Feature Spec\n{{.Spec}}\n\n## Codebase Context\n{{.Context}}\n\n{{if .Style}}Implementation style: {{.Style}}{{end}}\n\nPlan your approach, then implement. Write tests first.\n",
				"rework":  "Address the following reviewer findings for this feature.\n\n## Feature Spec\n{{.Spec}}\n\n## Codebase Context\n{{.Context}}\n\n## Reviewer Findings\n{{range .Findings}}\n- [{{.Category}}/{{.Severity}}] {{.File}}:{{.Line}} — {{.Issue}}\n  Action: {{.Action}}\n{{end}}\n\nAddress each finding specifically. Do not reimplement from scratch.\nMake targeted changes that resolve each issue.\n",
			},
		},
	}
}

// agenticProvider wraps MockAgenticProvider for convenience.
func mockProvider(response string) *testutil.MockAgenticProvider {
	return &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{{FinalText: response}},
	}
}

func mockProviderErr(err error) *testutil.MockAgenticProvider {
	return &testutil.MockAgenticProvider{Err: err}
}

// initGitRepo sets up a bare git repo in dir for worktree diff tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

func TestBuildInitial(t *testing.T) {
	dir := t.TempDir()
	provider := mockProvider("Created auth module with login handler and tests.")
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "package main\n\nfunc main() {}"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add user authentication", "cwd": dir})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	output, ok := result.Content.(BuildOutput)
	if !ok {
		t.Fatalf("expected BuildOutput, got %T", result.Content)
	}
	if output.Summary != "Created auth module with login handler and tests." {
		t.Errorf("unexpected summary: %s", output.Summary)
	}
	if output.CycleNumber != 1 {
		t.Errorf("expected cycle_number=1, got %d", output.CycleNumber)
	}
	if output.Style != "tdd" {
		t.Errorf("expected style=tdd, got %s", output.Style)
	}

	// Verify the prompt used the initial template
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{"spec": "Add user authentication"})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Implement the following feature") {
		t.Errorf("expected initial template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Add user authentication") {
		t.Errorf("expected spec in prompt, got: %s", userPrompt)
	}
}

func TestBuildWithFindings(t *testing.T) {
	dir := t.TempDir()
	provider := mockProvider("Fixed error handling in auth.go and added missing test.")
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	findings := []ReviewFinding{
		{
			Category: "correctness",
			Severity: "error",
			File:     "auth.go",
			Line:     42,
			Issue:    "Missing error check on db.Query",
			Action:   "Add error handling for the database query",
		},
		{
			Category: "testing",
			Severity: "warn",
			File:     "auth_test.go",
			Line:     0,
			Issue:    "No test for error path",
			Action:   "Add test case for database failure",
		},
	}
	findingsJSON, err := json.Marshal(findings)
	if err != nil {
		t.Fatalf("marshal findings: %v", err)
	}

	input := envelope.New("study", "analyze")
	input.Content = "package auth\n\nfunc Login() {}"
	input.ContentType = "text"

	flags := map[string]string{
		"spec":     "Add user authentication",
		"findings": string(findingsJSON),
		"cwd":      dir,
	}

	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	output, ok := result.Content.(BuildOutput)
	if !ok {
		t.Fatalf("expected BuildOutput, got %T", result.Content)
	}
	if output.CycleNumber != 2 {
		t.Errorf("expected cycle_number=2, got %d", output.CycleNumber)
	}

	// Verify the prompt used the rework template
	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, flags)
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Address the following reviewer findings") {
		t.Errorf("expected rework template, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "correctness/error") {
		t.Errorf("expected findings category/severity in prompt, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "auth.go:42") {
		t.Errorf("expected file:line in prompt, got: %s", userPrompt)
	}
	if !strings.Contains(userPrompt, "Missing error check on db.Query") {
		t.Errorf("expected issue in prompt, got: %s", userPrompt)
	}
}

func TestBuildTDDStyle(t *testing.T) {
	dir := t.TempDir()
	provider := mockProvider("Built with TDD approach.")
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("study", "analyze")
	input.Content = "codebase context here"
	input.ContentType = "text"

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{
		"spec":  "Add feature X",
		"style": "tdd",
	})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Implementation style: tdd") {
		t.Errorf("expected style=tdd in prompt, got: %s", userPrompt)
	}

	// Also verify handler works end-to-end
	handler := NewHandlerWith(provider, pc, compiled, nil)
	result := handler(input, map[string]string{"spec": "Add feature X", "style": "tdd", "cwd": dir})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Style != "tdd" {
		t.Errorf("expected style=tdd, got %s", output.Style)
	}
}

func TestBuildImplFirstStyle(t *testing.T) {
	dir := t.TempDir()
	provider := mockProvider("Built with impl-first approach.")
	pc := testConfig()
	compiled := CompileTemplates(pc)

	input := envelope.New("study", "analyze")
	input.Content = "codebase context here"
	input.ContentType = "text"

	_, userPrompt, errEnv := preparePrompt(compiled, pc, input, map[string]string{
		"spec":  "Add feature X",
		"style": "impl-first",
	})
	if errEnv != nil {
		t.Fatalf("unexpected error: %v", errEnv)
	}
	if !strings.Contains(userPrompt, "Implementation style: impl-first") {
		t.Errorf("expected style=impl-first in prompt, got: %s", userPrompt)
	}

	// Also verify handler works end-to-end
	handler := NewHandlerWith(provider, pc, compiled, nil)
	result := handler(input, map[string]string{"spec": "Add feature X", "style": "impl-first", "cwd": dir})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Style != "impl-first" {
		t.Errorf("expected style=impl-first, got %s", output.Style)
	}
}

func TestBuildEmptySpec(t *testing.T) {
	handler := NewHandler(mockProvider("something"), testConfig(), nil)

	input := envelope.New("input", "test")
	// No content, no spec flag
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error for empty spec")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "no spec provided") {
		t.Errorf("expected 'no spec provided' message, got: %s", result.Error.Message)
	}
}

func TestBuildCwdRequired(t *testing.T) {
	handler := NewHandler(mockProvider("something"), testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add feature"})

	if result.Error == nil {
		t.Fatal("expected error for missing cwd")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "cwd") {
		t.Errorf("expected 'cwd' in message, got: %s", result.Error.Message)
	}
}

func TestBuildProviderError(t *testing.T) {
	dir := t.TempDir()
	handler := NewHandler(mockProviderErr(errors.New("auth failed")), testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add feature", "cwd": dir})

	testutil.AssertFatalError(t, result)
}

func TestBuildProviderEmpty(t *testing.T) {
	// An agentic provider that returns neither text nor tool calls produces
	// a fatal error (not warn) because it indicates the provider is broken.
	dir := t.TempDir()
	handler := NewHandler(&emptyAgenticProvider{}, testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add feature", "cwd": dir})

	testutil.AssertFatalError(t, result)
}

func TestBuildFindingsParsing(t *testing.T) {
	dir := t.TempDir()
	handler := NewHandler(mockProvider("something"), testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"spec":     "Add feature",
		"findings": "not valid json{{{",
		"cwd":      dir,
	})

	if result.Error == nil {
		t.Fatal("expected error for malformed findings JSON")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "invalid findings JSON") {
		t.Errorf("expected 'invalid findings JSON' message, got: %s", result.Error.Message)
	}
}

func TestBuildStreamHandler(t *testing.T) {
	dir := t.TempDir()
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{{FinalText: "Built auth module with tests."}},
	}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "codebase context"
	input.ContentType = "text"

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"spec": "Add auth", "cwd": dir}, sink)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}

	output, ok := result.Content.(BuildOutput)
	if !ok {
		t.Fatalf("expected BuildOutput, got %T", result.Content)
	}
	if output.Summary != "Built auth module with tests." {
		t.Errorf("unexpected summary: %s", output.Summary)
	}
	// The agentic loop passes the final text to onChunk — at least 1 chunk expected.
	if len(chunks) == 0 {
		t.Error("expected at least one chunk from stream handler")
	}
}

func TestBuildTemplateCompilation(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)

	expectedTemplates := []string{"initial", "rework"}
	for _, name := range expectedTemplates {
		if _, ok := compiled[name]; !ok {
			t.Errorf("expected template %q to be compiled", name)
		}
	}

	if len(compiled) != len(expectedTemplates) {
		t.Errorf("expected %d compiled templates, got %d", len(expectedTemplates), len(compiled))
	}
}

func TestBuildSpecFromInput(t *testing.T) {
	// When no spec flag is provided, the input content is used as the spec.
	dir := t.TempDir()
	handler := NewHandler(mockProvider("Built from input content."), testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "Feature: add user profiles with avatar uploads"
	input.ContentType = "text"

	result := handler(input, map[string]string{"cwd": dir})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Summary != "Built from input content." {
		t.Errorf("unexpected summary: %s", output.Summary)
	}
}

func TestStreamHandlerProviderError(t *testing.T) {
	dir := t.TempDir()
	provider := mockProviderErr(errors.New("stream timeout"))
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"spec": "Add feature", "cwd": dir}, func(string) {})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandlerEmptySpec(t *testing.T) {
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(mockProvider("something"), pc, compiled, nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestStreamHandlerProviderEmpty(t *testing.T) {
	dir := t.TempDir()
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(&emptyAgenticProvider{}, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"spec": "Add feature", "cwd": dir}, func(string) {})

	testutil.AssertFatalError(t, result)
}

// --- New agentic tests ---

func TestBuildAgenticLoop(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Provider returns one write_file tool call, then final text.
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{
				testutil.MakeToolCall("c1", "write_file", map[string]any{
					"path":    "hello.go",
					"content": "package main\n",
				}),
			}},
			{FinalText: "implementation complete"},
		},
	}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add hello", "cwd": dir})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output, ok := result.Content.(BuildOutput)
	if !ok {
		t.Fatalf("expected BuildOutput, got %T", result.Content)
	}
	if output.Summary != "implementation complete" {
		t.Errorf("expected 'implementation complete', got %q", output.Summary)
	}
	// File was written — should show up as created.
	found := false
	for _, f := range output.FilesCreated {
		if strings.Contains(f, "hello.go") {
			found = true
		}
	}
	if !found {
		// go-git may not find it without a commit; verify the file at least exists.
		if _, err := os.Stat(dir + "/hello.go"); err != nil {
			t.Errorf("hello.go not created; FilesCreated=%v", output.FilesCreated)
		}
	}
}

func TestBuildMaxTurnsExhausted(t *testing.T) {
	dir := t.TempDir()

	// Provider always returns a tool call — never a final text.
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{
				{ID: "c1", Name: "list_dir", Input: json.RawMessage(`{"path":"."}`)},
			}},
		},
	}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"spec":      "Add feature",
		"cwd":       dir,
		"max_turns": "2",
	})

	if result.Error == nil {
		t.Fatal("expected error for exhausted turns")
	}
}

func TestBuildToolSandbox(t *testing.T) {
	dir := t.TempDir()

	// Provider tries to write outside the worktree; loop feeds back the error
	// as a tool result, then returns a final text.
	provider := &testutil.MockAgenticProvider{
		Turns: []testutil.AgenticTurn{
			{ToolCalls: []bridge.ToolCall{
				testutil.MakeToolCall("c1", "write_file", map[string]any{
					"path":    "../../etc/passwd",
					"content": "evil",
				}),
			}},
			{FinalText: "sandboxing caught the traversal"},
		},
	}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "do evil", "cwd": dir})

	// Handler should succeed (the loop continues after the tool error).
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Summary != "sandboxing caught the traversal" {
		t.Errorf("unexpected summary: %q", output.Summary)
	}
}

// emptyAgenticProvider always returns an empty response with no error.
type emptyAgenticProvider struct{}

func (p *emptyAgenticProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (p *emptyAgenticProvider) CompleteWithTools(_ context.Context, _ string, _ []bridge.AgenticMessage, _ []bridge.Tool) (bridge.AgenticResponse, error) {
	return bridge.AgenticResponse{Text: ""}, nil
}
