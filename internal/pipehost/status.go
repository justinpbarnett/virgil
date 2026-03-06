package pipehost

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

// EnvTaskID is the environment variable carrying the graph task identifier.
// Set by the executor when running parallel graph tasks; empty for sequential
// execution. Pipes that use StatusReporter include it in every event so the
// consumer can attribute events from concurrent pipes to the correct task.
const EnvTaskID = "VIRGIL_TASK_ID"

// StatusReporter provides a simple API for pipes to emit status events.
// It writes JSON lines to the given writer (typically os.Stderr).
//
// Most pipes do not need this. It exists for long-running pipes that want to
// report progress milestones or blocking waits beyond what slog messages and
// ToolChunkPrefix already provide.
type StatusReporter struct {
	pipe   string
	taskID string
	w      io.Writer
	mu     sync.Mutex
}

// NewStatusReporter creates a StatusReporter that writes to os.Stderr.
// The pipe name is taken from pipeName. taskID is read from the
// VIRGIL_TASK_ID environment variable (empty if not set).
func NewStatusReporter(pipeName string) *StatusReporter {
	taskID := os.Getenv(EnvTaskID)
	return NewStatusReporterWriter(os.Stderr, pipeName, taskID)
}

// NewStatusReporterWriter creates a StatusReporter with an explicit writer and
// task ID. Prefer NewStatusReporter for production use.
func NewStatusReporterWriter(w io.Writer, pipeName, taskID string) *StatusReporter {
	return &StatusReporter{
		pipe:   pipeName,
		taskID: taskID,
		w:      w,
	}
}

// Progress reports a meaningful progress milestone.
func (r *StatusReporter) Progress(message string) {
	r.emit(pipe.StatusProgress, message, nil)
}

// ProgressDetail reports a progress milestone with structured metadata.
func (r *StatusReporter) ProgressDetail(message string, detail map[string]any) {
	r.emit(pipe.StatusProgress, message, detail)
}

// Waiting reports that the pipe is blocked on an external resource.
// resource is a short identifier such as "anthropic-api" or "filesystem".
func (r *StatusReporter) Waiting(resource string) {
	r.emit(pipe.StatusWaiting, fmt.Sprintf("waiting for %s", resource), map[string]any{"resource": resource})
}

// Error reports a non-fatal error during execution.
func (r *StatusReporter) Error(message string) {
	r.emit(pipe.StatusError, message, nil)
}

// emit constructs a StatusEvent, marshals it to JSON, and writes it as a
// single newline-terminated line. The mutex prevents interleaving when
// multiple goroutines call methods concurrently.
func (r *StatusReporter) emit(status, message string, detail map[string]any) {
	event := pipe.StatusEvent{
		Status:  status,
		Pipe:    r.pipe,
		TaskID:  r.taskID,
		Message: message,
		TS:      pipe.NowUnix(),
		Detail:  detail,
	}
	b, err := json.Marshal(event)
	if err != nil {
		return
	}
	b = append(b, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.w.Write(b)
}

