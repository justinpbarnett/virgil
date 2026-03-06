package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/store"
)

// mockAIProvider implements bridge.Provider for AI planner injection.
type mockAIProvider struct {
	response string
	delay    time.Duration
	captured *string // if non-nil, receives the user message passed to Complete
}

func (m *mockAIProvider) Complete(ctx context.Context, _, user string) (string, error) {
	if m.captured != nil {
		*m.captured = user
	}
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return m.response, nil
}

// layer4Defs returns pipe definitions used by Layer 4 tests.
// A signal like "xyzzy foobar" won't match any trigger and will reach Layer 4.
func layer4Defs() []pipe.Definition {
	return []pipe.Definition{
		{
			Name:     "chat",
			Category: "general",
			Triggers: pipe.Triggers{Keywords: []string{"chat"}},
		},
		{
			Name:        "calendar",
			Description: "Reads and manages calendar events.",
			Category:    "time",
			Flags: map[string]pipe.Flag{
				"action": {Values: []string{"list", "create"}, Default: "list"},
				"range":  {Values: []string{"today", "tomorrow"}},
			},
			Triggers: pipe.Triggers{
				Exact:    []string{"check my calendar"},
				Keywords: []string{"calendar", "schedule"},
			},
		},
	}
}

// buildLayer4Server builds a Server wired for Layer 4 testing.
// Pass nil for optional dependencies that are not needed by a specific test.
func buildLayer4Server(
	t *testing.T,
	aiPlan *planner.AIPlanner,
	missLog *router.MissLog,
	st *store.Store,
	ack *mockStreamingProvider,
) *Server {
	t.Helper()

	reg := pipe.NewRegistry()
	defs := layer4Defs()
	for _, def := range defs {
		d := def
		reg.Register(d, func(input envelope.Envelope, _ map[string]string) envelope.Envelope {
			out := envelope.New(d.Name, "respond")
			out.Content = d.Name + " response"
			out.ContentType = envelope.ContentText
			return out
		})
	}

	rt := router.NewRouter(reg.Definitions(), slog.Default())
	p := parser.New(parser.LoadVocabulary(config.VocabularyConfig{}))
	pl := planner.New(config.TemplatesConfig{}, nil, nil)
	run := runtime.New(reg, nil, nil)

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "localhost", Port: 7890},
	}

	s := &Server{
		config:      cfg,
		router:      rt,
		parser:      p,
		planner:     pl,
		runtime:     run,
		registry:    reg,
		aiPlanner:   aiPlan,
		missLog:     missLog,
		store:       st,
		logger:      slog.Default(),
		voiceStatus: newBroker[voiceStatus](),
		voiceInput:  newBroker[string](),
		voiceSpeak:  newBroker[voiceSpeakMsg](),
		voiceCycle:  newBroker[struct{}](),
		voiceStop:   newBroker[struct{}](),
	}
	if ack != nil {
		s.ackProvider = ack
	}
	return s
}

// sendSSE sends a signal via the SSE path and returns the full response body.
func sendSSE(t *testing.T, s *Server, text string) string {
	t.Helper()
	handler := s.Handler()
	body := strings.NewReader(`{"text":"` + text + `"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)
	return w.Body.String()
}

// TestLayer4AIPlanner verifies that when the AI planner returns a valid plan,
// the server executes that plan instead of the deterministic fallback.
func TestLayer4AIPlanner(t *testing.T) {
	provider := &mockAIProvider{
		// AI returns a calendar plan for an unmatched signal
		response: `{"pipe": "calendar", "flags": {"action": "list"}}`,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	s := buildLayer4Server(t, ap, nil, nil, nil)

	resp := sendSSE(t, s, "xyzzy foobar")

	if !strings.Contains(resp, "event: done") {
		t.Fatalf("expected done event, got:\n%s", resp)
	}
	// The route event should name the calendar pipe (from the AI plan)
	if !strings.Contains(resp, `"pipe":"calendar"`) {
		t.Errorf("expected AI plan to route to calendar, got:\n%s", resp)
	}
	// The done envelope should contain the calendar pipe's output
	if !strings.Contains(resp, "calendar response") {
		t.Errorf("expected calendar pipe output in done event, got:\n%s", resp)
	}
}

// TestLayer4FallbackAfterAIFailure verifies that when the AI planner returns nil
// (chat-only or error), the server falls back to the deterministic planner (chat).
func TestLayer4FallbackAfterAIFailure(t *testing.T) {
	provider := &mockAIProvider{
		// chat-only response causes AIPlanner to return nil
		response: `{"pipe": "chat", "flags": {}}`,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	s := buildLayer4Server(t, ap, nil, nil, nil)

	resp := sendSSE(t, s, "xyzzy foobar")

	if !strings.Contains(resp, "event: done") {
		t.Fatalf("expected done event, got:\n%s", resp)
	}
	// Deterministic fallback routes to chat
	if !strings.Contains(resp, `"pipe":"chat"`) {
		t.Errorf("expected deterministic fallback to chat, got:\n%s", resp)
	}
}

// TestLayer4AckStartsBeforePlanning verifies that the ack stream starts before
// the AI planner completes, so the user sees immediate feedback during planning.
func TestLayer4AckStartsBeforePlanning(t *testing.T) {
	// Slow planner: 50ms delay to allow ack goroutine to write before planning finishes
	provider := &mockAIProvider{
		response: `{"pipe": "chat", "flags": {}}`,
		delay:    50 * time.Millisecond,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	ackProv := &mockStreamingProvider{chunks: []string{"On it — "}}
	s := buildLayer4Server(t, ap, nil, nil, ackProv)

	resp := sendSSE(t, s, "xyzzy foobar")

	// Both ack and route events must be present
	if !strings.Contains(resp, "event: ack") {
		t.Fatalf("expected ack event in Layer 4 response, got:\n%s", resp)
	}
	if !strings.Contains(resp, "event: route") {
		t.Fatalf("expected route event in response, got:\n%s", resp)
	}

	// Ack must appear before the route event (ack fires before planning completes,
	// route fires after planning).
	ackIdx := strings.Index(resp, "event: ack")
	routeIdx := strings.Index(resp, "event: route")
	if ackIdx >= routeIdx {
		t.Errorf("expected ack event before route event, ack at %d, route at %d in:\n%s", ackIdx, routeIdx, resp)
	}
}

// TestLayer4MissLogIncludesAIPlan verifies that when the AI planner produces a plan,
// the miss log entry includes the serialized plan and non-zero confidence.
func TestLayer4MissLogIncludesAIPlan(t *testing.T) {
	dir := t.TempDir()
	missLogPath := filepath.Join(dir, "misses.jsonl")
	missLog, err := router.NewMissLog(missLogPath)
	if err != nil {
		t.Fatalf("failed to create miss log: %v", err)
	}
	t.Cleanup(func() { missLog.Close() })

	provider := &mockAIProvider{
		response: `{"pipe": "calendar", "flags": {"action": "list"}}`,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	s := buildLayer4Server(t, ap, missLog, nil, nil)

	sendSSE(t, s, "xyzzy foobar")

	// Read and parse the miss log entry
	data, err := os.ReadFile(missLogPath)
	if err != nil {
		t.Fatalf("failed to read miss log: %v", err)
	}

	var entry router.MissEntry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil { // strip trailing newline
		t.Fatalf("failed to parse miss log entry: %v, raw: %s", err, data)
	}

	if entry.Signal != "xyzzy foobar" {
		t.Errorf("expected signal 'xyzzy foobar', got %q", entry.Signal)
	}
	if !strings.Contains(entry.AIPlan, "calendar") {
		t.Errorf("expected AIPlan to contain 'calendar', got %q", entry.AIPlan)
	}
	if entry.AIConfidence != 0.7 {
		t.Errorf("expected AIConfidence 0.7, got %f", entry.AIConfidence)
	}
}

// TestLayer4MissMetadataPopulated verifies that KeywordsFound and KeywordsNotFound
// are populated on Layer 4 route results and written to the miss log by the server.
func TestLayer4MissMetadataPopulated(t *testing.T) {
	dir := t.TempDir()
	missLogPath := filepath.Join(dir, "misses.jsonl")
	missLog, err := router.NewMissLog(missLogPath)
	if err != nil {
		t.Fatalf("failed to create miss log: %v", err)
	}
	t.Cleanup(func() { missLog.Close() })

	// AI planner returns nil (chat-only) so we rely on metadata from the router
	provider := &mockAIProvider{
		response: `{"pipe": "chat", "flags": {}}`,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	s := buildLayer4Server(t, ap, missLog, nil, nil)

	// "xyzzy" and "foobar" are unknown keywords — they should appear in KeywordsNotFound
	sendSSE(t, s, "xyzzy foobar")

	data, err := os.ReadFile(missLogPath)
	if err != nil {
		t.Fatalf("failed to read miss log: %v", err)
	}

	var entry router.MissEntry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("failed to parse miss log entry: %v, raw: %s", err, data)
	}

	if len(entry.KeywordsNotFound) == 0 {
		t.Error("expected KeywordsNotFound to be populated, got empty slice")
	}

	found := false
	for _, kw := range entry.KeywordsNotFound {
		if kw == "xyzzy" || kw == "foobar" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'xyzzy' or 'foobar' in KeywordsNotFound, got %v", entry.KeywordsNotFound)
	}
}

// TestLayer4PlannerReceivesMemory verifies that when the store contains recent
// invocations, the AI planner receives that context in its user message.
func TestLayer4PlannerReceivesMemory(t *testing.T) {
	// Open a real SQLite store in a temp directory
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Write recent invocations so buildPlannerMemory has something to return
	if _, err := st.SaveInvocation("calendar", "check my calendar", "3 events today"); err != nil {
		t.Fatalf("failed to save invocation: %v", err)
	}
	if _, err := st.SaveInvocation("calendar", "what meetings do I have?", "2 meetings"); err != nil {
		t.Fatalf("failed to save invocation: %v", err)
	}

	var captured string
	provider := &mockAIProvider{
		response: `{"pipe": "chat", "flags": {}}`,
		captured: &captured,
	}
	ap := planner.NewAIPlanner(provider, layer4Defs(), slog.Default())
	s := buildLayer4Server(t, ap, nil, st, nil)

	sendSSE(t, s, "what about tomorrow?")

	// The planner's user message should contain the recent history section
	if !strings.Contains(captured, "Recent history") {
		t.Errorf("expected user message to contain 'Recent history', got: %q", captured)
	}
	// It should include one of the recent signals
	if !strings.Contains(captured, "calendar") {
		t.Errorf("expected user message to reference recent 'calendar' invocation, got: %q", captured)
	}
}

