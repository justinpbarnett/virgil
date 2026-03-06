package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
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

// startAck launches the ack stream in a goroutine and returns a channel that
// closes when the ack completes. Returns a pre-closed channel if no ack provider
// is configured, so callers can always <-ackDone without nil-checking.
func (s *Server) startAck(ctx context.Context, signal string, mu *sync.Mutex, w http.ResponseWriter, flusher http.Flusher) <-chan struct{} {
	done := make(chan struct{}, 1)
	if s.ackProvider == nil {
		done <- struct{}{}
		return done
	}
	go func() {
		user := buildAckUserPrompt(signal, nil)
		_, err := s.ackProvider.CompleteStream(ctx, ackSystemPrompt, user, func(chunk string) {
			mu.Lock()
			w.Write([]byte(sse.FormatText(envelope.SSEEventAck, chunk))) //nolint:errcheck
			flusher.Flush()
			mu.Unlock()
		})
		if err != nil {
			s.logger.Warn("ack failed", "error", err)
		}
		done <- struct{}{}
	}()
	return done
}

// buildPlannerMemory fetches lightweight memory context for the AI planner.
func (s *Server) buildPlannerMemory() planner.PlannerMemory {
	var mem planner.PlannerMemory
	if s.store == nil {
		return mem
	}
	invocations, err := s.store.RecentInvocations(3)
	if err == nil {
		for _, inv := range invocations {
			mem.RecentSignals = append(mem.RecentSignals, planner.RecentSignal{
				Signal: inv.Signal,
				Pipe:   inv.Pipe,
			})
		}
	}
	states, err := s.store.ListState("planner")
	if err == nil {
		for _, st := range states {
			mem.WorkingState = append(mem.WorkingState, st.Key+": "+st.Content)
		}
	}
	return mem
}

// buildPlanForRoute produces an execution plan. For Layer 4 (fallback), it tries
// the AI planner first, falls back to the deterministic planner, and logs a miss.
// For Layers 1-3 it uses the deterministic planner directly.
// It mutates route.Pipe and route.Confidence when the AI planner succeeds.
func (s *Server) buildPlanForRoute(route *router.RouteResult, signal string, parsed parser.ParsedSignal) runtime.Plan {
	if route.Layer != router.LayerFallback {
		return s.planner.Plan(*route, parsed)
	}

	var aiPlan *runtime.Plan
	var aiConf float64
	if s.aiPlanner != nil {
		plannerMem := s.buildPlannerMemory()
		aiPlan, aiConf = s.aiPlanner.Plan(signal, plannerMem)
	}

	var plan runtime.Plan
	if aiPlan != nil {
		plan = *aiPlan
		route.Pipe = plan.Steps[0].Pipe
		route.Confidence = aiConf
	} else {
		plan = s.planner.Plan(*route, parsed)
	}
	s.logMiss(*route, signal, aiPlan, aiConf)
	return plan
}

// logMiss writes a miss log entry with AI plan data.
func (s *Server) logMiss(route router.RouteResult, signal string, aiPlan *runtime.Plan, aiConf float64) {
	if s.missLog == nil {
		return
	}
	var planJSON string
	if aiPlan != nil {
		if data, err := json.Marshal(aiPlan.Steps); err == nil {
			planJSON = string(data)
		}
	}
	_ = s.missLog.Log(router.MissEntry{
		Signal:           signal,
		KeywordsFound:    route.KeywordsFound,
		KeywordsNotFound: route.KeywordsNotFound,
		FallbackPipe:     route.Pipe,
		AIPlan:           planJSON,
		AIConfidence:     aiConf,
	})
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

	// Check if this route maps to a pipeline.
	var result envelope.Envelope
	if pc := s.pipelineForRoute(route.Pipe); pc != nil {
		result = s.executePipeline(*pc, seed)
	} else {
		plan := s.buildPlanForRoute(&route, req.Text, parsed)

		execSeed := seed
		if len(plan.Steps) == 1 {
			memCh := s.runtime.PrefetchMemory(seed, plan.Steps[0].Pipe)
			execSeed = <-memCh
			plan.SkipFirstMemoryInjection = true
		}

		result = s.runtime.Execute(plan, execSeed)
	}

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

	seed := buildSeed(req)
	route := s.router.Route(r.Context(), req.Text, parsed)

	var mu sync.Mutex

	sseSink := func(event runtime.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch event.Type {
		case envelope.SSEEventChunk:
			w.Write([]byte(sse.FormatText(envelope.SSEEventChunk, event.Data))) //nolint:errcheck
		case envelope.SSEEventStep:
			sse.WriteEvent(w, flusher, envelope.SSEEventStep, []byte(event.Data))
		case envelope.SSEEventTool:
			sse.WriteEvent(w, flusher, envelope.SSEEventTool, []byte(event.Data))
		case envelope.SSEEventTaskStatus:
			sse.WriteEvent(w, flusher, envelope.SSEEventTaskStatus, []byte(event.Data))
		case envelope.SSEEventTaskChunk:
			sse.WriteEvent(w, flusher, envelope.SSEEventTaskChunk, []byte(event.Data))
		case envelope.SSEEventTaskDone:
			sse.WriteEvent(w, flusher, envelope.SSEEventTaskDone, []byte(event.Data))
		case envelope.SSEEventPipelineProgress:
			sse.WriteEvent(w, flusher, envelope.SSEEventPipelineProgress, []byte(event.Data))
		}
		flusher.Flush()
	}

	// Check if this route maps to a pipeline.
	if pc := s.pipelineForRoute(route.Pipe); pc != nil {
		sse.WriteJSON(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": pc.Name, "layer": route.Layer})
		result := s.executePipelineStream(*pc, seed, sseSink)
		mu.Lock()
		sse.WriteJSON(w, flusher, envelope.SSEEventDone, result)
		mu.Unlock()
		return
	}

	isLayer4 := route.Layer == router.LayerFallback

	// Layer 4: start ack IMMEDIATELY — before AI planning.
	// The user sees feedback within milliseconds even though planning takes seconds.
	var ackDone <-chan struct{}
	if isLayer4 && s.aiPlanner != nil {
		ackDone = s.startAck(r.Context(), req.Text, &mu, w, flusher)
	}

	// Build the plan — AI planning for Layer 4, deterministic for Layers 1-3.
	plan := s.buildPlanForRoute(&route, req.Text, parsed)

	// Send route event now that we know the actual pipe.
	if len(plan.Steps) > 0 {
		sse.WriteJSON(w, flusher, envelope.SSEEventRoute, map[string]any{"pipe": plan.Steps[0].Pipe, "layer": route.Layer})
	}

	// Layers 2-3: start ack after planning (planning was instant).
	// Skip ack for layer 1 (exact match) — response is fast, ack would be redundant.
	if ackDone == nil && route.Layer != router.LayerExact {
		pipeIsAIBacked := len(plan.Steps) > 0 && s.config.Pipes[plan.Steps[0].Pipe].Prompts.System != ""
		if pipeIsAIBacked {
			ackDone = s.startAck(r.Context(), req.Text, &mu, w, flusher)
		}
	}

	// Prefetch memory for the correct pipe (runs in parallel with ack).
	execSeed := seed
	if len(plan.Steps) == 1 {
		memCh := s.runtime.PrefetchMemory(seed, plan.Steps[0].Pipe)
		execSeed = <-memCh
		plan.SkipFirstMemoryInjection = true
	}

	result := s.runtime.ExecuteStream(r.Context(), plan, execSeed, sseSink)
	if ackDone != nil {
		<-ackDone
	}
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

// pipelineForRoute returns the PipelineConfig if the route maps to a pipeline
// rather than a regular pipe. Returns nil if the route is a regular pipe.
func (s *Server) pipelineForRoute(pipeName string) *config.PipelineConfig {
	if s.config == nil {
		return nil
	}
	if pc, ok := s.config.Pipelines[pipeName]; ok {
		return &pc
	}
	return nil
}

// newPipelineExecutor creates a PipelineExecutor for the given config.
func (s *Server) newPipelineExecutor(pc config.PipelineConfig) (*runtime.PipelineExecutor, error) {
	observer := runtime.NewLogObserver(s.logger, s.config.LogLevel)
	return runtime.NewPipelineExecutor(s.runtime, pc, observer, s.logger)
}

// executePipeline creates a PipelineExecutor and runs the pipeline with the
// given seed envelope. Returns the final output envelope.
func (s *Server) executePipeline(pc config.PipelineConfig, seed envelope.Envelope) envelope.Envelope {
	pe, err := s.newPipelineExecutor(pc)
	if err != nil {
		return envelope.NewFatalError(pc.Name, "pipeline init: "+err.Error())
	}
	return pe.Execute(seed)
}

// executePipelineStream creates a PipelineExecutor with an SSE sink for
// streaming progress events during pipeline execution.
func (s *Server) executePipelineStream(pc config.PipelineConfig, seed envelope.Envelope, sink func(runtime.StreamEvent)) envelope.Envelope {
	pe, err := s.newPipelineExecutor(pc)
	if err != nil {
		return envelope.NewFatalError(pc.Name, "pipeline init: "+err.Error())
	}
	return pe.ExecuteStream(seed, sink)
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
