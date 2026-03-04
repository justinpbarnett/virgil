package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type signalRequest struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
}

type voiceStatus struct {
	Recording bool   `json:"recording"`
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
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

	// Parse → Route → Plan → Execute
	parsed := s.parser.Parse(req.Text)
	route := s.router.Route(r.Context(), req.Text, parsed)

	s.logger.Debug("signal parsed",
		"verb", parsed.Verb,
		"type", parsed.Type,
		"source", parsed.Source,
		"pipe", route.Pipe,
		"layer", route.Layer,
		"confidence", route.Confidence,
	)

	plan := s.planner.Plan(route, parsed)

	seed := envelope.New("signal", "input")
	seed.Content = req.Text
	seed.ContentType = envelope.ContentText
	seed.Args["signal"] = req.Text
	if req.Model != "" {
		seed.Args[envelope.FlagModelOverride] = req.Model
	}

	// SSE streaming path
	if r.Header.Get("Accept") == envelope.SSEContentType {
		s.handleSSE(w, r, plan, seed, route.Layer)
		return
	}

	result := s.runtime.Execute(plan, seed)

	s.logger.Info("signal complete", "duration", time.Since(start).String())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, plan runtime.Plan, seed envelope.Envelope, routeLayer int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	if len(plan.Steps) > 0 {
		routeData, _ := json.Marshal(map[string]any{"pipe": plan.Steps[0].Pipe, "layer": routeLayer})
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventRoute, routeData)
		flusher.Flush()
	}

	sink := func(event runtime.StreamEvent) {
		switch event.Type {
		case envelope.SSEEventChunk:
			escaped, _ := json.Marshal(event.Data)
			fmt.Fprintf(w, "event: %s\ndata: {\"text\":%s}\n\n", envelope.SSEEventChunk, escaped)
		case envelope.SSEEventStep:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventStep, event.Data)
		}
		flusher.Flush()
	}

	result := s.runtime.ExecuteStream(r.Context(), plan, seed, sink)

	doneData, _ := json.Marshal(result)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventDone, doneData)
	flusher.Flush()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handlePipes(w http.ResponseWriter, _ *http.Request) {
	defs := s.registry.Definitions()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(defs)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := map[string]any{
		"status":     "ok",
		"pipes":      len(s.registry.Definitions()),
		"started_at": s.startedAt.Format(time.RFC3339),
		"uptime":     time.Since(s.startedAt).String(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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
	serveSSE(w, r, &s.voiceInput, 8, "voice_input", func(text string) []byte {
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
	serveSSE(w, r, &s.voiceSpeak, 8, "voice_speak", func(msg voiceSpeakMsg) []byte {
		return marshalJSON(map[string]string{"text": msg.Text, "priority": msg.Priority})
	})
}

func (s *Server) handleVoiceCyclePost(w http.ResponseWriter, r *http.Request) {
	s.voiceCycle.broadcast(struct{}{})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceCycleSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceCycle, 4, "voice_cycle", func(_ struct{}) []byte {
		return emptyJSON
	})
}

func (s *Server) handleVoiceStopPost(w http.ResponseWriter, r *http.Request) {
	s.voiceStop.broadcast(struct{}{})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceStopSSE(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, &s.voiceStop, 4, "voice_stop", func(_ struct{}) []byte {
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
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	s.lastVoiceStatus.Lock()
	last := s.lastVoiceStatus.val
	s.lastVoiceStatus.Unlock()
	if last != nil {
		fmt.Fprintf(w, "event: voice_status\ndata: %s\n\n", marshalJSON(*last))
		flusher.Flush()
	}

	ch := s.voiceStatus.subscribe(8)
	defer s.voiceStatus.unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case vs := <-ch:
			fmt.Fprintf(w, "event: voice_status\ndata: %s\n\n", marshalJSON(vs))
			flusher.Flush()
		}
	}
}
