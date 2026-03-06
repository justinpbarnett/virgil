package verify

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// goTestEvent represents a single JSON event from `go test -json`.
type goTestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Output  string    `json:"Output"`
	Elapsed float64   `json:"Elapsed"`
}

// golangciLintOutput represents the top-level JSON output from golangci-lint.
type golangciLintOutput struct {
	Issues []golangciLintIssue `json:"Issues"`
}

// golangciLintIssue represents a single lint issue.
type golangciLintIssue struct {
	FromLinter string           `json:"FromLinter"`
	Text       string           `json:"Text"`
	Pos        golangciLintPos  `json:"Pos"`
	SourceLines []string        `json:"SourceLines"`
}

// golangciLintPos represents the position of a lint issue.
type golangciLintPos struct {
	Filename string `json:"Filename"`
	Line     int    `json:"Line"`
	Column   int    `json:"Column"`
}

// parseGoTestJSON parses output from `go test -json` into a TestResult.
// Each line of the output is a JSON object with Action, Package, Test, Output fields.
// We track pass/fail per test and collect failure output.
func parseGoTestJSON(output string) (*TestResult, error) {
	result := &TestResult{
		Passed: true,
	}

	// Track output per test for failure messages
	type testKey struct {
		pkg  string
		test string
	}
	testOutputs := make(map[testKey][]string)
	testResults := make(map[testKey]string) // "pass", "fail", "skip"
	packageResults := make(map[string]string)

	var maxElapsed float64
	hasEvents := false

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Non-JSON lines (e.g., build errors) indicate a compilation failure
			// Collect them as a build error
			result.Passed = false
			result.Failures = append(result.Failures, TestFailure{
				Test:    "(build)",
				Package: "",
				Error:   line,
			})
			continue
		}

		hasEvents = true
		key := testKey{pkg: event.Package, test: event.Test}

		switch event.Action {
		case "output":
			if event.Test != "" {
				testOutputs[key] = append(testOutputs[key], event.Output)
			}
		case "pass":
			if event.Test != "" {
				testResults[key] = "pass"
			} else {
				packageResults[event.Package] = "pass"
				if event.Elapsed > maxElapsed {
					maxElapsed = event.Elapsed
				}
			}
		case "fail":
			if event.Test != "" {
				testResults[key] = "fail"
			} else {
				packageResults[event.Package] = "fail"
				if event.Elapsed > maxElapsed {
					maxElapsed = event.Elapsed
				}
			}
		case "skip":
			if event.Test != "" {
				testResults[key] = "skip"
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning go test output: %w", err)
	}

	if !hasEvents && len(result.Failures) == 0 {
		// No JSON events and no build errors — empty output
		return result, nil
	}

	// Count results
	for key, status := range testResults {
		switch status {
		case "pass":
			result.Total++
		case "fail":
			result.Total++
			result.Failed++
			result.Passed = false
			outputLines := testOutputs[key]
			errorMsg := strings.TrimSpace(strings.Join(outputLines, ""))
			result.Failures = append(result.Failures, TestFailure{
				Test:    key.test,
				Package: key.pkg,
				Error:   errorMsg,
			})
		case "skip":
			result.Total++
			result.Skipped++
		}
	}

	// Check for package-level failures without individual test failures
	// (e.g., build errors that prevent tests from running)
	for pkg, status := range packageResults {
		if status == "fail" {
			// Check if we already have test-level failures for this package
			hasTestFailures := false
			for key, s := range testResults {
				if key.pkg == pkg && s == "fail" {
					hasTestFailures = true
					break
				}
			}
			if !hasTestFailures {
				result.Passed = false
				// Collect package-level output
				key := testKey{pkg: pkg, test: ""}
				outputLines := testOutputs[key]
				errorMsg := strings.TrimSpace(strings.Join(outputLines, ""))
				if errorMsg == "" {
					errorMsg = fmt.Sprintf("package %s failed", pkg)
				}
				result.Failures = append(result.Failures, TestFailure{
					Test:    "(build)",
					Package: pkg,
					Error:   errorMsg,
				})
			}
		}
	}

	result.Duration = time.Duration(maxElapsed * float64(time.Second))

	return result, nil
}

// parseLintJSON parses output from `golangci-lint run --out-format json` into a LintResult.
func parseLintJSON(output string) (*LintResult, error) {
	result := &LintResult{
		Passed: true,
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return result, nil
	}

	var lintOutput golangciLintOutput
	if err := json.Unmarshal([]byte(output), &lintOutput); err != nil {
		return nil, fmt.Errorf("parsing golangci-lint JSON: %w", err)
	}

	if lintOutput.Issues == nil {
		return result, nil
	}

	for _, issue := range lintOutput.Issues {
		result.Errors = append(result.Errors, LintError{
			File:    issue.Pos.Filename,
			Line:    issue.Pos.Line,
			Column:  issue.Pos.Column,
			Rule:    issue.FromLinter,
			Message: issue.Text,
		})
	}

	if len(result.Errors) > 0 {
		result.Passed = false
	}

	return result, nil
}

// FileChecker abstracts file-system existence checks for testability.
type FileChecker interface {
	Exists(path string) bool
	ReadFile(path string) ([]byte, error)
}

// OSFileChecker implements FileChecker using the real file system.
type OSFileChecker struct{}

func (f *OSFileChecker) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (f *OSFileChecker) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// detectTestCommand auto-detects the appropriate test command for the project at cwd.
// Checks justfile (test recipe), Makefile (test target), go.mod (go test),
// package.json (npm test), in that order.
func detectTestCommand(cwd string, fc FileChecker) (string, error) {
	// Go projects: always use `go test -json` since verify needs
	// machine-readable output. Task runners (justfile, Makefile) produce
	// human-readable output that the JSON parser can't handle.
	goModPath := filepath.Join(cwd, "go.mod")
	if fc.Exists(goModPath) {
		return "go test ./... -json -count=1", nil
	}

	// Check for justfile with test recipe
	justfilePath := filepath.Join(cwd, "justfile")
	if fc.Exists(justfilePath) {
		data, err := fc.ReadFile(justfilePath)
		if err == nil {
			if hasRecipe(string(data), "test") {
				return "just test", nil
			}
		}
	}

	// Check for Makefile with test target
	if detectMakefileTarget(cwd, fc, "test") {
		return "make test", nil
	}

	// Check for package.json
	packageJSONPath := filepath.Join(cwd, "package.json")
	if fc.Exists(packageJSONPath) {
		return "npm test", nil
	}

	return "", fmt.Errorf("no recognizable test configuration found in %s", cwd)
}

// detectMakefileTarget checks whether the Makefile in cwd contains a target with the given name.
func detectMakefileTarget(cwd string, fc FileChecker, target string) bool {
	makefilePath := filepath.Join(cwd, "Makefile")
	if !fc.Exists(makefilePath) {
		return false
	}
	data, err := fc.ReadFile(makefilePath)
	if err != nil {
		return false
	}
	prefix := target + ":"
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// detectLintCommand auto-detects the appropriate lint command for the project at cwd.
func detectLintCommand(cwd string, fc FileChecker) (string, error) {
	// Check for justfile with lint recipe
	justfilePath := filepath.Join(cwd, "justfile")
	if fc.Exists(justfilePath) {
		data, err := fc.ReadFile(justfilePath)
		if err == nil {
			if hasRecipe(string(data), "lint") {
				return "just lint", nil
			}
		}
	}

	// Check for .golangci.yml or .golangci.yaml
	for _, name := range []string{".golangci.yml", ".golangci.yaml"} {
		if fc.Exists(filepath.Join(cwd, name)) {
			return "golangci-lint run --out-format json", nil
		}
	}

	// Check for go.mod (assume golangci-lint is available for Go projects)
	goModPath := filepath.Join(cwd, "go.mod")
	if fc.Exists(goModPath) {
		return "golangci-lint run --out-format json", nil
	}

	return "", fmt.Errorf("no recognizable lint configuration found in %s", cwd)
}

// hasRecipe checks whether a justfile contains a recipe with the given name.
// Justfile recipes look like "name:" or "name: dep1 dep2" at the start of a line (no leading whitespace).
func hasRecipe(justfileContent string, recipe string) bool {
	scanner := bufio.NewScanner(strings.NewReader(justfileContent))
	for scanner.Scan() {
		line := scanner.Text()
		// Recipe lines must start at column 0 (no leading whitespace)
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		// Find the colon that separates recipe name from dependencies
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colonIdx])
		// Handle recipes with parameters like "name param"
		parts := strings.Fields(name)
		if len(parts) > 0 && parts[0] == recipe {
			return true
		}
	}
	return false
}
