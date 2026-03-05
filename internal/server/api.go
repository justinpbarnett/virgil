package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/sse"
)

const ackSystemPrompt = `You are acknowledging a request. Write 1-2 sentences max.
Be specific — reference what the user asked for. If context is provided,
use relevant details to personalize. Use action phrases like "Sure, getting
started on..." or "On it —". Do not perform the task, just acknowledge it.`

func buildAckUserPrompt(signal string, memories []envelope.MemoryEntry) string {
	var sb strings.Builder
	sb.WriteString("Request: ")
	sb.WriteString(signal)
	if len(memories) > 0 {
		sb.WriteString("\n\nContext:\n")
		for _, m := range memories {
			sb.WriteString(m.Content)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}


type signalRequest struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
}

type voiceStatus struct {
	Recording bool   `json:"recording"`
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
}

func buildSeed(req signalRequest) envelope.Envelope {
	seed := envelope.New("signal", "input")
	seed.Content = req.Text
	seed.ContentType = envelope.ContentText
	seed.Args["signal"] = req.Text
	if req.Model != "" {
		seed.Args[envelope.FlagModelOverride] = req.Model
	}
	if cwd, err := os.Getwd(); err == nil {
		seed.Args["cwd"] = cwd
	}
	return seed
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var req signalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error("decode failed", "error", err)
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		s.logger.Error("empty signal")
		http.Error(w, `{"error":"text field is required"}`, http.StatusBadRequest)
		return
	}

	s.logger.Info("signal received")

	parsed := s.parser.Parse(req.Text)

	// SSE streaming path — start response before routing so the client
	// gets the connection immediately (routing can take seconds for layer-4).
	if r.Header.Get("Accept") == envelope.SSEContentType {
		s.handleSSE(w, r, req, parsed)
		return
	}

	// Synchronous path — route first (deterministic, <1ms), then execute.
	seed := buildSeed(req)
	route := s.router.Route(r.Context(), req.Text, parsed)
	plan := s.planner.Plan(route, parsed)

	execSeed := seed
	if len(plan.Steps) == 1 {
		memCh := s.runtime.PrefetchMemory(seed, plan.Steps[0].Pipe)
		execSeed = <-memCh
		plan.SkipFirstMemoryInjection = true
	}

	result := s.runtime.Execute(plan, execSeed)

	s.logger.Info("signal complete", "duration", time.Since(start).String())

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, req signalRequest, parsed parser.ParsedSignal) {
	flusher, ok := sse.InitResponse(w)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	// Route first — all layers are deterministic and complete in <1ms.
	seed := buildSeed(req)
	route := s.router.Route(r.Context(), req.Text, parsed)
	plan := s.planner.Plan(route, parsed)

	if len(plan.Steps) > 0 {
		sse.WriteJSON(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": plan.Steps[0].Pipe, "layer": route.Layer})
	}

	// Prefetch memory for the correct pipe.
	execSeed := seed
	if len(plan.Steps) == 1 {
		memCh := s.runtime.PrefetchMemory(seed, plan.Steps[0].Pipe)
		execSeed = <-memCh
		plan.SkipFirstMemoryInjection = true
	}

	var mu sync.Mutex
	ackDone := make(chan struct{}, 1)

	pipeIsAIBacked := len(plan.Steps) > 0 && s.config.Pipes[plan.Steps[0].Pipe].Prompts.System != ""
	if pipeIsAIBacked && s.ackProvider != nil {
		go func() {
			user := buildAckUserPrompt(req.Text, execSeed.Memory)
			_, _ = s.ackProvider.CompleteStream(r.Context(), ackSystemPrompt, user, func(chunk string) {
				mu.Lock()
				w.Write([]byte(sse.FormatText(envelope.SSEEventAck, chunk))) //nolint:errcheck
				flusher.Flush()
				mu.Unlock()
			})
			ackDone <- struct{}{}
		}()
	} else {
		ackDone <- struct{}{}
	}

	sink := func(event runtime.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch event.Type {
		case envelope.SSEEventChunk:
			w.Write([]byte(sse.FormatText(envelope.SSEEventChunk, event.Data))) //nolint:errcheck
		case envelope.SSEEventStep:
			sse.WriteEvent(w, flusher, envelope.SSEEventStep, []byte(event.Data))
		}
		flusher.Flush()
	}

	result := s.runtime.ExecuteStream(r.Context(), plan, execSeed, sink)
	<-ackDone
	mu.Lock()
	sse.WriteJSON(w, flusher, envelope.SSEEventDone, result)
	mu.Unlock()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handlePipes(w http.ResponseWriter, _ *http.Request) {
	defs := s.registry.Definitions()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(defs)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := map[string]any{
		"status":     "ok",
		"pipes":      len(s.registry.Definitions()),
		"started_at": s.startedAt.Format(time.RFC3339),
		"uptime":     time.Since(s.startedAt).String(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func marshalJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

var emptyJSON = []byte("{}")

func (s *Server) handleVoiceInputPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	s.voiceInput.broadcast(req.Text)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceInputSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceInput, 8, envelope.SSEEventVoiceInput, func(text string) []byte {
		return marshalJSON(map[string]string{"text": text})
	})
}

func (s *Server) handleVoiceSpeakPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text     string `json:"text"`
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	s.voiceSpeak.broadcast(voiceSpeakMsg{Text: req.Text, Priority: req.Priority})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceSpeakSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceSpeak, 8, envelope.SSEEventVoiceSpeak, func(msg voiceSpeakMsg) []byte {
		return marshalJSON(map[string]string{"text": msg.Text, "priority": msg.Priority})
	})
}

func (s *Server) handleVoiceCyclePost(w http.ResponseWriter, r *http.Request) {
	s.voiceCycle.broadcast(struct{}{})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceCycleSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceCycle, 4, envelope.SSEEventVoiceCycle, func(_ struct{}) []byte {
		return emptyJSON
	})
}

func (s *Server) handleVoiceStopPost(w http.ResponseWriter, r *http.Request) {
	s.voiceStop.broadcast(struct{}{})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceStopSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceStop, 4, envelope.SSEEventVoiceStop, func(_ struct{}) []byte {
		return emptyJSON
	})
}

func (s *Server) handleVoiceStatusPost(w http.ResponseWriter, r *http.Request) {
	var vs voiceStatus
	if err := json.NewDecoder(r.Body).Decode(&vs); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	s.lastVoiceStatus.Lock()
	s.lastVoiceStatus.val = &vs
	s.lastVoiceStatus.Unlock()
	s.voiceStatus.broadcast(vs)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceStatusSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sse.InitResponse(w)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	ch := s.voiceStatus.subscribe(8)
	defer s.voiceStatus.unsubscribe(ch)

	s.lastVoiceStatus.Lock()
	last := s.lastVoiceStatus.val
	s.lastVoiceStatus.Unlock()
	if last != nil {
		sse.WriteJSON(w, flusher, envelope.SSEEventVoiceStatus, *last)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case vs := <-ch:
			sse.WriteJSON(w, flusher, envelope.SSEEventVoiceStatus, vs)
		}
	}
}
