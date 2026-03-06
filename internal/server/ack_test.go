package server

import (
	"context"
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
	"log/slog"
)

// mockStreamingProvider is a StreamingProvider that emits a fixed ack chunk.
type mockStreamingProvider struct {
	chunks []string
}

func (m *mockStreamingProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return strings.Join(m.chunks, ""), nil
}

func (m *mockStreamingProvider) CompleteStream(_ context.Context, _, _ string, onChunk func(string)) (string, error) {
	for _, c := range m.chunks {
		onChunk(c)
	}
	return strings.Join(m.chunks, ""), nil
}

// testAckServer builds a Server with an ackProvider and an AI-backed "chat" pipe
// (with a system prompt in config.Pipes) plus a deterministic "calendar" pipe.
// Pass nil to test graceful degradation with no ack provider.
func testAckServer(t *testing.T, ackProvider *mockStreamingProvider) *Server {
	t.Helper()

	reg := pipe.NewRegistry()

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
		sink("real-token")
		out := envelope.New("chat", "respond")
		out.Content = "chat response"
		out.ContentType = envelope.ContentText
		return out
	})

	reg.Register(pipe.Definition{
		Name:     "calendar",
		Category: "time",
		Triggers: pipe.Triggers{
			Exact:    []string{"check my calendar"},
			Keywords: []string{"calendar"},
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
		Pipes: map[string]config.PipeConfig{
			"chat": {
				Name: "chat",
				Prompts: config.PromptsConfig{
					System: "You are a helpful assistant.",
				},
			},
			// calendar has no system prompt — deterministic
			"calendar": {
				Name: "calendar",
			},
		},
	}

	s := &Server{
		config:      cfg,
		router:      rt,
		parser:      p,
		planner:     pl,
		runtime:     run,
		registry:    reg,
		logger:      slog.Default(),
		voiceStatus: newBroker[voiceStatus](),
		voiceInput:  newBroker[string](),
		voiceSpeak:  newBroker[voiceSpeakMsg](),
		voiceCycle:  newBroker[struct{}](),
		voiceStop:   newBroker[struct{}](),
	}
	// Avoid interface nil trap: only set ackProvider if the concrete value is non-nil.
	if ackProvider != nil {
		s.ackProvider = ackProvider
	}
	return s
}

func TestAckEventsForAIBackedPipe(t *testing.T) {
	ack := &mockStreamingProvider{chunks: []string{"On it — "}}
	s := testAckServer(t, ack)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()
	if !strings.Contains(resp, "event: ack") {
		t.Errorf("expected ack event for AI-backed pipe, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected done event in response, got:\n%s", resp)
	}
}

func TestNoAckForDeterministicPipe(t *testing.T) {
	ack := &mockStreamingProvider{chunks: []string{"On it — "}}
	s := testAckServer(t, ack)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"check my calendar"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()
	if strings.Contains(resp, "event: ack") {
		t.Errorf("expected no ack event for deterministic pipe, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected done event in response, got:\n%s", resp)
	}
}

func TestNilAckProviderGracefulDegradation(t *testing.T) {
	s := testAckServer(t, nil) // nil ackProvider
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()
	if strings.Contains(resp, "event: ack") {
		t.Errorf("expected no ack event with nil provider, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected done event in response, got:\n%s", resp)
	}
}

// TestAckAndChunkEventsBothPresent verifies that both ack and chunk events appear
// in the stream when an AI-backed pipe is invoked with an ack provider configured.
// In production the ack arrives first due to natural timing (ack ~200ms vs pipeline
// ~2-8s), but the ordering is not a hard code guarantee in unit tests.
func TestAckAndChunkEventsBothPresent(t *testing.T) {
	ack := &mockStreamingProvider{chunks: []string{"On it — "}}
	s := testAckServer(t, ack)
	handler := s.Handler()

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	resp := w.Body.String()
	if !strings.Contains(resp, "event: ack") {
		t.Errorf("expected ack event in response, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: chunk") {
		t.Errorf("expected chunk event in response, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: done") {
		t.Errorf("expected done event in response, got:\n%s", resp)
	}
	if !strings.Contains(resp, "On it") {
		t.Errorf("expected ack text in response, got:\n%s", resp)
	}
}
