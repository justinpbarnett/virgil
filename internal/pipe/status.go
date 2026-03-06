package pipe

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"
)

// StatusEvent is a structured status report emitted by a pipe subprocess on
// stderr. The executor reads these concurrently to provide real-time
// observability.
type StatusEvent struct {
	Status  string         `json:"status"`            // event type: progress, waiting, error
	Pipe    string         `json:"pipe"`              // pipe name
	TaskID  string         `json:"task_id,omitempty"` // graph task ID (empty for sequential)
	Message string         `json:"message"`           // human-readable description
	TS      int64          `json:"ts"`                // unix timestamp (seconds)
	Detail  map[string]any `json:"detail,omitempty"`  // type-specific structured data
}

// Status event type constants.
const (
	StatusProgress = "progress"
	StatusWaiting  = "waiting"
	StatusError    = "error"
)

// StatusSink is a callback that receives parsed status events from a
// subprocess. Analogous to the chunk sink for streaming text.
type StatusSink func(event StatusEvent)

// stderrResult holds the plain (non-structured) stderr text collected by
// readStderr after the goroutine completes.
type stderrResult struct {
	plain string
}

// readStderr reads lines from r concurrently, discriminating between status
// events, structured slog messages, and plain text. It is the streaming
// replacement for the batch forwardLogs function.
//
// For each line:
//   - JSON with "status" key → parsed as StatusEvent, forwarded to sink (if non-nil)
//   - JSON with "msg" key    → forwarded to logger (existing slog behavior)
//   - Everything else        → collected as plain stderr text
//
// readStderr sends one stderrResult on done when the reader is exhausted.
func readStderr(r io.Reader, logger *slog.Logger, pipeName string, sink StatusSink, done chan<- stderrResult) {
	var plainLines strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if line[0] != '{' {
			// Plain text — not JSON.
			plainLines.Write(line)
			plainLines.WriteByte('\n')
			continue
		}

		// Attempt to parse as a generic JSON object to discriminate.
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			// Starts with '{' but not valid JSON — treat as plain text.
			plainLines.Write(line)
			plainLines.WriteByte('\n')
			continue
		}

		// Priority 1: status event (has "status" key).
		if statusVal, ok := raw["status"].(string); ok && statusVal != "" {
			var event StatusEvent
			if err := json.Unmarshal(line, &event); err == nil {
				if sink != nil {
					sink(event)
				}
				continue
			}
			// Unmarshal failed — fall through to plain text.
		}

		// Priority 2: slog message (has "msg" key).
		if logger != nil {
			msg, _ := raw["msg"].(string)
			if msg != "" {
				var lvl slog.Level
				if levelStr, _ := raw["level"].(string); levelStr != "" {
					if err := lvl.UnmarshalText([]byte(levelStr)); err != nil {
						lvl = slog.LevelInfo
					}
				}
				attrs := []any{"pipe", pipeName}
				for k, v := range raw {
					if k == "time" || k == "level" || k == "msg" {
						continue
					}
					attrs = append(attrs, k, v)
				}
				logger.Log(context.Background(), lvl, msg, attrs...)
				continue
			}
		}

		// Not a recognizable JSON structure — treat as plain text.
		plainLines.Write(line)
		plainLines.WriteByte('\n')
	}

	done <- stderrResult{plain: plainLines.String()}
}

// NowUnix returns the current Unix timestamp in seconds. Exposed so that
// pipehost.StatusReporter can use the same clock variable in tests.
// Declared as a variable so tests can substitute it.
var NowUnix = func() int64 { return time.Now().Unix() }
