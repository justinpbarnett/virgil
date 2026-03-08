// Package router defines the interface for pipe and pipeline registration.
//
// Cloud deployments use the Registry interface to register additional pipes
// (e.g., Slack, Jira) alongside core pipes without modifying core code.
package router

import (
	pkgpipe "github.com/justinpbarnett/virgil/pkg/pipe"
)

// Registry is the interface for registering pipes and pipelines.
// The core implementation (internal/pipe.Registry) satisfies this interface.
type Registry interface {
	// Register adds a pipe with its definition and handler.
	Register(def pkgpipe.Definition, handler pkgpipe.Handler)

	// RegisterStream adds a streaming handler for a named pipe.
	RegisterStream(name string, handler pkgpipe.StreamHandler)

	// Definitions returns all registered pipe definitions.
	Definitions() []pkgpipe.Definition
}
