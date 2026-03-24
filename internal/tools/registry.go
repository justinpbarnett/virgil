package tools

import (
	"fmt"
	"sync"

	"github.com/justinpbarnett/virgil/internal"
)

// Registry holds all registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*internal.Tool
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
	if _, exists := r.tools[t.Name]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name))
	}
	r.tools[t.Name] = t
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) *internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Definitions returns all registered tools (for passing to the model).
func (r *Registry) Definitions() []*internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*internal.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// DefinitionsFor returns tools matching the given name subset.
func (r *Registry) DefinitionsFor(names []string) []*internal.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*internal.Tool, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}
