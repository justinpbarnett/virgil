package internal

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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
	calendarPipe "github.com/justinpbarnett/virgil/internal/pipes/calendar"
	chatPipe "github.com/justinpbarnett/virgil/internal/pipes/chat"
	draftPipe "github.com/justinpbarnett/virgil/internal/pipes/draft"
	memoryPipe "github.com/justinpbarnett/virgil/internal/pipes/memory"
	"github.com/justinpbarnett/virgil/internal/planner"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/server"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

// Mock calendar client
type mockCalendarClient struct{}

func (m *mockCalendarClient) GetEvents(_ context.Context, _ string, _, _ time.Time) ([]calendarPipe.Event, error) {
	return []calendarPipe.Event{
		{Title: "Standup", Start: "10:00 AM", End: "10:30 AM", Location: "Room A"},
		{Title: "Design Review", Start: "1:00 PM", End: "2:00 PM", Location: "Conference B"},
		{Title: "1:1 with Sarah", Start: "3:00 PM", End: "3:30 PM", Location: ""},
	}, nil
}

func (m *mockCalendarClient) CreateEvent(_ context.Context, _ string, title string, start, end time.Time, location, description string) (*calendarPipe.Event, error) {
	return &calendarPipe.Event{ID: "new1", Title: title}, nil
}

func (m *mockCalendarClient) UpdateEvent(_ context.Context, _ string, eventID, title string, _, _ time.Time, _, _ string) (*calendarPipe.Event, error) {
	return &calendarPipe.Event{ID: eventID, Title: title}, nil
}

func (m *mockCalendarClient) DeleteEvent(_ context.Context, _, _ string) error {
	return nil
}

func setupIntegrationServer(t *testing.T) http.Handler {
	t.Helper()

	// Load config from project config directory
	cfg, err := config.Load("../config", "./pipes")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Use temp database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg.DatabasePath = dbPath

	// Open store
	memStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { memStore.Close() })

	// Build parser
	vocab := parser.LoadVocabulary(cfg.Vocabulary)
	p := parser.New(vocab)

	// Mock provider
	provider := &testutil.MockProvider{}

	// Register all pipes
	reg := pipe.NewRegistry()

	if memCfg, ok := cfg.Pipes["memory"]; ok {
		reg.Register(memCfg.ToDefinition(), memoryPipe.NewHandler(memStore, nil))
	}

	if calCfg, ok := cfg.Pipes["calendar"]; ok {
		reg.Register(calCfg.ToDefinition(), calendarPipe.NewHandler(&mockCalendarClient{}, nil))
	}

	if draftCfg, ok := cfg.Pipes["draft"]; ok {
		reg.Register(draftCfg.ToDefinition(), draftPipe.NewHandler(provider, draftCfg, nil))
	}

	if chatCfg, ok := cfg.Pipes["chat"]; ok {
		reg.Register(chatCfg.ToDefinition(), chatPipe.NewHandler(provider, chatCfg.Prompts.System, chatCfg.Prompts.Templates, nil))
	}

	// Build router
	missLogPath := filepath.Join(t.TempDir(), "misses.jsonl")
	missLog, _ := router.NewMissLog(missLogPath)
	if missLog != nil {
		t.Cleanup(func() { missLog.Close() })
	}
	rt := router.NewRouter(reg.Definitions(), missLog, nil)

	// Build planner
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources, nil)

	// Build runtime with format templates
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	observer := runtime.NewLogObserver(logger, config.Debug)
	run, err := runtime.NewWithFormats(reg, observer, logger, config.Debug, cfg.RawFormats())
	if err != nil {
		t.Fatalf("failed to build runtime: %v", err)
	}

	// Build server
	srv := server.New(server.Deps{
		Config:   cfg,
		Router:   rt,
		Parser:   p,
		Planner:  pl,
		Runtime:  run,
		Registry: reg,
		Logger:   logger,
	})
	return srv.Handler()
}

func sendSignal(t *testing.T, handler http.Handler, text string) envelope.Envelope {
	t.Helper()
	body := strings.NewReader(`{"text":"` + text + `"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var env envelope.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	return env
}

// Scenario 1: "what's on my calendar today" → calendar retrieves events, formatted to text
func TestIntegration_CalendarToday(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(t, handler, "what's on my calendar today")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "calendar" {
		t.Errorf("expected final pipe=calendar, got %s", result.Pipe)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text (formatted), got %s", result.ContentType)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(s, "3 events") {
		t.Errorf("expected '3 events' in output, got: %s", s)
	}
	if !strings.Contains(s, "Standup") {
		t.Errorf("expected 'Standup' in output, got: %s", s)
	}
}

// Scenario 2: "remember that OAuth uses short-lived tokens" → memory stores (already text, no formatting)
func TestIntegration_MemoryStore(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(t, handler, "remember that OAuth uses short-lived tokens")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "memory" {
		t.Errorf("expected final pipe=memory, got %s", result.Pipe)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
}

// Scenario 3: "what do I know about OAuth" → memory retrieves, formatted to text
func TestIntegration_MemoryRetrieve(t *testing.T) {
	handler := setupIntegrationServer(t)

	// First store something
	sendSignal(t, handler, "remember that OAuth uses short-lived tokens")

	// Then retrieve
	result := sendSignal(t, handler, "what do I know about OAuth")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "memory" {
		t.Errorf("expected final pipe=memory, got %s", result.Pipe)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text (formatted), got %s", result.ContentType)
	}
	s, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(s, "Found") {
		t.Errorf("expected 'Found' in output, got: %s", s)
	}
}

// Scenario 4: "draft a blog post about my notes on OAuth" → memory.retrieve | draft
func TestIntegration_DraftFromNotes(t *testing.T) {
	handler := setupIntegrationServer(t)

	// Store some notes first
	sendSignal(t, handler, "remember that OAuth uses short-lived tokens")
	sendSignal(t, handler, "remember that OAuth refresh tokens should be rotated")

	// Draft from notes
	result := sendSignal(t, handler, "draft a blog post about my notes on OAuth")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	if result.Pipe != "draft" {
		t.Errorf("expected final pipe=draft, got %s", result.Pipe)
	}
}

// Scenario 5: "xyzzy foobar nonsense" → falls through to chat, miss logged
func TestIntegration_FallbackToChat(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(t, handler, "xyzzy foobar nonsense")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "chat" {
		t.Errorf("expected pipe=chat, got %s", result.Pipe)
	}
}

// Scenario 6: Miss log has valid JSONL structure
func TestIntegration_MissLogStructure(t *testing.T) {
	dir := t.TempDir()
	missLogPath := filepath.Join(dir, "misses.jsonl")

	// Load config
	cfg, err := config.Load("../config", "./pipes")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	cfg.DatabasePath = filepath.Join(dir, "test.db")

	memStore, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer memStore.Close()

	vocab := parser.LoadVocabulary(cfg.Vocabulary)
	p := parser.New(vocab)
	provider := &testutil.MockProvider{}

	reg := pipe.NewRegistry()
	if chatCfg, ok := cfg.Pipes["chat"]; ok {
		reg.Register(chatCfg.ToDefinition(), chatPipe.NewHandler(provider, chatCfg.Prompts.System, chatCfg.Prompts.Templates, nil))
	}

	missLog, _ := router.NewMissLog(missLogPath)
	defer missLog.Close()
	rt := router.NewRouter(reg.Definitions(), missLog, nil)
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources, nil)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	run := runtime.New(reg, nil, nil)
	srv := server.New(server.Deps{
		Config:   cfg,
		Router:   rt,
		Parser:   p,
		Planner:  pl,
		Runtime:  run,
		Registry: reg,
		Logger:   logger,
	})
	handler := srv.Handler()

	// Send unrecognized signal
	sendSignal(t, handler, "completely unknown gibberish")

	// Read miss log
	data, err := os.ReadFile(missLogPath)
	if err != nil {
		t.Fatalf("failed to read miss log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatal("expected at least one miss log entry")
	}

	// Verify JSONL structure
	var entry router.MissEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("invalid JSONL: %v", err)
	}
	if entry.Signal == "" {
		t.Error("expected signal field to be populated")
	}
	if entry.FallbackPipe == "" {
		t.Error("expected fallback_pipe field to be populated")
	}
	if entry.Timestamp == "" {
		t.Error("expected timestamp field to be populated")
	}
}

