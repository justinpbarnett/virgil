package planner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

// mockProvider is a test double for bridge.Provider.
type mockProvider struct {
	response string
	err      error
	delay    time.Duration
}

func (m *mockProvider) Complete(ctx context.Context, _, _ string) (string, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return m.response, m.err
}

func testDefs() []pipe.Definition {
	return []pipe.Definition{
		{
			Name:        "calendar",
			Description: "Reads and manages events on calendar services.",
			Category:    "time",
			Flags: map[string]pipe.Flag{
				"action": {Values: []string{"list", "create"}, Default: "list"},
				"range":  {Values: []string{"today", "tomorrow", "this_week"}},
			},
		},
		{
			Name:        "study",
			Description: "Gathers and compresses relevant context from a source.",
			Category:    "research",
			Flags: map[string]pipe.Flag{
				"source": {Values: []string{"codebase", "memory", "files"}},
				"role":   {Values: []string{"general", "planner", "builder"}},
			},
		},
		{
			Name:        "draft",
			Description: "Produces written content from input context.",
			Category:    "comms",
			Flags: map[string]pipe.Flag{
				"type": {Values: []string{"summary", "email", "blog"}},
			},
		},
		{
			Name:        "chat",
			Description: "General conversational assistant.",
			Category:    "general",
		},
	}
}

func TestCatalogueBuild(t *testing.T) {
	pipeNames := make(map[string]bool)
	catalogue := buildCatalogue(testDefs(), pipeNames)

	// chat should be excluded from catalogue but in pipeNames
	if strings.Contains(catalogue, "- chat") {
		t.Error("expected chat to be excluded from catalogue")
	}
	if !pipeNames["chat"] {
		t.Error("expected chat to be in pipeNames")
	}

	// calendar should be in the catalogue with flags
	if !strings.Contains(catalogue, "- calendar") {
		t.Error("expected calendar in catalogue")
	}
	if !strings.Contains(catalogue, "action") {
		t.Error("expected action flag in catalogue")
	}

	// study and draft should be in catalogue
	if !strings.Contains(catalogue, "- study") {
		t.Error("expected study in catalogue")
	}
	if !strings.Contains(catalogue, "- draft") {
		t.Error("expected draft in catalogue")
	}
}

func TestPlanSinglePipe(t *testing.T) {
	provider := &mockProvider{
		response: `{"pipe": "calendar", "flags": {"action": "list"}}`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("what's on my calendar?", PlannerMemory{})
	if plan == nil {
		t.Fatal("expected a plan, got nil")
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "calendar" {
		t.Errorf("expected pipe calendar, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["action"] != "list" {
		t.Errorf("expected action=list, got %s", plan.Steps[0].Flags["action"])
	}
	if conf != 0.7 {
		t.Errorf("expected confidence 0.7, got %f", conf)
	}
}

func TestPlanMultiStep(t *testing.T) {
	provider := &mockProvider{
		response: `{"steps": [{"pipe": "study", "flags": {"source": "codebase"}}, {"pipe": "chat", "flags": {}}]}`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("what pipe should we build next?", PlannerMemory{})
	if plan == nil {
		t.Fatal("expected a plan, got nil")
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "study" {
		t.Errorf("expected first pipe study, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[1].Pipe != "chat" {
		t.Errorf("expected second pipe chat, got %s", plan.Steps[1].Pipe)
	}
	if conf != 0.7 {
		t.Errorf("expected confidence 0.7, got %f", conf)
	}
}

func TestPlanChatOnlyReturnsNil(t *testing.T) {
	provider := &mockProvider{
		response: `{"pipe": "chat", "flags": {}}`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("hey", PlannerMemory{})
	if plan != nil {
		t.Errorf("expected nil plan for chat-only response, got %+v", plan)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
}

func TestPlanUnknownPipeReturnsNil(t *testing.T) {
	provider := &mockProvider{
		response: `{"pipe": "nonexistent", "flags": {}}`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("do something", PlannerMemory{})
	if plan != nil {
		t.Errorf("expected nil plan for unknown pipe, got %+v", plan)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
}

func TestPlanProviderError(t *testing.T) {
	provider := &mockProvider{
		err: errors.New("provider unavailable"),
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("something", PlannerMemory{})
	if plan != nil {
		t.Errorf("expected nil plan on provider error, got %+v", plan)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
}

func TestPlanInvalidJSON(t *testing.T) {
	provider := &mockProvider{
		response: `not json at all`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	plan, conf := ap.Plan("something", PlannerMemory{})
	if plan != nil {
		t.Errorf("expected nil plan on invalid JSON, got %+v", plan)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
}

func TestPlanTimeout(t *testing.T) {
	provider := &mockProvider{
		delay:    11 * time.Second, // beyond 10-second timeout
		response: `{"pipe": "calendar", "flags": {}}`,
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	start := time.Now()
	plan, conf := ap.Plan("something", PlannerMemory{})
	elapsed := time.Since(start)

	if plan != nil {
		t.Errorf("expected nil plan on timeout, got %+v", plan)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
	// Should complete within ~10.5s (timeout + small buffer), not 11s
	if elapsed > 10500*time.Millisecond {
		t.Errorf("expected timeout to fire within 10.5s, took %v", elapsed)
	}
}

func TestPlanWithMemoryContext(t *testing.T) {
	var capturedUserMsg string
	provider := &captureProvider{
		response: `{"pipe": "calendar", "flags": {}}`,
		onComplete: func(_, user string) {
			capturedUserMsg = user
		},
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	mem := PlannerMemory{
		RecentSignals: []RecentSignal{
			{Signal: "check my calendar", Pipe: "calendar"},
		},
		WorkingState: []string{"calendar/last_query: today's events"},
	}
	ap.Plan("what about tomorrow?", mem)

	if !strings.Contains(capturedUserMsg, "Recent history") {
		t.Error("expected user message to contain 'Recent history'")
	}
	if !strings.Contains(capturedUserMsg, "check my calendar") {
		t.Error("expected user message to contain recent signal")
	}
	if !strings.Contains(capturedUserMsg, "Working state") {
		t.Error("expected user message to contain 'Working state'")
	}
	if !strings.Contains(capturedUserMsg, "calendar/last_query") {
		t.Error("expected user message to contain working state entry")
	}
}

func TestPlanEmptyMemory(t *testing.T) {
	var capturedUserMsg string
	provider := &captureProvider{
		response: `{"pipe": "calendar", "flags": {}}`,
		onComplete: func(_, user string) {
			capturedUserMsg = user
		},
	}
	ap := NewAIPlanner(provider, testDefs(), nil)

	ap.Plan("check my calendar", PlannerMemory{})

	if strings.Contains(capturedUserMsg, "Recent history") {
		t.Error("expected no 'Recent history' section for empty memory")
	}
	if strings.Contains(capturedUserMsg, "Working state") {
		t.Error("expected no 'Working state' section for empty memory")
	}
	if capturedUserMsg != "check my calendar" {
		t.Errorf("expected user message to be just the signal, got: %q", capturedUserMsg)
	}
}

// captureProvider captures the user message passed to Complete.
type captureProvider struct {
	response   string
	onComplete func(system, user string)
}

func (c *captureProvider) Complete(_ context.Context, system, user string) (string, error) {
	if c.onComplete != nil {
		c.onComplete(system, user)
	}
	return c.response, nil
}
