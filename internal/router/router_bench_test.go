package router

import (
	"context"
	"testing"

	"github.com/justinpbarnett/virgil/internal/parser"
)

func BenchmarkRouteExactMatch(b *testing.B) {
	r := NewRouter(testDefs(), nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "check my calendar", parser.ParsedSignal{})
	}
}

func BenchmarkRouteKeyword(b *testing.B) {
	r := NewRouter(testDefs(), nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "show my scheduling events for the next meeting", parser.ParsedSignal{})
	}
}

func BenchmarkRouteCategory(b *testing.B) {
	r := NewRouter(testDefs(), nil)
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
	r := NewRouter(testDefs(), nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(context.Background(), "xyzzy completely unrecognized signal", parser.ParsedSignal{})
	}
}
