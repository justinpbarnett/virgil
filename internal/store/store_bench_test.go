package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkRetrieveContext(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	for i := 0; i < 20; i++ {
		s.PutState("project", fmt.Sprintf("key%d", i), "working on benchmarks")
		s.SaveInvocation("chat", "example signal", "example response about Go")
	}

	requests := []ContextRequest{
		{Type: "working_state"},
		{Type: "topic_history"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.RetrieveContext("example query", requests, 500)
	}
}

func BenchmarkSearchInvocations(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	for i := 0; i < 20; i++ {
		s.SaveInvocation("chat", "example signal", "example response about Go")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.SearchInvocations("example", "", 10, time.Time{})
	}
}

func BenchmarkListAllState(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	for i := 0; i < 20; i++ {
		s.PutState("ns", fmt.Sprintf("key%d", i), "some state content")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.listAllState()
	}
}
