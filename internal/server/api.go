package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

// defaultSpecPipe is the pipe name used for speculative execution and memory
// prefetch. Chat is the most common destination for user signals.
const defaultSpecPipe = "chat"

// writeSSEEvent marshals data as JSON and writes an SSE event to w.
func writeSSEEvent(w io.Writer, flusher http.Flusher, eventType string, data any) {
	encoded, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded)
	flusher.Flush()
}

// formatSSEChunk formats a text chunk as an SSE chunk line.
func formatSSEChunk(data string) string {
	escaped, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: {\"text\":%s}\n\n", envelope.SSEEventChunk, escaped)
}

// specSink buffers SSE chunk events until routing confirms chat, then switches
// to writing directly to the response writer.
type specSink struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	live    bool
	w       io.Writer
	flusher http.Flusher
}

func (ss *specSink) write(event runtime.StreamEvent) {
	if event.Type != envelope.SSEEventChunk {
		return
	}
	line := formatSSEChunk(event.Data)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.live {
		ss.w.Write([]byte(line)) //nolint:errcheck
		ss.flusher.Flush()
	} else {
		ss.buf.WriteString(line)
	}
}

// goLive flushes the buffer to w and switches subsequent writes to go directly
// to w. Must be called after routing confirms the speculative stream is correct.
func (ss *specSink) goLive() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.buf.Len() > 0 {
		ss.w.Write(ss.buf.Bytes()) //nolint:errcheck
		ss.flusher.Flush()
		ss.buf.Reset()
	}
	ss.live = true
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

	// Synchronous path
	seed := buildSeed(req)

	// Start memory prefetch concurrently with routing; chat is the most
	// common destination so we prefetch for that pipe by default.
	memCh := s.runtime.PrefetchMemory(seed, defaultSpecPipe)

	route := s.router.Route(r.Context(), req.Text, parsed)
	plan := s.planner.Plan(route, parsed)

	var execSeed envelope.Envelope
	if route.Pipe == defaultSpecPipe && len(plan.Steps) == 1 {
		execSeed = <-memCh
		plan.SkipFirstMemoryInjection = true
	} else {
		go func() { <-memCh }() // drain to prevent goroutine leak
		execSeed = seed
	}

	result := s.runtime.Execute(plan, execSeed)

	s.logger.Info("signal complete", "duration", time.Since(start).String())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, req signalRequest, parsed parser.ParsedSignal) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Build seed early so we can start memory prefetch and the speculative
	// chat stream before routing completes.
	seed := buildSeed(req)

	// Prefetch memory for the chat pipe concurrently with routing. The channel
	// is buffered (capacity 1) so the background goroutine never blocks if its
	// result is not consumed (e.g. routing resolves to a non-chat pipe).
	memCh := s.runtime.PrefetchMemory(seed, defaultSpecPipe)

	// Start speculative chat stream: begin the AI call immediately, buffer
	// its output until routing confirms the route is chat.
	specCtx, specCancel := context.WithCancel(r.Context())
	ss := &specSink{w: w, flusher: flusher}
	specDone := make(chan envelope.Envelope, 1)

	go func() {
		// Wait for memory to be ready or bail if routing cancelled us first.
		var chatSeed envelope.Envelope
		select {
		case chatSeed = <-memCh:
		case <-specCtx.Done():
			specDone <- envelope.Envelope{}
			return
		}
		chatPlan := runtime.Plan{
			Steps:                    []runtime.Step{{Pipe: defaultSpecPipe}},
			SkipFirstMemoryInjection: true,
		}
		result := s.runtime.ExecuteStream(specCtx, chatPlan, chatSeed, ss.write)
		specDone <- result
	}()

	// Route and plan concurrently with the speculative stream.
	route := s.router.Route(r.Context(), req.Text, parsed)
	plan := s.planner.Plan(route, parsed)

	if route.Pipe == defaultSpecPipe && len(plan.Steps) == 1 {
		// Routing confirmed chat — send route event, flush buffered chunks,
		// then let the speculative stream complete.
		writeSSEEvent(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": defaultSpecPipe, "layer": route.Layer})

		ss.goLive()
		result := <-specDone
		specCancel()

		writeSSEEvent(w, flusher, envelope.SSEEventDone, result)
		return
	}

	// Route resolved to a different pipe — cancel the speculative stream and
	// drain its goroutine, then execute the correct pipe.
	specCancel()
	go func() { <-specDone }()

	if len(plan.Steps) > 0 {
		writeSSEEvent(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": plan.Steps[0].Pipe, "layer": route.Layer})
	}

	// Use pre-fetched memory if we still have it available from the speculative run.
	// Since the spec stream was cancelled, rebuild with a fresh context.
	execSeed := seed
	sink := func(event runtime.StreamEvent) {
		switch event.Type {
		case envelope.SSEEventChunk:
			w.Write([]byte(formatSSEChunk(event.Data))) //nolint:errcheck
		case envelope.SSEEventStep:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventStep, event.Data)
		}
		flusher.Flush()
	}

	result := s.runtime.ExecuteStream(r.Context(), plan, execSeed, sink)
	writeSSEEvent(w, flusher, envelope.SSEEventDone, result)
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
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", envelope.SSEContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ch := s.voiceStatus.subscribe(8)
	defer s.voiceStatus.unsubscribe(ch)

	s.lastVoiceStatus.Lock()
	last := s.lastVoiceStatus.val
	s.lastVoiceStatus.Unlock()
	if last != nil {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventVoiceStatus, marshalJSON(*last))
		flusher.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case vs := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.SSEEventVoiceStatus, marshalJSON(vs))
			flusher.Flush()
		}
	}
}
