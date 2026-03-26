package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/observe"
)

// Registry holds all registered tools.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]*internal.Tool
	order  []string
	events *observe.EventLog
}

// NewRegistry creates an empty tool registry. Pass a non-nil EventLog to
// enable automatic execution logging for every registered tool.
func NewRegistry(events *observe.EventLog) *Registry {
	return &Registry{tools: make(map[string]*internal.Tool), events: events}
}

// Register adds a tool to the registry. If the registry has an EventLog,
// Execute is transparently wrapped to record timing, inputs, outputs, and
// errors keyed to the trace carried in the context. Panics if the name is
// already taken (a programming error, not a runtime condition).
func (r *Registry) Register(t *internal.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.Name == "" {
		panic("tool registered with empty name")
	}
	if t.Execute == nil {
		panic(fmt.Sprintf("tool %q registered with nil Execute", t.Name))
	}
	if _, exists := r.tools[t.Name]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name))
	}
	if r.events != nil {
		t.Execute = r.wrapExecute(t.Name, t.Execute)
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) *internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tool names in registration order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Definitions returns all registered tools in registration order (for passing to the model).
func (r *Registry) Definitions() []*internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*internal.Tool, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.tools[name])
	}
	return result
}

// DefinitionsFor returns tools matching the given name subset.
// Logs a warning for any requested tool names not found in the registry.
func (r *Registry) DefinitionsFor(names []string) []*internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*internal.Tool, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		} else {
			slog.Warn("tool not found in registry", "name", name)
		}
	}
	return result
}

func (r *Registry) wrapExecute(name string, fn func(context.Context, map[string]any) (*internal.ToolResult, error)) func(context.Context, map[string]any) (*internal.ToolResult, error) {
	component := "tool:" + name
	return func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
		start := time.Now()
		result, err := fn(ctx, params)
		duration := time.Since(start)

		var inputStr string
		if len(params) > 0 {
			b, merr := json.Marshal(params)
			if merr != nil {
				slog.Warn("failed to marshal tool params", "tool", name, "err", merr)
				inputStr = fmt.Sprintf("%v", params)
			} else {
				inputStr = string(b)
			}
		}

		traceID, parentSpan := observe.TraceFromContext(ctx)
		ev := &internal.Event{
			Component:  component,
			SpanID:     observe.GenerateSpanID(),
			TraceID:    traceID,
			ParentSpan: parentSpan,
			DurationMs: duration.Milliseconds(),
			Input:      inputStr,
		}

		if err != nil {
			ev.Action = "error"
			ev.Error = err.Error()
			slog.Error("tool execution failed", "tool", name, "err", err)
		} else {
			ev.Action = "invoke"
			if result != nil {
				ev.Output = result.Format()
			}
		}

		if logErr := r.events.Log(ev); logErr != nil {
			slog.Error("failed to log tool event", "tool", name, "err", logErr)
		}

		return result, err
	}
}
