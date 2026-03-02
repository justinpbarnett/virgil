package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

type signalRequest struct {
	Text string `json:"text"`
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	var req signalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, `{"error":"text field is required"}`, http.StatusBadRequest)
		return
	}

	// Parse → Route → Plan → Execute
	parsed := s.parser.Parse(req.Text)
	route := s.router.Route(req.Text, parsed)

	s.logger.Info("signal processed",
		"signal", req.Text,
		"pipe", route.Pipe,
		"layer", route.Layer,
		"confidence", route.Confidence,
	)

	plan := s.planner.Plan(route, parsed)

	seed := envelope.New("signal", "input")
	seed.Content = req.Text
	seed.ContentType = envelope.ContentText

	// SSE streaming path
	if r.Header.Get("Accept") == envelope.SSEContentType {
		s.handleSSE(w, r, plan, seed)
		return
	}

	result := s.runtime.Execute(plan, seed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, plan runtime.Plan, seed envelope.Envelope) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	sink := func(chunk string) {
		escaped, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "event: %s\ndata: {\"text\":%s}\n\n", envelope.SSEEventChunk, escaped)
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
