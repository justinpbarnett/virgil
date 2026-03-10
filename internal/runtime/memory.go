package runtime

import (
	"context"
	"sort"
	"time"

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
	SaveInvocation(pipe, signal, output string, contextIDs []string) error
}

// StoreMemoryInjector implements MemoryInjector backed by a Store.
type StoreMemoryInjector struct {
	store          *store.Store
	codebaseSearch func(ctx context.Context, query string, budget int) ([]envelope.MemoryEntry, error)
}

func NewStoreMemoryInjector(s *store.Store) *StoreMemoryInjector {
	return &StoreMemoryInjector{store: s}
}

// WithCodebaseSearch sets a function that performs live codebase searches for
// memory context entries of type "codebase".
func (m *StoreMemoryInjector) WithCodebaseSearch(fn func(ctx context.Context, query string, budget int) ([]envelope.MemoryEntry, error)) {
	m.codebaseSearch = fn
}

func (m *StoreMemoryInjector) InjectContext(env envelope.Envelope, cfg config.MemoryConfig) envelope.Envelope {
	if cfg.Disabled {
		return env
	}

	effective := cfg
	if effective.Budget == 0 && len(effective.Context) == 0 {
		effective = config.DefaultMemoryConfig()
	}

	query := envelope.ContentToText(env.Content, env.ContentType)
	remainingBudget := effective.Budget

	// Separate codebase context requests from store-backed requests
	var storeEntries []config.MemoryContextEntry
	var codebaseEntries []config.MemoryContextEntry
	for _, c := range effective.Context {
		if c.Type == "codebase" {
			codebaseEntries = append(codebaseEntries, c)
		} else {
			storeEntries = append(storeEntries, c)
		}
	}

	// Handle codebase entries via live search (half the budget each)
	if len(codebaseEntries) > 0 && m.codebaseSearch != nil {
		codebaseBudget := remainingBudget / 2
		remainingBudget -= codebaseBudget
		entries, err := m.codebaseSearch(context.Background(), query, codebaseBudget)
		if err == nil {
			env.Memory = append(env.Memory, entries...)
		}
	}

	// Handle store-backed entries
	if len(storeEntries) > 0 {
		requests := make([]store.ContextRequest, len(storeEntries))
		for i, c := range storeEntries {
			requests[i] = store.ContextRequest{Type: c.Type, Depth: c.Depth, Relations: c.Relations, Kind: c.Kind}
		}
		entries, err := m.store.RetrieveContext(query, requests, remainingBudget)
		if err == nil && len(entries) > 0 {
			env.Memory = append(env.Memory, entries...)
		}
	}

	return env
}

// StoreMemorySaver implements MemorySaver backed by a Store.
type StoreMemorySaver struct {
	store *store.Store
}

func NewStoreMemorySaver(s *store.Store) *StoreMemorySaver {
	return &StoreMemorySaver{store: s}
}

func (m *StoreMemorySaver) SaveInvocation(pipe, signal, output string, contextIDs []string) error {
	invID, err := m.store.SaveInvocation(pipe, signal, output)
	if err != nil {
		return err
	}

	if len(contextIDs) == 0 {
		return nil
	}

	now := time.Now()
	var edges []store.Edge

	// produced_by: invocation was produced using each context memory
	for _, ctxID := range contextIDs {
		if ctxID == invID {
			continue
		}
		edges = append(edges, store.Edge{
			SourceID:  invID,
			TargetID:  ctxID,
			Relation:  store.RelationProducedBy,
			Strength:  1.0,
			CreatedAt: now,
		})
	}

	// co_occurred: all pairs of context IDs with canonical (lexicographic) ordering
	sortedIDs := make([]string, len(contextIDs))
	copy(sortedIDs, contextIDs)
	sort.Strings(sortedIDs)

	for i := 0; i < len(sortedIDs); i++ {
		for j := i + 1; j < len(sortedIDs); j++ {
			edges = append(edges, store.Edge{
				SourceID:  sortedIDs[i],
				TargetID:  sortedIDs[j],
				Relation:  store.RelationCoOccurred,
				Strength:  1.0,
				CreatedAt: now,
			})
		}
	}

	if len(edges) == 0 {
		return nil
	}
	return m.store.CreateEdges(edges)
}
