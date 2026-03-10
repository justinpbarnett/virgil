// Package store re-exports the memory store types for use by external modules.
//
// The concrete implementation lives in internal/store. This package provides
// type aliases so that code outside the module (e.g., virgil-cloud) can
// access the Store and Memory types without importing internal packages.
package store

import internalstore "github.com/justinpbarnett/virgil/internal/store"

// Store is the SQLite-backed memory store.
type Store = internalstore.Store

// Memory is the unified memory record.
type Memory = internalstore.Memory

// Edge represents a typed relationship between two memory entries.
type Edge = internalstore.Edge

// Kind constants for memory entries.
const (
	KindExplicit     = internalstore.KindExplicit
	KindWorkingState = internalstore.KindWorkingState
	KindInvocation   = internalstore.KindInvocation
	KindTodo         = internalstore.KindTodo
	KindReminder     = internalstore.KindReminder
	KindGoal         = internalstore.KindGoal
	KindSummary      = internalstore.KindSummary
)

// Relation constants for memory edges.
const (
	RelationCoOccurred  = internalstore.RelationCoOccurred
	RelationProducedBy  = internalstore.RelationProducedBy
	RelationRefinedFrom = internalstore.RelationRefinedFrom
)

// Open creates or opens a SQLite database at the given path.
func Open(path string) (*Store, error) {
	return internalstore.Open(path)
}
