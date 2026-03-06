package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

// flushRecorder wraps httptest.ResponseRecorder to implement http.Flusher,
// which is required by handleSSE.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (r *flushRecorder) Flush() {
	r.flushed++
}

// testSSEServer builds a Server with both sync and streaming chat handlers and
// a separate "calendar" pipe with an exact-match trigger.
func testSSEServer(t *testing.T) *Server {
	t.Helper()

	reg := pipe.NewRegistry()

	// Chat pipe — sync + stream
	reg.Register(pipe.Definition{
		Name:     "chat",
		Category: "general",
		Triggers: pipe.Triggers{Keywords: []string{"chat", "hello"}},
	}, func(input envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("chat", "respond")
		out.Content = "chat response"
		out.ContentType = envelope.ContentText
		return out
	})
	reg.RegisterStream("chat", func(_ context.Context, input envelope.Envelope, _ map[string]string, sink func(string)) envelope.Envelope {
		sink("token1")
		sink("token2")
		out := envelope.New("chat", "respond")
		out.Content = "chat response"
		out.ContentType = envelope.ContentText
		return out
	})

	// Calendar pipe — sync only, triggered by exact match
	reg.Register(pipe.Definition{
		Name:     "calendar",
		Category: "time",
		Triggers: pipe.Triggers{
			Exact:    []string{"check my calendar"},
			Keywords: []string{"calendar", "schedule"},
		},
	}, func(input envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("calendar", "respond")
		out.Content = "calendar response"
		out.ContentType = envelope.ContentText
		return out
	})

	defs := reg.Definitions()
	rt := router.NewRouter(defs, nil)
	p := parser.New(parser.LoadVocabulary(config.VocabularyConfig{}))
	pl := planner.New(config.TemplatesConfig{}, nil, nil)
	run := runtime.New(reg, nil, nil)

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "localhost", Port: 7890},
	}

	return New(Deps{
		Config:   cfg,
		Router:   rt,
		Parser:   p,
		Planner:  pl,
		Runtime:  run,
		Registry: reg,
		Logger:   slog.Default(),
	})
}

// TestSSEChatRouteProducesChunks verifies that a chat-bound SSE request
// receives chunk events streamed from the chat pipe.
func TestSSEChatRouteProducesChunks(t *testing.T) {
	s := testSSEServer(t)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()

	if !strings.Contains(resp, "event: chunk") {
		t.Errorf("expected SSE chunk events in response, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected SSE done event in response, got:\n%s", resp)
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestSSEChatRouteEmitsRouteEvent verifies that the route event is sent.
func TestSSEChatRouteEmitsRouteEvent(t *testing.T) {
	s := testSSEServer(t)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()
	if !strings.Contains(resp, "event: route") {
		t.Errorf("expected SSE route event in response, got:\n%s", resp)
	}
}

// TestSSENonChatRouteCancelsSpeculativeStream verifies that when routing
// resolves to a non-chat pipe (calendar), the correct pipe is used and the
// response contains a done event (not stuck waiting on the cancelled spec stream).
func TestSSENonChatRouteCancelsSpeculativeStream(t *testing.T) {
	s := testSSEServer(t)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"check my calendar"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()

	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected SSE done event in response, got:\n%s", resp)
	}
	// The done envelope should contain calendar pipe output, not chat.
	if strings.Contains(resp, "chat response") {
		t.Errorf("expected calendar response, got chat response in:\n%s", resp)
	}
	if !strings.Contains(resp, "calendar") {
		t.Errorf("expected calendar pipe result in done event, got:\n%s", resp)
	}
}

// TestSSERequestCompletes verifies the request terminates cleanly and does not
// block. If goroutines leaked the test would hang (caught by -timeout flag).
func TestSSERequestCompletes(t *testing.T) {
	s := testSSEServer(t)
	handler := s.Handler()

	for _, text := range []string{"hello", "check my calendar", "xyzzy unmatched"} {
		body := strings.NewReader(`{"text":"` + text + `"}`)
		req := httptest.NewRequest("POST", "/signal", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")

		w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
		handler.ServeHTTP(w, req)

		if !strings.Contains(w.Body.String(), "event: done") {
			t.Errorf("signal %q: expected done event, got:\n%s", text, w.Body.String())
		}
	}
}
