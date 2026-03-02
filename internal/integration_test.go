package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	"context"
	"log/slog"
	"time"
)

// Mock provider for integration tests
type mockProvider struct {
	response string
}

func (m *mockProvider) Complete(_ context.Context, system, user string) (string, error) {
	if m.response != "" {
		return m.response, nil
	}
	return "Mock response for: " + user, nil
}

// Mock calendar client
type mockCalendarClient struct{}

func (m *mockCalendarClient) GetEvents(_ context.Context, _ string, _, _ time.Time) ([]calendarPipe.Event, error) {
	return []calendarPipe.Event{
		{Title: "Standup", Start: "10:00 AM", End: "10:30 AM", Location: "Room A"},
		{Title: "Design Review", Start: "1:00 PM", End: "2:00 PM", Location: "Conference B"},
		{Title: "1:1 with Sarah", Start: "3:00 PM", End: "3:30 PM", Location: ""},
	}, nil
}

func setupIntegrationServer(t *testing.T) http.Handler {
	t.Helper()

	// Load config from project config directory
	cfg, err := config.Load("../config")
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
	provider := &mockProvider{}

	// Register all pipes
	reg := pipe.NewRegistry()

	if memCfg, ok := cfg.Pipes["memory"]; ok {
		reg.Register(memCfg.ToDefinition(), memoryPipe.NewHandler(memStore))
	}

	if calCfg, ok := cfg.Pipes["calendar"]; ok {
		reg.Register(calCfg.ToDefinition(), calendarPipe.NewHandler(&mockCalendarClient{}))
	}

	if draftCfg, ok := cfg.Pipes["draft"]; ok {
		reg.Register(draftCfg.ToDefinition(), draftPipe.NewHandler(provider, draftCfg))
	}

	if chatCfg, ok := cfg.Pipes["chat"]; ok {
		reg.Register(chatCfg.ToDefinition(), chatPipe.NewHandler(provider))
	}

	// Build router
	missLogPath := filepath.Join(t.TempDir(), "misses.jsonl")
	missLog, _ := router.NewMissLog(missLogPath)
	if missLog != nil {
		t.Cleanup(func() { missLog.Close() })
	}
	rt := router.NewRouter(reg.Definitions(), missLog)

	// Build planner
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources)

	// Build runtime
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	observer := runtime.NewLogObserver(logger, "debug")
	run := runtime.New(reg, observer)

	// Build server
	srv := server.New(cfg, rt, p, pl, run, reg, logger)
	return srv.Handler()
}

func sendSignal(handler http.Handler, text string) envelope.Envelope {
	body := strings.NewReader(`{"text":"` + text + `"}`)
	req := httptest.NewRequest("POST", "/signal", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var env envelope.Envelope
	json.Unmarshal(w.Body.Bytes(), &env)
	return env
}

// Scenario 1: "what's on my calendar today" → routes to calendar, returns list
func TestIntegration_CalendarToday(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(handler, "what's on my calendar today")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "list" {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
}

// Scenario 2: "remember that OAuth uses short-lived tokens" → routes to memory, stores
func TestIntegration_MemoryStore(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(handler, "remember that OAuth uses short-lived tokens")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", result.Pipe)
	}
	if result.Action != "store" {
		t.Errorf("expected action=store, got %s", result.Action)
	}
}

// Scenario 3: "what do I know about OAuth" → routes to memory, retrieves
func TestIntegration_MemoryRetrieve(t *testing.T) {
	handler := setupIntegrationServer(t)

	// First store something
	sendSignal(handler, "remember that OAuth uses short-lived tokens")

	// Then retrieve
	result := sendSignal(handler, "what do I know about OAuth")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", result.Pipe)
	}
	if result.ContentType != "list" {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}

	// Verify actual entries were returned
	contentJSON, _ := json.Marshal(result.Content)
	if result.Content == nil || string(contentJSON) == "[]" || string(contentJSON) == "null" {
		t.Error("expected non-empty content with matching entries")
	}
}

// Scenario 4: "draft a blog post about my notes on OAuth" → 2-step plan (memory.retrieve | draft)
func TestIntegration_DraftFromNotes(t *testing.T) {
	handler := setupIntegrationServer(t)

	// Store some notes first
	sendSignal(handler, "remember that OAuth uses short-lived tokens")
	sendSignal(handler, "remember that OAuth refresh tokens should be rotated")

	// Draft from notes
	result := sendSignal(handler, "draft a blog post about my notes on OAuth")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "text" {
		t.Errorf("expected content_type=text, got %s", result.ContentType)
	}
	// Draft pipe should have been the last to execute
	if result.Pipe != "draft" {
		t.Errorf("expected final pipe=draft, got %s", result.Pipe)
	}
}

// Scenario 5: "xyzzy foobar nonsense" → falls through to chat, miss logged
func TestIntegration_FallbackToChat(t *testing.T) {
	handler := setupIntegrationServer(t)
	result := sendSignal(handler, "xyzzy foobar nonsense")

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
	cfg, err := config.Load("../config")
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
	provider := &mockProvider{}

	reg := pipe.NewRegistry()
	if chatCfg, ok := cfg.Pipes["chat"]; ok {
		reg.Register(chatCfg.ToDefinition(), chatPipe.NewHandler(provider))
	}

	missLog, _ := router.NewMissLog(missLogPath)
	defer missLog.Close()
	rt := router.NewRouter(reg.Definitions(), missLog)
	pl := planner.New(cfg.Templates, cfg.Vocabulary.Sources)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	run := runtime.New(reg, nil)
	srv := server.New(cfg, rt, p, pl, run, reg, logger)
	handler := srv.Handler()

	// Send unrecognized signal
	sendSignal(handler, "completely unknown gibberish")

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

