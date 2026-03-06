package pipe

import "sync"

type Registry struct {
	mu                  sync.RWMutex
	handlers            map[string]Handler
	streamHandlers      map[string]StreamHandler
	definitions         map[string]Definition
	persistentProcesses []*PersistentProcess
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

// RegisterPersistent starts a PersistentProcess for the given pipe and
// registers both its sync handler and stream handler. The stream handler
// is always registered to support context cancellation for all pipes.
// The process is tracked for shutdown via Shutdown().
func (r *Registry) RegisterPersistent(def Definition, cfg SubprocessConfig, _ bool) error {
	proc := NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		return err
	}

	r.mu.Lock()
	r.handlers[def.Name] = proc.Handler()
	// Always register a stream handler for persistent pipes. Even non-streaming
	// pipes benefit from the StreamHandler's context cancellation support —
	// without it, sync Handler calls block until the subprocess responds and
	// cannot be cancelled by the caller.
	r.streamHandlers[def.Name] = proc.StreamHandler()
	r.definitions[def.Name] = def
	r.persistentProcesses = append(r.persistentProcesses, proc)
	r.mu.Unlock()

	return nil
}

// Shutdown stops all persistent pipe processes. Safe to call multiple times.
func (r *Registry) Shutdown() {
	r.mu.Lock()
	procs := r.persistentProcesses
	r.persistentProcesses = nil
	r.mu.Unlock()

	for _, proc := range procs {
		proc.Stop()
	}
}
