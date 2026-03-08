// Package store re-exports the memory store types for use by external modules.
//
// The concrete implementation lives in internal/store. This package provides
// type aliases so that code outside the module (e.g., virgil-cloud) can
// access the Store and Todo types without importing internal packages.
package store

import internalstore "github.com/justinpbarnett/virgil/internal/store"

// Store is the SQLite-backed memory and todo store.
type Store = internalstore.Store

// Todo represents a task/todo item.
type Todo = internalstore.Todo

// Open creates or opens a SQLite database at the given path.
func Open(path string) (*Store, error) {
	return internalstore.Open(path)
}
