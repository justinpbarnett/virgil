package envelope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"
)

// Severity constants for EnvelopeError.
const (
	SeverityFatal = "fatal"
	SeverityError = "error"
	SeverityWarn  = "warn"
)

// ContentType constants.
const (
	ContentText       = "text"
	ContentList       = "list"
	ContentStructured = "structured"
	ContentBinary     = "binary"
)

// SSE constants shared between server and client.
const (
	SSEEventChunk  = "chunk"
	SSEEventDone   = "done"
	SSEEventStep   = "step"
	SSEEventRoute  = "route"
	SSEContentType = "text/event-stream"

	SSEEventVoiceStatus = "voice_status"
	SSEEventVoiceInput  = "voice_input"
	SSEEventVoiceSpeak  = "voice_speak"
	SSEEventVoiceCycle  = "voice_cycle"
	SSEEventVoiceStop   = "voice_stop"
)

// Voice priority constants for speak events.
const (
	VoicePriorityAnnouncement = "announcement"
	VoicePriorityResponse     = "response"
)

// FlagModelOverride is the envelope Args key for overriding the model on a per-request basis.
const FlagModelOverride = "_model"

type MemoryEntry struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// EnvelopeUsage carries token counts and computed cost for a provider API call.
// It is nil when no usage data is available (e.g., Claude CLI, unknown model).
type EnvelopeUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Model        string  `json:"model"`
	Cost         float64 `json:"cost"`
}

type Envelope struct {
	Pipe        string            `json:"pipe"`
	Action      string            `json:"action"`
	Args        map[string]string `json:"args"`
	Timestamp   time.Time         `json:"timestamp"`
	Duration    time.Duration     `json:"duration"`
	Content     any               `json:"content"`
	ContentType string            `json:"content_type"`
	Error       *EnvelopeError    `json:"error"`
	Memory      []MemoryEntry     `json:"memory,omitempty"`
	Usage       *EnvelopeUsage    `json:"usage,omitempty"`
}

type EnvelopeError struct {
	Message   string `json:"message"`
	Severity  string `json:"severity"`
	Retryable bool   `json:"retryable"`
}

func New(pipe, action string) Envelope {
	return Envelope{
		Pipe:      pipe,
		Action:    action,
		Args:      make(map[string]string),
		Timestamp: time.Now(),
	}
}

// NewFatalError creates an envelope representing a fatal error from a named pipe.
func NewFatalError(pipe, message string) Envelope {
	out := New(pipe, "error")
	out.Error = FatalError(message)
	return out
}

// FatalError returns a non-retryable fatal EnvelopeError.
func FatalError(message string) *EnvelopeError {
	return &EnvelopeError{
		Message:  message,
		Severity: SeverityFatal,
	}
}

// WarnError returns a warning-level EnvelopeError.
func WarnError(message string) *EnvelopeError {
	return &EnvelopeError{
		Message:  message,
		Severity: SeverityWarn,
	}
}

// NewRetryableError creates an envelope representing a retryable error from a named pipe.
func NewRetryableError(pipe, message string) Envelope {
	out := New(pipe, "error")
	out.Error = &EnvelopeError{
		Message:   message,
		Severity:  SeverityError,
		Retryable: true,
	}
	return out
}

// ClassifyError wraps err into an EnvelopeError, marking timeouts as retryable.
func ClassifyError(prefix string, err error) *EnvelopeError {
	if isTimeout(err) {
		return &EnvelopeError{
			Message:   fmt.Sprintf("%s: %v", prefix, err),
			Severity:  SeverityError,
			Retryable: true,
		}
	}
	return FatalError(fmt.Sprintf("%s: %v", prefix, err))
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func ContentToText(content any, contentType string) string {
	if content == nil {
		return ""
	}

	switch contentType {
	case ContentText:
		s, ok := content.(string)
		if ok {
			return s
		}
		return fmt.Sprintf("%v", content)

	case ContentList:
		return renderList(content)

	case ContentStructured:
		return renderStructured(content)

	case ContentBinary:
		return "[binary content]"

	default:
		return ""
	}
}

func renderList(content any) string {
	rv := reflect.ValueOf(content)
	if rv.Kind() != reflect.Slice {
		return fmt.Sprintf("%v", content)
	}

	var lines []string
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i).Interface()
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, renderItem(item)))
	}
	return strings.Join(lines, "\n")
}

func renderItem(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		return renderMap(v)
	default:
		rv := reflect.ValueOf(item)
		if rv.Kind() == reflect.Struct {
			return renderStruct(rv)
		}
		// Handle pointer to struct
		if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
			return renderStruct(rv.Elem())
		}
		return fmt.Sprintf("%v", item)
	}
}

func renderMap(m map[string]any) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	return strings.Join(parts, ", ")
}

func renderStructFields(rv reflect.Value) []string {
	rt := rv.Type()
	var parts []string
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			if tagName, _, _ := strings.Cut(tag, ","); tagName != "" && tagName != "-" {
				name = tagName
			}
		}
		parts = append(parts, fmt.Sprintf("%s: %v", name, rv.Field(i).Interface()))
	}
	return parts
}

func renderStruct(rv reflect.Value) string {
	return strings.Join(renderStructFields(rv), ", ")
}

func renderStructured(content any) string {
	switch v := content.(type) {
	case map[string]any:
		var lines []string
		for k, val := range v {
			lines = append(lines, fmt.Sprintf("%s: %v", k, val))
		}
		return strings.Join(lines, "\n")
	default:
		rv := reflect.ValueOf(content)
		if rv.Kind() == reflect.Struct {
			return renderStructLines(rv)
		}
		// Try JSON marshaling as last resort
		b, err := json.Marshal(content)
		if err != nil {
			return fmt.Sprintf("%v", content)
		}
		return string(b)
	}
}

func renderStructLines(rv reflect.Value) string {
	return strings.Join(renderStructFields(rv), "\n")
}
