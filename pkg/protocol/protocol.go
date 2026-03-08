// Package protocol defines the subprocess communication protocol for pipes.
//
// Pipes run as separate binaries. The parent process sends a SubprocessRequest
// as JSON on stdin and reads the response (an Envelope or streaming chunks)
// from stdout.
package protocol

import (
	pkgenv "github.com/justinpbarnett/virgil/pkg/envelope"
)

// SubprocessRequest is the JSON payload sent to a pipe subprocess on stdin.
type SubprocessRequest struct {
	Envelope pkgenv.Envelope   `json:"Envelope"`
	Flags    map[string]string `json:"Flags"`
	Stream   bool              `json:"Stream"`
}

// SubprocessChunk is a single line of streaming output from a subprocess.
// Either Chunk is set (partial text) or Envelope is set (final result).
type SubprocessChunk struct {
	Chunk    string          `json:"chunk,omitempty"`
	Envelope *pkgenv.Envelope `json:"envelope,omitempty"`
}
