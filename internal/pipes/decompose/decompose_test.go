package decompose

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

// mockProvider is a bridge.Provider that returns canned responses.
type mockProvider struct {
	response string
	err      error
	// lastSystem and lastUser capture the prompts passed to Complete for inspection.
	lastSystem string
	lastUser   string
}

func (m *mockProvider) Complete(_ context.Context, system, user string) (string, error) {
	m.lastSystem = system
	m.lastUser = user
	return m.response, m.err
}

// mustJSON marshals v to a JSON string, panicking on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// makePipeConfig builds a minimal PipeConfig with compiled templates for testing.
func makePipeConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "decompose",
		Prompts: config.PromptsConfig{
			System: "You are a software architect.",
			Templates: map[string]string{
				"initial": "Decompose the following feature into sub-tasks.\n\n## Feature Spec\n{{.Spec}}\n\n## Codebase Context\n{{.Context}}\n\nMaximum {{.MaxTasks}} tasks.",
				"rework":  "The following reviewer findings need to be addressed.\n\n## Original Feature Spec\n{{.Spec}}\n\n## Codebase Context\n{{.Context}}\n\n## Reviewer Findings\n{{range .Findings}}\n- [{{.Category}}/{{.Severity}}] {{.File}}:{{.Line}} — {{.Issue}}\n  Action: {{.Action}}\n{{end}}\n\nMaximum {{.MaxTasks}} tasks.",
			},
		},
	}
}

func runHandler(provider *mockProvider, flags map[string]string, inputContent string) envelope.Envelope {
	pc := makePipeConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, nil)
	input := envelope.Envelope{
		Content:     inputContent,
		ContentType: envelope.ContentText,
	}
	return handler(input, flags)
}

// --- Valid DAG tests ---

func TestValidDAGLinearChain(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "types", Spec: "Define types", Files: []string{"types.go"}, DependsOn: []string{}},
		{ID: "t2", Name: "impl", Spec: "Implement", Files: []string{"impl.go"}, DependsOn: []string{"t1"}},
		{ID: "t3", Name: "wire", Spec: "Wire up", Files: []string{"wire.go"}, DependsOn: []string{"t2"}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %s", result.Error.Message)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Fatalf("expected ContentStructured, got %q", result.ContentType)
	}

	b, _ := json.Marshal(result.Content)
	var got DecomposeOutput
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if len(got.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(got.Tasks))
	}
	if got.Tasks[0].ID != "t1" || got.Tasks[1].ID != "t2" || got.Tasks[2].ID != "t3" {
		t.Errorf("unexpected task IDs: %v", got.Tasks)
	}
}

func TestValidDAGDiamond(t *testing.T) {
	// t1 -> t2, t1 -> t3, t2+t3 -> t4 (diamond)
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "root", Spec: "Root task", Files: []string{"root.go"}, DependsOn: []string{}},
		{ID: "t2", Name: "left", Spec: "Left branch", Files: []string{"left.go"}, DependsOn: []string{"t1"}},
		{ID: "t3", Name: "right", Spec: "Right branch", Files: []string{"right.go"}, DependsOn: []string{"t1"}},
		{ID: "t4", Name: "merge", Spec: "Merge", Files: []string{"merge.go"}, DependsOn: []string{"t2", "t3"}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %s", result.Error.Message)
	}
	b, _ := json.Marshal(result.Content)
	var got DecomposeOutput
	json.Unmarshal(b, &got)
	if len(got.Tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(got.Tasks))
	}
}

func TestValidDAGSingleTask(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "single", Spec: "Do everything", Files: []string{"main.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "tiny feature"}, "")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %s", result.Error.Message)
	}
}

// --- Validation failure tests ---

func TestCyclicDAGRejection(t *testing.T) {
	// t1 -> t2 -> t3 -> t1
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{"t3"}},
		{ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t1"}},
		{ID: "t3", Name: "c", Spec: "do c", Files: []string{"c.go"}, DependsOn: []string{"t2"}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for cyclic DAG, got none")
	}
	if !strings.Contains(result.Error.Message, "cycle") {
		t.Errorf("expected 'cycle' in error, got: %s", result.Error.Message)
	}
}

func TestFileOverlapSameLevel(t *testing.T) {
	// t1 and t2 both at level 0, both touching bar.go
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"foo.go", "bar.go"}, DependsOn: []string{}},
		{ID: "t2", Name: "b", Spec: "do b", Files: []string{"bar.go", "baz.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for file overlap, got none")
	}
	if !strings.Contains(result.Error.Message, "bar.go") {
		t.Errorf("expected 'bar.go' in error, got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "level 0") {
		t.Errorf("expected 'level 0' in error, got: %s", result.Error.Message)
	}
}

func TestFileOverlapAllowedDifferentLevels(t *testing.T) {
	// t1 uses foo.go at level 0; t2 depends on t1 and also uses foo.go at level 1 — allowed
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"foo.go"}, DependsOn: []string{}},
		{ID: "t2", Name: "b", Spec: "do b", Files: []string{"foo.go", "bar.go"}, DependsOn: []string{"t1"}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected no error for different-level overlap, got: %s", result.Error.Message)
	}
}

func TestMaxTasksExceeded(t *testing.T) {
	tasks := DecomposeOutput{Tasks: make([]Task, 10)}
	for i := range tasks.Tasks {
		tasks.Tasks[i] = Task{
			ID:        fmt.Sprintf("t%d", i+1),
			Name:      fmt.Sprintf("task%d", i+1),
			Spec:      "do something",
			Files:     []string{fmt.Sprintf("file%d.go", i+1)},
			DependsOn: []string{},
		}
	}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec", "max_tasks": "5"}, "")

	if result.Error == nil {
		t.Fatal("expected error for max_tasks exceeded, got none")
	}
	if !strings.Contains(result.Error.Message, "exceeds max_tasks") {
		t.Errorf("expected 'exceeds max_tasks' in error, got: %s", result.Error.Message)
	}
}

func TestMissingRequiredFieldSpec(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "", Files: []string{"a.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for missing spec field, got none")
	}
	if !strings.Contains(result.Error.Message, "missing required field") {
		t.Errorf("expected 'missing required field' in error, got: %s", result.Error.Message)
	}
}

func TestMissingRequiredFieldFiles(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for empty files, got none")
	}
	if !strings.Contains(result.Error.Message, "missing required field") {
		t.Errorf("expected 'missing required field' in error, got: %s", result.Error.Message)
	}
}

func TestDuplicateTaskIDs(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
		{ID: "t1", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for duplicate IDs, got none")
	}
	if !strings.Contains(result.Error.Message, "duplicate task ID") {
		t.Errorf("expected 'duplicate task ID' in error, got: %s", result.Error.Message)
	}
}

func TestUnknownDependencyReference(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t99"}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for unknown dependency, got none")
	}
	if !strings.Contains(result.Error.Message, "unknown task") {
		t.Errorf("expected 'unknown task' in error, got: %s", result.Error.Message)
	}
}

// --- Rework mode tests ---

func TestReworkModeFindings(t *testing.T) {
	findings := []ReviewFinding{
		{Category: "correctness", Severity: "high", File: "auth.go", Line: 42, Issue: "unvalidated token", Action: "validate before access"},
	}
	findingsJSON := mustJSON(findings)

	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "r1", Name: "fix-token", Spec: "Fix token validation at auth.go:42", Files: []string{"auth.go", "auth_test.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec", "findings": findingsJSON}, "")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %s", result.Error.Message)
	}

	// Verify rework template was used (prompt should contain "Reviewer Findings")
	if !strings.Contains(provider.lastUser, "Reviewer Findings") {
		t.Errorf("expected rework template to be used; user prompt did not contain 'Reviewer Findings'")
	}
}

func TestNoFindingsUsesInitialTemplate(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "impl", Spec: "Implement feature", Files: []string{"feature.go"}, DependsOn: []string{}},
	}}
	provider := &mockProvider{response: mustJSON(tasks)}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %s", result.Error.Message)
	}
	if !strings.Contains(provider.lastUser, "Decompose the following feature") {
		t.Errorf("expected initial template; prompt did not contain 'Decompose the following feature'")
	}
}

// --- Empty spec ---

func TestEmptySpecFatalError(t *testing.T) {
	provider := &mockProvider{response: "{}"}
	result := runHandler(provider, map[string]string{"spec": ""}, "")

	if result.Error == nil {
		t.Fatal("expected fatal error for empty spec, got none")
	}
	if !strings.Contains(result.Error.Message, "no spec provided for decompose") {
		t.Errorf("unexpected error message: %s", result.Error.Message)
	}
}

// --- JSON extraction tests ---

func TestJSONExtractionMarkdownFences(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "impl", Spec: "Implement", Files: []string{"impl.go"}, DependsOn: []string{}},
	}}
	fenced := "```json\n" + mustJSON(tasks) + "\n```"
	provider := &mockProvider{response: fenced}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected JSON extracted from fences, got error: %s", result.Error.Message)
	}
}

func TestJSONExtractionPrefixedText(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "impl", Spec: "Implement", Files: []string{"impl.go"}, DependsOn: []string{}},
	}}
	prefixed := "Here is the task graph:\n" + mustJSON(tasks)
	provider := &mockProvider{response: prefixed}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error != nil {
		t.Fatalf("expected JSON extracted from prefixed text, got error: %s", result.Error.Message)
	}
}

func TestJSONExtractionTotalFailure(t *testing.T) {
	provider := &mockProvider{response: "I don't understand the request."}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error for non-JSON response, got none")
	}
	if !strings.Contains(result.Error.Message, "failed to extract task graph") {
		t.Errorf("expected 'failed to extract task graph' in error, got: %s", result.Error.Message)
	}
}

// --- Provider error ---

func TestProviderErrorPropagation(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("connection refused")}
	result := runHandler(provider, map[string]string{"spec": "feature spec"}, "")

	if result.Error == nil {
		t.Fatal("expected error from provider failure, got none")
	}
	if !strings.Contains(result.Error.Message, "decompose failed") {
		t.Errorf("expected 'decompose failed' in error, got: %s", result.Error.Message)
	}
}

// --- Table-driven validateDAG tests ---

func TestValidateDAG(t *testing.T) {
	tests := []struct {
		name     string
		input    DecomposeOutput
		maxTasks int
		wantErr  string // substring; "" means no error
	}{
		{
			name: "valid linear chain",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
				{ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t1"}},
			}},
			maxTasks: 8,
			wantErr:  "",
		},
		{
			name: "valid single task",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "",
		},
		{
			name: "cycle detected",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{"t2"}},
				{ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{"t1"}},
			}},
			maxTasks: 8,
			wantErr:  "cycle",
		},
		{
			name: "file overlap at same level",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"shared.go"}, DependsOn: []string{}},
				{ID: "t2", Name: "b", Spec: "do b", Files: []string{"shared.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "shared.go",
		},
		{
			name: "file overlap allowed at different levels",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"shared.go"}, DependsOn: []string{}},
				{ID: "t2", Name: "b", Spec: "do b", Files: []string{"shared.go"}, DependsOn: []string{"t1"}},
			}},
			maxTasks: 8,
			wantErr:  "",
		},
		{
			name: "max tasks exceeded",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
				{ID: "t2", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{}},
				{ID: "t3", Name: "c", Spec: "do c", Files: []string{"c.go"}, DependsOn: []string{}},
			}},
			maxTasks: 2,
			wantErr:  "exceeds max_tasks",
		},
		{
			name: "missing id",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "missing required field",
		},
		{
			name: "missing name",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "missing required field",
		},
		{
			name: "missing spec",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "", Files: []string{"a.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "missing required field",
		},
		{
			name: "missing files",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "missing required field",
		},
		{
			name: "duplicate task ID",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
				{ID: "t1", Name: "b", Spec: "do b", Files: []string{"b.go"}, DependsOn: []string{}},
			}},
			maxTasks: 8,
			wantErr:  "duplicate task ID",
		},
		{
			name: "unknown dependency",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{"t99"}},
			}},
			maxTasks: 8,
			wantErr:  "unknown task",
		},
		{
			name: "diamond valid",
			input: DecomposeOutput{Tasks: []Task{
				{ID: "t1", Name: "root", Spec: "root", Files: []string{"root.go"}, DependsOn: []string{}},
				{ID: "t2", Name: "left", Spec: "left", Files: []string{"left.go"}, DependsOn: []string{"t1"}},
				{ID: "t3", Name: "right", Spec: "right", Files: []string{"right.go"}, DependsOn: []string{"t1"}},
				{ID: "t4", Name: "merge", Spec: "merge", Files: []string{"merge.go"}, DependsOn: []string{"t2", "t3"}},
			}},
			maxTasks: 8,
			wantErr:  "",
		},
		{
			name:     "empty task list",
			input:    DecomposeOutput{Tasks: []Task{}},
			maxTasks: 8,
			wantErr:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDAG(tc.input, tc.maxTasks)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.wantErr)
				} else if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
				}
			}
		})
	}
}

// --- extractJSON tests ---

func TestExtractJSONDirect(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
	}}
	out, err := extractJSON(mustJSON(tasks))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
}

func TestExtractJSONFences(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
	}}
	fenced := "```json\n" + mustJSON(tasks) + "\n```"
	out, err := extractJSON(fenced)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
}

func TestExtractJSONPrefix(t *testing.T) {
	tasks := DecomposeOutput{Tasks: []Task{
		{ID: "t1", Name: "a", Spec: "do a", Files: []string{"a.go"}, DependsOn: []string{}},
	}}
	prefixed := "Here is the result:\n" + mustJSON(tasks)
	out, err := extractJSON(prefixed)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
}

func TestExtractJSONFailure(t *testing.T) {
	_, err := extractJSON("I cannot help with that.")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to extract task graph") {
		t.Errorf("unexpected error: %v", err)
	}
}
