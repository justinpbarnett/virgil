package build

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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

func TestBuildInitial(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Created auth module with login handler and tests."}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "package main\n\nfunc main() {}"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add user authentication"})

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
	provider := &testutil.MockProvider{Response: "Fixed error handling in auth.go and added missing test."}
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
	provider := &testutil.MockProvider{Response: "Built with TDD approach."}
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
	result := handler(input, map[string]string{"spec": "Add feature X", "style": "tdd"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Style != "tdd" {
		t.Errorf("expected style=tdd, got %s", output.Style)
	}
}

func TestBuildImplFirstStyle(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Built with impl-first approach."}
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
	result := handler(input, map[string]string{"spec": "Add feature X", "style": "impl-first"})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Style != "impl-first" {
		t.Errorf("expected style=impl-first, got %s", output.Style)
	}
}

func TestBuildEmptySpec(t *testing.T) {
	provider := &testutil.MockProvider{Response: "something"}
	handler := NewHandler(provider, testConfig(), nil)

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

func TestBuildProviderError(t *testing.T) {
	provider := &testutil.MockProvider{Err: errors.New("auth failed")}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add feature"})

	testutil.AssertFatalError(t, result)
}

func TestBuildProviderEmpty(t *testing.T) {
	provider := &emptyProvider{}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{"spec": "Add feature"})

	if result.Error == nil {
		t.Fatal("expected warn error for empty provider response")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
	if result.Error.Retryable {
		t.Error("expected retryable=false")
	}
}

func TestBuildFindingsParsing(t *testing.T) {
	provider := &testutil.MockProvider{Response: "something"}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(input, map[string]string{
		"spec":     "Add feature",
		"findings": "not valid json{{{",
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
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "Built auth module with tests."},
		Chunks:       []string{"Building ", "auth module ", "with tests."},
	}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "codebase context"
	input.ContentType = "text"

	var chunks []string
	sink := func(chunk string) { chunks = append(chunks, chunk) }

	result := handler(context.Background(), input, map[string]string{"spec": "Add auth"}, sink)

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
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
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
	provider := &testutil.MockProvider{Response: "Built from input content."}
	handler := NewHandler(provider, testConfig(), nil)

	input := envelope.New("study", "analyze")
	input.Content = "Feature: add user profiles with avatar uploads"
	input.ContentType = "text"

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	output := result.Content.(BuildOutput)
	if output.Summary != "Built from input content." {
		t.Errorf("unexpected summary: %s", output.Summary)
	}
}

func TestStreamHandlerProviderError(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Err: errors.New("stream timeout")},
	}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"spec": "Add feature"}, func(string) {})

	testutil.AssertFatalError(t, result)
}

func TestStreamHandlerEmptySpec(t *testing.T) {
	provider := &testutil.MockStreamProvider{
		MockProvider: testutil.MockProvider{Response: "something"},
	}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("input", "test")
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestStreamHandlerProviderEmpty(t *testing.T) {
	provider := &emptyProvider{}
	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewStreamHandlerWith(provider, pc, compiled, nil)

	input := envelope.New("study", "analyze")
	input.Content = "context"
	input.ContentType = "text"

	result := handler(context.Background(), input, map[string]string{"spec": "Add feature"}, func(string) {})

	if result.Error == nil {
		t.Fatal("expected warn error for empty provider response")
	}
	if result.Error.Severity != envelope.SeverityWarn {
		t.Errorf("expected severity=warn, got %s", result.Error.Severity)
	}
}

// emptyProvider always returns an empty string with no error.
type emptyProvider struct{}

func (p *emptyProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (p *emptyProvider) CompleteStream(_ context.Context, _, _ string, _ func(string)) (string, error) {
	return "", nil
}
