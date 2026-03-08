// Package memory defines the interface for swappable memory backends.
//
// The default implementation uses SQLite (internal/store). Cloud deployments
// can implement this interface with Postgres, DynamoDB, or any other store.
package memory

import "time"

// Backend is the interface for memory storage. Implement this to swap
// the default SQLite store for a different backend.
type Backend interface {
	// Save stores an explicit memory entry with the given tags.
	Save(content string, tags []string) error

	// Search finds memory entries matching a query, returning up to limit results.
	Search(query string, limit int, sort string) ([]Entry, error)

	// PutState stores a working state entry.
	PutState(namespace, key, content string) error

	// GetState retrieves a working state entry.
	GetState(namespace, key string) (string, bool, error)

	// DeleteState removes a working state entry.
	DeleteState(namespace, key string) error

	// ListState returns all working state entries in a namespace.
	ListState(namespace string) ([]StateEntry, error)

	// SaveInvocation records a pipe invocation for history.
	SaveInvocation(pipe, signal, output string) (string, error)

	// Close releases resources held by the backend.
	Close() error
}

// Entry represents an explicit memory entry.
type Entry struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StateEntry represents a working state entry.
type StateEntry struct {
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}
