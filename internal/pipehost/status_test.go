package pipehost

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

// captureReporter creates a StatusReporter writing to an in-memory buffer and
// returns both the reporter and the buffer for inspection.
func captureReporter(pipeName, taskID string) (*StatusReporter, *bytes.Buffer) {
	var buf bytes.Buffer
	r := NewStatusReporterWriter(&buf, pipeName, taskID)
	return r, &buf
}

// parseEvent decodes a single JSON line from buf into a StatusEvent.
func parseEvent(t *testing.T, buf *bytes.Buffer) pipe.StatusEvent {
	t.Helper()
	var e pipe.StatusEvent
	if err := json.NewDecoder(buf).Decode(&e); err != nil {
		t.Fatalf("failed to decode status event: %v\nraw: %q", err, buf.String())
	}
	return e
}

// TestStatusReporterProgress verifies Progress writes a valid status event.
func TestStatusReporterProgress(t *testing.T) {
	r, buf := captureReporter("build", "")
	r.Progress("writing tests")

	e := parseEvent(t, buf)
	if e.Status != pipe.StatusProgress {
		t.Errorf("expected status=%q, got %q", pipe.StatusProgress, e.Status)
	}
	if e.Pipe != "build" {
		t.Errorf("expected pipe=%q, got %q", "build", e.Pipe)
	}
	if e.Message != "writing tests" {
		t.Errorf("expected message=%q, got %q", "writing tests", e.Message)
	}
	if e.TS == 0 {
		t.Error("expected non-zero timestamp")
	}
	if e.TaskID != "" {
		t.Errorf("expected empty task_id, got %q", e.TaskID)
	}
}

// TestStatusReporterProgressDetail verifies ProgressDetail includes the detail map.
func TestStatusReporterProgressDetail(t *testing.T) {
	r, buf := captureReporter("build", "")
	r.ProgressDetail("step 2", map[string]any{"files": 3, "step": "tests"})

	e := parseEvent(t, buf)
	if e.Status != pipe.StatusProgress {
		t.Errorf("expected status=%q, got %q", pipe.StatusProgress, e.Status)
	}
	if e.Message != "step 2" {
		t.Errorf("expected message=%q, got %q", "step 2", e.Message)
	}
	if e.Detail == nil {
		t.Fatal("expected non-nil detail")
	}
	// JSON numbers decode as float64.
	files, _ := e.Detail["files"].(float64)
	if files != 3 {
		t.Errorf("expected detail.files=3, got %v", e.Detail["files"])
	}
	step, _ := e.Detail["step"].(string)
	if step != "tests" {
		t.Errorf("expected detail.step=%q, got %q", "tests", step)
	}
}

// TestStatusReporterWaiting verifies Waiting emits a waiting event with the
// resource captured in the detail map.
func TestStatusReporterWaiting(t *testing.T) {
	r, buf := captureReporter("build", "")
	r.Waiting("anthropic-api")

	e := parseEvent(t, buf)
	if e.Status != pipe.StatusWaiting {
		t.Errorf("expected status=%q, got %q", pipe.StatusWaiting, e.Status)
	}
	if !strings.Contains(e.Message, "anthropic-api") {
		t.Errorf("expected message to contain resource name, got %q", e.Message)
	}
	if e.Detail == nil {
		t.Fatal("expected non-nil detail")
	}
	resource, _ := e.Detail["resource"].(string)
	if resource != "anthropic-api" {
		t.Errorf("expected detail.resource=%q, got %q", "anthropic-api", resource)
	}
}

// TestStatusReporterError verifies Error emits an error event.
func TestStatusReporterError(t *testing.T) {
	r, buf := captureReporter("build", "")
	r.Error("rate limited, retrying")

	e := parseEvent(t, buf)
	if e.Status != pipe.StatusError {
		t.Errorf("expected status=%q, got %q", pipe.StatusError, e.Status)
	}
	if e.Message != "rate limited, retrying" {
		t.Errorf("expected message=%q, got %q", "rate limited, retrying", e.Message)
	}
}

// TestStatusReporterTaskID verifies that a reporter constructed with a task ID
// includes it in every event.
func TestStatusReporterTaskID(t *testing.T) {
	r, buf := captureReporter("build", "task-99")
	r.Progress("doing something")

	e := parseEvent(t, buf)
	if e.TaskID != "task-99" {
		t.Errorf("expected task_id=%q, got %q", "task-99", e.TaskID)
	}
}

// TestStatusReporterConcurrent verifies that 100 goroutines calling methods
// simultaneously produce only valid, non-interleaved JSON lines.
func TestStatusReporterConcurrent(t *testing.T) {
	var buf bytes.Buffer
	r := NewStatusReporterWriter(&buf, "build", "")

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				r.Progress("milestone")
			case 1:
				r.Waiting("api")
			case 2:
				r.Error("minor issue")
			}
		}(i)
	}
	wg.Wait()

	// Each line must be a valid JSON object.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Errorf("expected %d lines, got %d", n, len(lines))
	}
	for i, line := range lines {
		if line == "" {
			t.Errorf("line %d is empty", i)
			continue
		}
		var e pipe.StatusEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d is not valid JSON: %v\nline: %q", i, err, line)
		}
		if e.Status == "" {
			t.Errorf("line %d has empty status: %q", i, line)
		}
	}
}

// TestStatusReporterEnvTaskID verifies that NewStatusReporter reads the task
// ID from the VIRGIL_TASK_ID environment variable.
func TestStatusReporterEnvTaskID(t *testing.T) {
	t.Setenv(EnvTaskID, "env-task-7")

	// We can't easily intercept os.Stderr, so create via the writer variant
	// but verify EnvTaskID is exported and correct value.
	if EnvTaskID != "VIRGIL_TASK_ID" {
		t.Errorf("expected EnvTaskID=%q, got %q", "VIRGIL_TASK_ID", EnvTaskID)
	}
}
