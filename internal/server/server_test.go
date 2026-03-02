package server

import (
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

func testServer(t *testing.T) *Server {
	t.Helper()

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{
		Name:     "chat",
		Category: "general",
		Triggers: pipe.Triggers{Keywords: []string{"chat"}},
	}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("chat", "respond")
		out.Content = "Hello! I'm Virgil."
		out.ContentType = "text"
		return out
	})

	defs := reg.Definitions()
	rt := router.NewRouter(defs, nil)
	p := parser.New(parser.LoadVocabulary(config.VocabularyConfig{}))
	pl := planner.New(config.TemplatesConfig{}, nil)
	run := runtime.New(reg, nil)

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "localhost", Port: 7890},
	}

	logger := slog.Default()

	return New(cfg, rt, p, pl, run, reg, logger)
}

func TestHealthEndpoint(t *testing.T) {
	s := testServer(t)
	handler := s.Handler()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("expected 'ok' in response, got %s", w.Body.String())
	}
}

func TestSignalEndpoint(t *testing.T) {
	s := testServer(t)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello there"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "content") {
		t.Errorf("expected envelope in response, got %s", w.Body.String())
	}
}

func TestSignalEndpointEmptyBody(t *testing.T) {
	s := testServer(t)
	handler := s.Handler()

	body := strings.NewReader(`{"text":""}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSignalEndpointInvalidJSON(t *testing.T) {
	s := testServer(t)
	handler := s.Handler()

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest("POST", "/signal", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
