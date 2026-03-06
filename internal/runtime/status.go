package runtime

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

// StatusEvent carries a single parallel task event from a task goroutine.
type StatusEvent struct {
	TaskID    string
	Type      string // "status", "chunk", "done"
	Status    string // "waiting", "running", "done", "failed"
	Name      string
	Pipe      string
	Activity  string
	Text      string
	Duration  time.Duration
	Error     string
	DependsOn []string
}

// StatusSink is a callback invoked by task goroutines to report task events.
// Implementations must be safe for concurrent use from multiple goroutines.
type StatusSink func(event StatusEvent)

// NewStatusSink wraps a StreamEvent sink with a mutex and returns a StatusSink
// that translates StatusEvent values into the three new SSE event types.
func NewStatusSink(mu *sync.Mutex, sink func(StreamEvent)) StatusSink {
	return func(ev StatusEvent) {
		mu.Lock()
		defer mu.Unlock()

		switch ev.Type {
		case "status":
			payload := map[string]any{
				"task_id":    ev.TaskID,
				"name":       ev.Name,
				"pipe":       ev.Pipe,
				"status":     ev.Status,
				"activity":   ev.Activity,
				"depends_on": ev.DependsOn,
			}
			data, _ := json.Marshal(payload)
			sink(StreamEvent{Type: envelope.SSEEventTaskStatus, Data: string(data)})

		case "chunk":
			payload := map[string]string{
				"task_id": ev.TaskID,
				"text":    ev.Text,
			}
			data, _ := json.Marshal(payload)
			sink(StreamEvent{Type: envelope.SSEEventTaskChunk, Data: string(data)})

		case "done":
			dur := ""
			if ev.Duration > 0 {
				dur = ev.Duration.Round(time.Millisecond).String()
			}
			payload := map[string]string{
				"task_id":  ev.TaskID,
				"status":   ev.Status,
				"duration": dur,
				"error":    ev.Error,
			}
			data, _ := json.Marshal(payload)
			sink(StreamEvent{Type: envelope.SSEEventTaskDone, Data: string(data)})
		}
	}
}
