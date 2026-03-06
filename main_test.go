package main

import (
	"testing"

	"github.com/justinpbarnett/virgil/internal/version"
)

func TestMainVersion(t *testing.T) {
	// Ensure version package integration works
	v := version.Version()
	if v == "" {
		t.Error("Version integration failed")
	}
}