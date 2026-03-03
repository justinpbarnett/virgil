package runtime

import (
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/store"
)

// MemoryInjector pre-loads relevant memory context into an envelope before a pipe runs.
type MemoryInjector interface {
	InjectContext(env envelope.Envelope, cfg config.MemoryConfig) envelope.Envelope
}

// MemorySaver records a pipe invocation after successful terminal execution.
type MemorySaver interface {
	SaveInvocation(pipe, signal, output string) error
}

// StoreMemoryInjector implements MemoryInjector backed by a Store.
type StoreMemoryInjector struct {
	store *store.Store
}

func NewStoreMemoryInjector(s *store.Store) *StoreMemoryInjector {
	return &StoreMemoryInjector{store: s}
}

func (m *StoreMemoryInjector) InjectContext(env envelope.Envelope, cfg config.MemoryConfig) envelope.Envelope {
	if cfg.Disabled {
		return env
	}

	effective := cfg
	if effective.Budget == 0 && len(effective.Context) == 0 {
		effective = config.DefaultMemoryConfig()
	}

	requests := make([]store.ContextRequest, len(effective.Context))
	for i, c := range effective.Context {
		requests[i] = store.ContextRequest{Type: c.Type, Depth: c.Depth}
	}

	query := envelope.ContentToText(env.Content, env.ContentType)
	entries, err := m.store.RetrieveContext(query, requests, effective.Budget)
	if err != nil || len(entries) == 0 {
		return env
	}

	env.Memory = append(env.Memory, entries...)
	return env
}

// StoreMemorySaver implements MemorySaver backed by a Store.
type StoreMemorySaver struct {
	store *store.Store
}

func NewStoreMemorySaver(s *store.Store) *StoreMemorySaver {
	return &StoreMemorySaver{store: s}
}

func (m *StoreMemorySaver) SaveInvocation(pipe, signal, output string) error {
	return m.store.SaveInvocation(pipe, signal, output)
}
