package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// --- Mock executor ---

type mockExecutor struct {
	// calls records each invocation for inspection
	calls []mockCall
	// responses maps command prefix to response
	responses map[string]mockResponse
	// defaultResponse is used when no match is found in responses
	defaultResponse mockResponse
}

type mockCall struct {
	cmd string
	cwd string
}

type mockResponse struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (m *mockExecutor) Execute(_ context.Context, cmd string, cwd string) (string, string, int, error) {
	m.calls = append(m.calls, mockCall{cmd: cmd, cwd: cwd})

	for prefix, resp := range m.responses {
		if strings.HasPrefix(cmd, prefix) {
			return resp.stdout, resp.stderr, resp.exitCode, resp.err
		}
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

// --- Mock file checker ---

type mockFileChecker struct {
	files map[string]string // path -> content
}

func (m *mockFileChecker) Exists(path string) bool {
	_, ok := m.files[path]
	return ok
}

func (m *mockFileChecker) ReadFile(path string) ([]byte, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return []byte(content), nil
}

// --- Go test JSON parser tests ---

func TestParseGoTestJSON_AllPass(t *testing.T) {
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestOne"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestOne","Output":"=== RUN   TestOne\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestOne","Output":"--- PASS: TestOne (0.00s)\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestOne","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestTwo"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestTwo","Output":"=== RUN   TestTwo\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestTwo","Output":"--- PASS: TestTwo (0.00s)\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestTwo","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Output":"PASS\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.05}`

	result, err := parseGoTestJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true")
	}
	if result.Total != 2 {
		t.Errorf("expected Total=2, got %d", result.Total)
	}
	if result.Failed != 0 {
		t.Errorf("expected Failed=0, got %d", result.Failed)
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected no failures, got %d", len(result.Failures))
	}
}

func TestParseGoTestJSON_WithFailures(t *testing.T) {
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestGood"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestGood","Output":"--- PASS: TestGood (0.00s)\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestGood","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestBad"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestBad","Output":"    verify_test.go:42: expected 1, got 2\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Test":"TestBad","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Elapsed":0.05}`

	result, err := parseGoTestJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
	if result.Total != 2 {
		t.Errorf("expected Total=2, got %d", result.Total)
	}
	if result.Failed != 1 {
		t.Errorf("expected Failed=1, got %d", result.Failed)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(result.Failures))
	}
	if result.Failures[0].Test != "TestBad" {
		t.Errorf("expected test=TestBad, got %s", result.Failures[0].Test)
	}
	if result.Failures[0].Package != "pkg/a" {
		t.Errorf("expected package=pkg/a, got %s", result.Failures[0].Package)
	}
	if !strings.Contains(result.Failures[0].Error, "expected 1, got 2") {
		t.Errorf("expected error to contain 'expected 1, got 2', got %q", result.Failures[0].Error)
	}
}

func TestParseGoTestJSON_BuildError(t *testing.T) {
	// When compilation fails, go test -json emits non-JSON lines
	output := `# pkg/a
./main.go:10:5: undefined: foo`

	result, err := parseGoTestJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for build error")
	}
	if len(result.Failures) == 0 {
		t.Fatal("expected at least one failure for build error")
	}
	// The non-JSON lines should be captured as build errors
	foundBuildErr := false
	for _, f := range result.Failures {
		if strings.Contains(f.Error, "undefined: foo") {
			foundBuildErr = true
			break
		}
	}
	if !foundBuildErr {
		t.Error("expected build error to be captured in failures")
	}
}

func TestParseGoTestJSON_WithSkipped(t *testing.T) {
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestSkipped"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestSkipped","Output":"--- SKIP: TestSkipped (0.00s)\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"skip","Package":"pkg/a","Test":"TestSkipped","Elapsed":0.0}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.01}`

	result, err := parseGoTestJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true when only skipped tests")
	}
	if result.Total != 1 {
		t.Errorf("expected Total=1, got %d", result.Total)
	}
	if result.Skipped != 1 {
		t.Errorf("expected Skipped=1, got %d", result.Skipped)
	}
}

func TestParseGoTestJSON_EmptyOutput(t *testing.T) {
	result, err := parseGoTestJSON("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true for empty output")
	}
	if result.Total != 0 {
		t.Errorf("expected Total=0, got %d", result.Total)
	}
}

// --- Lint JSON parser tests ---

func TestParseLintJSON_Clean(t *testing.T) {
	output := `{"Issues":null}`

	result, err := parseLintJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true")
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

func TestParseLintJSON_WithErrors(t *testing.T) {
	output := `{
		"Issues": [
			{
				"FromLinter": "govet",
				"Text": "printf: Sprintf format %d has arg of wrong type",
				"Pos": {
					"Filename": "main.go",
					"Line": 42,
					"Column": 10
				}
			},
			{
				"FromLinter": "errcheck",
				"Text": "Error return value is not checked",
				"Pos": {
					"Filename": "handler.go",
					"Line": 15,
					"Column": 3
				}
			}
		]
	}`

	result, err := parseLintJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
	if len(result.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(result.Errors))
	}

	e1 := result.Errors[0]
	if e1.File != "main.go" {
		t.Errorf("expected file=main.go, got %s", e1.File)
	}
	if e1.Line != 42 {
		t.Errorf("expected line=42, got %d", e1.Line)
	}
	if e1.Column != 10 {
		t.Errorf("expected column=10, got %d", e1.Column)
	}
	if e1.Rule != "govet" {
		t.Errorf("expected rule=govet, got %s", e1.Rule)
	}
	if !strings.Contains(e1.Message, "wrong type") {
		t.Errorf("expected message to contain 'wrong type', got %q", e1.Message)
	}

	e2 := result.Errors[1]
	if e2.File != "handler.go" {
		t.Errorf("expected file=handler.go, got %s", e2.File)
	}
	if e2.Rule != "errcheck" {
		t.Errorf("expected rule=errcheck, got %s", e2.Rule)
	}
}

func TestParseLintJSON_EmptyOutput(t *testing.T) {
	result, err := parseLintJSON("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true for empty output")
	}
}

func TestParseLintJSON_EmptyIssues(t *testing.T) {
	output := `{"Issues":[]}`

	result, err := parseLintJSON(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true for empty issues array")
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

// --- Command detection tests ---

func TestDetectTestCommand_Justfile(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/justfile": "default: build\n\ntest:\n    go test ./... -v\n\nbuild:\n    go build\n",
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "just test" {
		t.Errorf("expected 'just test', got %q", cmd)
	}
}

func TestDetectTestCommand_GoMod(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n\ngo 1.25\n",
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "go test ./... -json -count=1" {
		t.Errorf("expected 'go test ./... -json -count=1', got %q", cmd)
	}
}

func TestDetectTestCommand_None(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{},
	}

	_, err := detectTestCommand("/project", fc)
	if err == nil {
		t.Fatal("expected error when no project config found")
	}
	if !strings.Contains(err.Error(), "no recognizable test configuration") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDetectTestCommand_GoModPriority(t *testing.T) {
	// go.mod takes priority over justfile because verify needs JSON output
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/justfile": "test:\n    custom test command\n",
			"/project/go.mod":   "module example.com/foo\n",
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "go test ./... -json -count=1" {
		t.Errorf("expected 'go test -json' (go.mod priority), got %q", cmd)
	}
}

func TestDetectTestCommand_JustfileNoTestRecipe(t *testing.T) {
	// justfile exists but has no test recipe — fall through to go.mod
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/justfile": "build:\n    go build\n",
			"/project/go.mod":   "module example.com/foo\n",
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "go test ./... -json -count=1" {
		t.Errorf("expected go test fallback, got %q", cmd)
	}
}

func TestDetectTestCommand_Makefile(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/Makefile": ".PHONY: test\ntest:\n\tgo test ./...\n",
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "make test" {
		t.Errorf("expected 'make test', got %q", cmd)
	}
}

func TestDetectTestCommand_PackageJSON(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/package.json": `{"name": "my-app", "scripts": {"test": "jest"}}`,
		},
	}

	cmd, err := detectTestCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "npm test" {
		t.Errorf("expected 'npm test', got %q", cmd)
	}
}

func TestDetectLintCommand_Justfile(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/justfile": "build:\n    go build\n\nlint:\n    golangci-lint run\n",
		},
	}

	cmd, err := detectLintCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "just lint" {
		t.Errorf("expected 'just lint', got %q", cmd)
	}
}

func TestDetectLintCommand_GolangciYml(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/.golangci.yml": "linters:\n  enable:\n    - govet\n",
		},
	}

	cmd, err := detectLintCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "golangci-lint run --out-format json" {
		t.Errorf("expected golangci-lint command, got %q", cmd)
	}
}

func TestDetectLintCommand_GoMod(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	cmd, err := detectLintCommand("/project", fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "golangci-lint run --out-format json" {
		t.Errorf("expected golangci-lint command, got %q", cmd)
	}
}

func TestDetectLintCommand_None(t *testing.T) {
	fc := &mockFileChecker{
		files: map[string]string{},
	}

	_, err := detectLintCommand("/project", fc)
	if err == nil {
		t.Fatal("expected error when no lint config found")
	}
}

// --- hasRecipe tests ---

func TestHasRecipe(t *testing.T) {
	justfile := "default: build\n\ntest:\n    go test ./...\n\nbuild:\n    go build\n"
	if !hasRecipe(justfile, "test") {
		t.Error("expected to find 'test' recipe")
	}
	if !hasRecipe(justfile, "build") {
		t.Error("expected to find 'build' recipe")
	}
	if hasRecipe(justfile, "lint") {
		t.Error("did not expect to find 'lint' recipe")
	}
}

func TestHasRecipe_WithDependencies(t *testing.T) {
	justfile := "test: build\n    go test ./...\n"
	if !hasRecipe(justfile, "test") {
		t.Error("expected to find 'test' recipe with dependencies")
	}
}

// --- Handler tests ---

// helperHandler creates a handler with a mock executor and file checker for testing.
func helperHandler(t *testing.T, exec *mockExecutor, fc *mockFileChecker) (func(envelope.Envelope, map[string]string) envelope.Envelope, string) {
	t.Helper()

	// Create a temp directory as the working directory
	cwd := t.TempDir()

	// Write any files from fc into the real temp dir so os.Stat passes
	// (the handler validates cwd with os.Stat)
	for relPath, content := range fc.files {
		// Only write files that are relative to "/project" by mapping them to cwd
		if strings.HasPrefix(relPath, "/project/") {
			realPath := filepath.Join(cwd, strings.TrimPrefix(relPath, "/project/"))
			dir := filepath.Dir(realPath)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(realPath, []byte(content), 0o644)
		}
	}

	// Rebuild the file checker with cwd-based paths
	realFC := &mockFileChecker{files: make(map[string]string)}
	for relPath, content := range fc.files {
		if strings.HasPrefix(relPath, "/project/") {
			realPath := filepath.Join(cwd, strings.TrimPrefix(relPath, "/project/"))
			realFC.files[realPath] = content
		}
	}

	pc := config.PipeConfig{Name: "verify"}
	handler := newHandlerWithFileChecker(exec, nil, pc, nil, realFC)
	return handler, cwd
}

func TestVerifyAllPass(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestOne"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestOne","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.05}`

	lintJSON := `{"Issues":null}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test": {stdout: testJSON, exitCode: 0},
			"golangci-lint": {stdout: lintJSON, exitCode: 0},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd})

	testutil.AssertEnvelope(t, result, "verify", "verify")

	if result.Error != nil {
		t.Fatalf("expected no error, got: %v", result.Error)
	}

	vo, ok := result.Content.(VerifyOutput)
	if !ok {
		t.Fatalf("expected VerifyOutput, got %T", result.Content)
	}
	if !vo.Passed {
		t.Error("expected Passed=true")
	}
	if vo.TestResult == nil {
		t.Fatal("expected TestResult to be non-nil")
	}
	if !vo.TestResult.Passed {
		t.Error("expected TestResult.Passed=true")
	}
	if vo.LintResult == nil {
		t.Fatal("expected LintResult to be non-nil")
	}
	if !vo.LintResult.Passed {
		t.Error("expected LintResult.Passed=true")
	}
}

func TestVerifyTestFail(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestBad"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestBad","Output":"    verify_test.go:42: expected 1, got 2\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Test":"TestBad","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Elapsed":0.05}`

	lintJSON := `{"Issues":null}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test":       {stdout: testJSON, exitCode: 1},
			"golangci-lint": {stdout: lintJSON, exitCode: 0},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd})

	if result.Error == nil {
		t.Fatal("expected error for test failure")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true")
	}
	if result.Error.Severity != envelope.SeverityError {
		t.Errorf("expected severity=error, got %s", result.Error.Severity)
	}

	vo := result.Content.(VerifyOutput)
	if vo.Passed {
		t.Error("expected Passed=false")
	}
	if vo.TestResult.Failed != 1 {
		t.Errorf("expected 1 failure, got %d", vo.TestResult.Failed)
	}
	if !strings.Contains(vo.Summary, "1 test failure") {
		t.Errorf("expected summary to mention test failure, got %q", vo.Summary)
	}
}

func TestVerifyLintFail(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestOne"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestOne","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.05}`

	lintJSON := `{"Issues":[{"FromLinter":"govet","Text":"bad thing","Pos":{"Filename":"main.go","Line":10,"Column":5}}]}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test":       {stdout: testJSON, exitCode: 0},
			"golangci-lint": {stdout: lintJSON, exitCode: 1},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd})

	if result.Error == nil {
		t.Fatal("expected error for lint failure")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true")
	}

	vo := result.Content.(VerifyOutput)
	if vo.Passed {
		t.Error("expected Passed=false")
	}
	if vo.LintResult == nil {
		t.Fatal("expected LintResult non-nil")
	}
	if vo.LintResult.Passed {
		t.Error("expected LintResult.Passed=false")
	}
	if len(vo.LintResult.Errors) != 1 {
		t.Errorf("expected 1 lint error, got %d", len(vo.LintResult.Errors))
	}
	if !strings.Contains(vo.Summary, "1 lint error") {
		t.Errorf("expected summary to mention lint error, got %q", vo.Summary)
	}
}

func TestVerifyBothFail(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestBad"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestBad","Output":"    error here\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Test":"TestBad","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestAlsoBad"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"pkg/a","Test":"TestAlsoBad","Output":"    another error\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Test":"TestAlsoBad","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"pkg/a","Elapsed":0.05}`

	lintJSON := `{"Issues":[
		{"FromLinter":"govet","Text":"bad1","Pos":{"Filename":"a.go","Line":1,"Column":1}},
		{"FromLinter":"errcheck","Text":"bad2","Pos":{"Filename":"b.go","Line":2,"Column":1}}
	]}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test":       {stdout: testJSON, exitCode: 1},
			"golangci-lint": {stdout: lintJSON, exitCode: 1},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd})

	if result.Error == nil {
		t.Fatal("expected error")
	}

	vo := result.Content.(VerifyOutput)
	if vo.Passed {
		t.Error("expected Passed=false")
	}
	if !strings.Contains(vo.Summary, "2 test failure") {
		t.Errorf("expected summary to contain '2 test failure', got %q", vo.Summary)
	}
	if !strings.Contains(vo.Summary, "2 lint error") {
		t.Errorf("expected summary to contain '2 lint error', got %q", vo.Summary)
	}
}

func TestVerifyLintDisabled(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestOne"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestOne","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.05}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test": {stdout: testJSON, exitCode: 0},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd, "lint": "false"})

	if result.Error != nil {
		t.Fatalf("expected no error, got: %v", result.Error)
	}

	vo := result.Content.(VerifyOutput)
	if !vo.Passed {
		t.Error("expected Passed=true")
	}
	if vo.LintResult != nil {
		t.Error("expected LintResult to be nil when lint disabled")
	}

	// Verify only the test command was executed (no lint command)
	for _, call := range exec.calls {
		if strings.HasPrefix(call.cmd, "golangci-lint") {
			t.Error("lint command should not have been executed when lint=false")
		}
	}
}

func TestVerifyExecutorError(t *testing.T) {
	exec := &mockExecutor{
		defaultResponse: mockResponse{
			err: fmt.Errorf("command not found: go"),
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "test execution failed") {
		t.Errorf("expected 'test execution failed' in error, got %q", result.Error.Message)
	}
}

func TestVerifyInvalidCwd(t *testing.T) {
	exec := &mockExecutor{}
	pc := config.PipeConfig{Name: "verify"}
	fc := &OSFileChecker{}
	handler := newHandlerWithFileChecker(exec, nil, pc, nil, fc)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cwd": "/nonexistent/path/that/does/not/exist"})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "invalid working directory") {
		t.Errorf("expected 'invalid working directory' error, got %q", result.Error.Message)
	}
}

func TestVerifyNoTestConfig(t *testing.T) {
	exec := &mockExecutor{}

	// Create a real temp dir so os.Stat passes
	cwd := t.TempDir()
	fc := &mockFileChecker{files: make(map[string]string)}

	pc := config.PipeConfig{Name: "verify"}
	handler := newHandlerWithFileChecker(exec, nil, pc, nil, fc)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"cwd": cwd})

	testutil.AssertFatalError(t, result)
	if !strings.Contains(result.Error.Message, "cannot detect test command") {
		t.Errorf("expected 'cannot detect test command' error, got %q", result.Error.Message)
	}
}

func TestVerifyCustomSuite(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg/a","Test":"TestOne"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Test":"TestOne","Elapsed":0.01}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.05}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"my-custom-test": {stdout: testJSON, exitCode: 0},
		},
	}

	cwd := t.TempDir()
	fc := &mockFileChecker{files: make(map[string]string)}

	pc := config.PipeConfig{Name: "verify"}
	handler := newHandlerWithFileChecker(exec, nil, pc, nil, fc)
	input := envelope.New("input", "test")

	_ = handler(input, map[string]string{"cwd": cwd, "suite": "my-custom-test-cmd", "lint": "false"})

	// The custom suite command is used directly
	if len(exec.calls) == 0 {
		t.Fatal("expected at least one executor call")
	}
	if exec.calls[0].cmd != "my-custom-test-cmd" {
		t.Errorf("expected custom test command, got %q", exec.calls[0].cmd)
	}
}

func TestVerifyEnvelopeCompliance(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.01}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test": {stdout: testJSON, exitCode: 0},
		},
	}
	fc := &mockFileChecker{
		files: map[string]string{
			"/project/go.mod": "module example.com/foo\n",
		},
	}

	handler, cwd := helperHandler(t, exec, fc)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"cwd": cwd, "lint": "false"})

	testutil.AssertEnvelope(t, result, "verify", "verify")
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected content_type=structured, got %s", result.ContentType)
	}
	if result.Args == nil {
		t.Error("expected args to be non-nil")
	}
}

func TestVerifyCwdFromInput(t *testing.T) {
	testJSON := `{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"pkg/a","Elapsed":0.01}`

	exec := &mockExecutor{
		responses: map[string]mockResponse{
			"go test": {stdout: testJSON, exitCode: 0},
		},
	}

	// Create a temp dir with go.mod
	cwd := t.TempDir()
	goModPath := filepath.Join(cwd, "go.mod")
	os.WriteFile(goModPath, []byte("module example.com/foo\n"), 0o644)

	fc := &mockFileChecker{
		files: map[string]string{
			goModPath: "module example.com/foo\n",
		},
	}

	pc := config.PipeConfig{Name: "verify"}
	handler := newHandlerWithFileChecker(exec, nil, pc, nil, fc)

	// Pass cwd through input envelope content
	input := envelope.New("input", "test")
	input.Content = map[string]any{"cwd": cwd}

	result := handler(input, map[string]string{"lint": "false"})

	if result.Error != nil {
		t.Fatalf("expected no error, got: %v", result.Error)
	}
	if len(exec.calls) == 0 {
		t.Fatal("expected executor to be called")
	}
	if exec.calls[0].cwd != cwd {
		t.Errorf("expected cwd=%s, got %s", cwd, exec.calls[0].cwd)
	}
}
