package tools

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/justinpbarnett/virgil/internal"
)

// Registry holds all registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*internal.Tool
	order []string
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*internal.Tool)}
}

// Register adds a tool to the registry. Panics if the name is already taken
// (a programming error, not a runtime condition).
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
