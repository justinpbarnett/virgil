package router

import (
	"context"
	"log/slog"
	"testing"

	"github.com/justinpbarnett/virgil/internal/parser"
)

// silentLogger returns a logger that discards all output, preventing
// log I/O from dominating benchmark timings.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkRouteExactMatch(b *testing.B) {
	r := NewRouter(testDefs(), silentLogger())
	defer r.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "check my calendar", parser.ParsedSignal{})
	}
}

func BenchmarkRouteKeyword(b *testing.B) {
	r := NewRouter(testDefs(), silentLogger())
	defer r.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "show my scheduling events for the next meeting", parser.ParsedSignal{})
	}
}

func BenchmarkRouteCategory(b *testing.B) {
	r := NewRouter(testDefs(), silentLogger())
	defer r.Close()
	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Action: "retrieve",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "what do you know", parsed)
	}
}

func BenchmarkRouteMiss(b *testing.B) {
	r := NewRouter(testDefs(), silentLogger())
	defer r.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "xyzzy completely unrecognized signal", parser.ParsedSignal{})
	}
}
