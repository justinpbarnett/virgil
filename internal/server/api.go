package server

import (
	"encoding/json"
	"net/http"

	"github.com/justinpbarnett/virgil/internal/envelope"
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
	seed.ContentType = "text"

	result := s.runtime.Execute(plan, seed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
