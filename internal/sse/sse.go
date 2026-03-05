package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Event represents a server-sent event.
type Event struct {
	Type string
	Data string
}

// Reader reads SSE events from a stream.
type Reader struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
}

// NewReader creates an SSE reader. maxBuf sets the maximum line size in bytes.
func NewReader(body io.ReadCloser, maxBuf int) *Reader {
	scanner := bufio.NewScanner(body)
	if maxBuf > 0 {
		scanner.Buffer(make([]byte, 4096), maxBuf)
	}
	return &Reader{scanner: scanner, body: body}
}

// Next reads the next SSE event. Returns io.EOF at end of stream.
func (r *Reader) Next() (Event, error) {
	var eventType, data string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			data = after
		} else if line == "" && eventType != "" {
			return Event{Type: eventType, Data: data}, nil
		}
	}
	if err := r.scanner.Err(); err != nil {
		return Event{}, err
	}
	return Event{}, io.EOF
}

// Close closes the underlying reader.
func (r *Reader) Close() {
	r.body.Close()
}

// InitResponse sets SSE headers on w and returns the flusher.
// Returns false if the ResponseWriter does not support flushing.
func InitResponse(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()
	return flusher, true
}

// WriteEvent writes a raw SSE event and flushes.
func WriteEvent(w io.Writer, flusher http.Flusher, eventType string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	flusher.Flush()
}

// WriteJSON marshals v as JSON and writes it as an SSE event.
func WriteJSON(w io.Writer, flusher http.Flusher, eventType string, v any) {
	data, _ := json.Marshal(v)
	WriteEvent(w, flusher, eventType, data)
}

// FormatText formats a text string as an SSE event with a JSON text wrapper.
func FormatText(eventType, text string) string {
	escaped, _ := json.Marshal(text)
	return fmt.Sprintf("event: %s\ndata: {\"text\":%s}\n\n", eventType, escaped)
}
