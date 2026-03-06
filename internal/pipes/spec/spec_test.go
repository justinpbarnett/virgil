package spec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// mockStore implements SpecStore for tests.
type mockStore struct {
	state map[string]string
}

func newMockStore() *mockStore {
	return &mockStore{state: make(map[string]string)}
}

func (m *mockStore) GetState(namespace, key string) (string, bool, error) {
	v, ok := m.state[namespace+"/"+key]
	return v, ok, nil
}

func (m *mockStore) PutState(namespace, key, content string) error {
	m.state[namespace+"/"+key] = content
	return nil
}

// capturingProvider records the last user prompt for assertion.
type capturingProvider struct {
	response   string
	lastPrompt string
}

func (c *capturingProvider) Complete(_ context.Context, _, user string) (string, error) {
	c.lastPrompt = user
	return c.response, nil
}

func testConfig() config.PipeConfig {
	return config.PipeConfig{
		Name: "spec",
		Prompts: config.PromptsConfig{
			System: "You are a senior software architect.",
			Templates: map[string]string{
				"create": "Create spec.\n\nFeature request:\n{{.Signal}}\n\n{{if .CodebaseContext}}Codebase context:\n{{.CodebaseContext}}\n{{end}}",
				"update": "Update spec.\n\nCurrent spec:\n{{.State}}\n\nEngineer input:\n{{.Signal}}\n\n{{if .CodebaseContext}}Codebase context:\n{{.CodebaseContext}}\n{{end}}",
			},
		},
	}
}

// makeTempSpecs creates a temp directory with optional .md files and returns the dir path.
func makeTempSpecs(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# "+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// --- resolveSpecFile tests ---

func TestResolveSpecFile_ExplicitPath(t *testing.T) {
	dir := makeTempSpecs(t, "feat-foo.md")
	path, exists := resolveSpecFile(dir, map[string]string{"path": "feat-foo.md"}, nil)
	if path != filepath.Join(dir, "feat-foo.md") {
		t.Errorf("unexpected path: %s", path)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestResolveSpecFile_ExplicitPathNew(t *testing.T) {
	dir := makeTempSpecs(t)
	path, exists := resolveSpecFile(dir, map[string]string{"path": "feat-new.md"}, nil)
	if path != filepath.Join(dir, "feat-new.md") {
		t.Errorf("unexpected path: %s", path)
	}
	if exists {
		t.Error("expected exists=false")
	}
}

func TestResolveSpecFile_TopicMatch(t *testing.T) {
	dir := makeTempSpecs(t, "feat-slack-pipe.md")
	path, exists := resolveSpecFile(dir, map[string]string{"topic": "slack pipe"}, nil)
	if path != filepath.Join(dir, "feat-slack-pipe.md") {
		t.Errorf("unexpected path: %s", path)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestResolveSpecFile_TopicNoMatch(t *testing.T) {
	dir := makeTempSpecs(t, "feat-slack-pipe.md")
	path, exists := resolveSpecFile(dir, map[string]string{"topic": "quantum computing"}, nil)
	if filepath.Dir(path) != dir {
		t.Errorf("expected path in dir %s, got %s", dir, path)
	}
	if exists {
		t.Error("expected exists=false for no match")
	}
	if filepath.Base(path) != "feat-quantum-computing.md" {
		t.Errorf("unexpected filename: %s", filepath.Base(path))
	}
}

func TestResolveSpecFile_ActiveFallback(t *testing.T) {
	dir := makeTempSpecs(t, "feat-foo.md")
	st := newMockStore()
	st.state["spec/active"] = "feat-foo.md"
	path, exists := resolveSpecFile(dir, map[string]string{}, st)
	if path != filepath.Join(dir, "feat-foo.md") {
		t.Errorf("unexpected path: %s", path)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestResolveSpecFile_NewFromTopic(t *testing.T) {
	dir := makeTempSpecs(t)
	path, exists := resolveSpecFile(dir, map[string]string{"topic": "notification system"}, nil)
	if exists {
		t.Error("expected exists=false")
	}
	if filepath.Base(path) != "feat-notification-system.md" {
		t.Errorf("unexpected filename: %s", filepath.Base(path))
	}
}

// --- findSpec tests ---

func TestFindSpec_SubstringMatch(t *testing.T) {
	dir := makeTempSpecs(t, "feat-slack-pipe.md", "feat-calendar.md")
	result := findSpec(dir, "slack")
	if result != filepath.Join(dir, "feat-slack-pipe.md") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestFindSpec_PrefersShortestMatch(t *testing.T) {
	dir := makeTempSpecs(t, "feat-slack-pipe.md", "feat-slack-integration.md")
	result := findSpec(dir, "slack")
	if result != filepath.Join(dir, "feat-slack-pipe.md") {
		t.Errorf("expected shortest match feat-slack-pipe.md, got %s", filepath.Base(result))
	}
}

func TestFindSpec_EmptyDir(t *testing.T) {
	dir := makeTempSpecs(t)
	result := findSpec(dir, "anything")
	if result != "" {
		t.Errorf("expected empty result, got %s", result)
	}
}

// --- handler integration tests ---

func TestHandler_CreateNewSpec(t *testing.T) {
	dir := makeTempSpecs(t)
	st := newMockStore()
	provider := &testutil.MockProvider{Response: "# Generated spec content"}
	pc := testConfig()
	compiled := CompileTemplates(pc)

	handler := NewHandlerWith(provider, pc, compiled, st, dir, nil)

	input := envelope.New("signal", "input")
	flags := map[string]string{"signal": "spec out notification system", "topic": "notification system"}
	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if result.Content != "# Generated spec content" {
		t.Errorf("unexpected content: %v", result.Content)
	}

	// Verify file was written
	expected := filepath.Join(dir, "feat-notification-system.md")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("spec file not written: %v", err)
	}
	if string(data) != "# Generated spec content" {
		t.Errorf("unexpected file content: %s", data)
	}

	// Verify working_state set
	active, ok, _ := st.GetState("spec", "active")
	if !ok || active != "feat-notification-system.md" {
		t.Errorf("expected spec/active=feat-notification-system.md, got %q", active)
	}
}

func TestHandler_UpdateExistingSpec(t *testing.T) {
	dir := makeTempSpecs(t, "feat-slack-pipe.md")
	st := newMockStore()
	cap := &capturingProvider{response: "# Updated spec"}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(cap, pc, compiled, st, dir, nil)

	input := envelope.New("signal", "input")
	flags := map[string]string{"signal": "use approach B", "topic": "slack pipe"}
	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}

	// Verify the update template was used
	if !strings.Contains(cap.lastPrompt, "Current spec:") {
		t.Errorf("expected update template to be used, prompt was: %s", cap.lastPrompt)
	}
}

func TestHandler_CodebaseContextPassthrough(t *testing.T) {
	dir := makeTempSpecs(t)
	st := newMockStore()
	cap := &capturingProvider{response: "# Spec"}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(cap, pc, compiled, st, dir, nil)

	input := envelope.New("study", "analyze")
	input.Content = "codebase analysis results"
	input.ContentType = envelope.ContentText

	flags := map[string]string{"signal": "new feature", "topic": "new feature"}
	result := handler(input, flags)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if !strings.Contains(cap.lastPrompt, "codebase analysis results") {
		t.Errorf("expected codebase context in prompt, got: %s", cap.lastPrompt)
	}
}

func TestHandler_WorkingStateSet(t *testing.T) {
	dir := makeTempSpecs(t)
	st := newMockStore()
	provider := &testutil.MockProvider{Response: "# Spec"}

	pc := testConfig()
	compiled := CompileTemplates(pc)
	handler := NewHandlerWith(provider, pc, compiled, st, dir, nil)

	input := envelope.New("signal", "input")
	flags := map[string]string{"signal": "auth system", "topic": "auth system"}
	handler(input, flags)

	active, ok, _ := st.GetState("spec", "active")
	if !ok {
		t.Fatal("expected spec/active to be set")
	}
	if active != "feat-auth-system.md" {
		t.Errorf("expected feat-auth-system.md, got %s", active)
	}
}
