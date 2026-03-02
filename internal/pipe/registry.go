package pipe

import "sync"

type Registry struct {
	mu             sync.RWMutex
	handlers       map[string]Handler
	streamHandlers map[string]StreamHandler
	definitions    map[string]Definition
}

func NewRegistry() *Registry {
	return &Registry{
		handlers:       make(map[string]Handler),
		streamHandlers: make(map[string]StreamHandler),
		definitions:    make(map[string]Definition),
	}
}

func (r *Registry) Register(def Definition, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[def.Name] = handler
	r.definitions[def.Name] = def
}

func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

func (r *Registry) GetDefinition(name string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.definitions[name]
	return d, ok
}

func (r *Registry) RegisterStream(name string, handler StreamHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamHandlers[name] = handler
}

func (r *Registry) GetStream(name string) (StreamHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.streamHandlers[name]
	return h, ok
}

func (r *Registry) Definitions() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]Definition, 0, len(r.definitions))
	for _, d := range r.definitions {
		defs = append(defs, d)
	}
	return defs
}
