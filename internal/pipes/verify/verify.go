package verify

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// VerifyOutput is the structured content returned by the verify pipe.
type VerifyOutput struct {
	Passed     bool        `json:"passed"`
	TestResult *TestResult `json:"test_result"`
	LintResult *LintResult `json:"lint_result"`
	Summary    string      `json:"summary"`
}

// TestResult holds the aggregate results of a test suite run.
type TestResult struct {
	Passed   bool          `json:"passed"`
	Total    int           `json:"total"`
	Failed   int           `json:"failed"`
	Skipped  int           `json:"skipped"`
	Failures []TestFailure `json:"failures"`
	Duration time.Duration `json:"duration"`
}

// TestFailure describes a single test failure with enough detail for the fix pipe.
type TestFailure struct {
	File     string `json:"file"`
	Test     string `json:"test"`
	Package  string `json:"package"`
	Error    string `json:"error"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// LintResult holds the aggregate results of a lint run.
type LintResult struct {
	Passed bool        `json:"passed"`
	Errors []LintError `json:"errors"`
}

// LintError describes a single lint error with file location and rule details.
type LintError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Executor is an alias for the shared pipeutil.Executor interface.
type Executor = pipeutil.Executor

// OSExecutor is an alias for the shared pipeutil.OSExecutor.
type OSExecutor = pipeutil.OSExecutor

// NewHandler returns a pipe.Handler that runs tests and linters, producing
// structured verification results.
func NewHandler(executor Executor, provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return newHandlerWithFileChecker(executor, provider, pipeConfig, logger, &OSFileChecker{})
}

// newHandlerWithFileChecker is the internal constructor that accepts a FileChecker for testing.
func newHandlerWithFileChecker(executor Executor, provider bridge.Provider, _ config.PipeConfig, logger *slog.Logger, fc FileChecker) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("verify", "verify")
		out.Args = flags

		// 1. Determine working directory
		cwd := flags["cwd"]
		if cwd == "" {
			// Try to get cwd from input envelope's structured content
			if m, ok := input.Content.(map[string]any); ok {
				if w, ok := m["cwd"].(string); ok && w != "" {
					cwd = w
				}
			}
		}
		if cwd == "" {
			// Default to current working directory
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				out.Duration = time.Since(out.Timestamp)
				out.Error = envelope.FatalError(fmt.Sprintf("cannot determine working directory: %v", err))
				return out
			}
		}

		// Validate cwd exists
		info, err := os.Stat(cwd)
		if err != nil || !info.IsDir() {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("invalid working directory: %s", cwd))
			return out
		}

		// 2. Determine and run test command
		suite := flags["suite"]
		var testCmd string
		if suite != "" && suite != "auto" {
			testCmd = suite
		} else {
			detected, err := detectTestCommand(cwd, fc)
			if err != nil {
				out.Duration = time.Since(out.Timestamp)
				out.Error = envelope.FatalError(fmt.Sprintf("cannot detect test command: %v", err))
				return out
			}
			testCmd = detected
		}

		logger.Debug("running tests", "cmd", testCmd, "cwd", cwd)

		ctx := context.Background()
		testStdout, testStderr, testExitCode, testErr := executor.Execute(ctx, testCmd, cwd)

		if testErr != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("test execution failed: %v", testErr))
			return out
		}

		// Parse test output — use stdout for JSON output, fall back to stderr
		testOutput := testStdout
		if testOutput == "" {
			testOutput = testStderr
		}

		testResult, parseErr := parseGoTestJSON(testOutput)
		if parseErr != nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError(fmt.Sprintf("failed to parse test output: %v", parseErr))
			return out
		}

		// If tests had a non-zero exit and we parsed no failures, mark as failed
		if testExitCode != 0 && testResult.Passed && testResult.Total == 0 {
			testResult.Passed = false
			errOutput := testStderr
			if errOutput == "" {
				errOutput = testStdout
			}
			testResult.Failures = append(testResult.Failures, TestFailure{
				Test:  "(build)",
				Error: strings.TrimSpace(errOutput),
			})
		}

		// 3. Optionally run lint
		var lintResult *LintResult
		lintFlag := flags["lint"]
		if lintFlag == "" {
			lintFlag = "true"
		}

		if lintFlag == "true" {
			lintCmd, lintDetectErr := detectLintCommand(cwd, fc)
			if lintDetectErr != nil {
				// Lint is optional — warn and skip
				logger.Warn("lint command not detected, skipping", "error", lintDetectErr)
			} else {
				logger.Debug("running lint", "cmd", lintCmd, "cwd", cwd)
				lintStdout, _, _, lintExecErr := executor.Execute(ctx, lintCmd, cwd)

				if lintExecErr != nil {
					// Lint execution itself failed (e.g., command not found)
					logger.Warn("lint execution failed, skipping", "error", lintExecErr)
				} else {
					parsed, lintParseErr := parseLintJSON(lintStdout)
					if lintParseErr != nil {
						logger.Warn("failed to parse lint output, skipping", "error", lintParseErr)
					} else {
						lintResult = parsed
					}
				}
			}
		}

		// 4. Plan-check (deferred)
		if flags["plan-check"] == "true" {
			if provider == nil {
				logger.Warn("plan-check requested but no provider available, skipping")
			} else {
				logger.Warn("plan-check not yet implemented, skipping")
			}
		}

		// 5. Aggregate results
		verifyOutput := VerifyOutput{
			Passed:     testResult.Passed,
			TestResult: testResult,
			LintResult: lintResult,
		}

		if lintResult != nil && !lintResult.Passed {
			verifyOutput.Passed = false
		}

		// Build summary
		var summaryParts []string
		if testResult.Failed > 0 {
			summaryParts = append(summaryParts, fmt.Sprintf("%d test failure(s)", testResult.Failed))
		} else if !testResult.Passed {
			summaryParts = append(summaryParts, "test suite failed")
		}
		if lintResult != nil && !lintResult.Passed {
			summaryParts = append(summaryParts, fmt.Sprintf("%d lint error(s)", len(lintResult.Errors)))
		}

		if verifyOutput.Passed {
			passed := testResult.Total - testResult.Skipped
			verifyOutput.Summary = fmt.Sprintf("all checks passed (%d tests passed", passed)
			if testResult.Skipped > 0 {
				verifyOutput.Summary += fmt.Sprintf(", %d skipped", testResult.Skipped)
			}
			if lintResult != nil {
				verifyOutput.Summary += ", lint clean"
			}
			verifyOutput.Summary += ")"
		} else {
			verifyOutput.Summary = strings.Join(summaryParts, ", ")
		}

		// 6. Return envelope
		out.Content = verifyOutput
		out.ContentType = envelope.ContentStructured

		if !verifyOutput.Passed {
			out.Error = &envelope.EnvelopeError{
				Message:   verifyOutput.Summary,
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
		}

		out.Duration = time.Since(out.Timestamp)
		return out
	}
}
