package planner

import (
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/parser"
	"github.com/justinpbarnett/virgil/internal/router"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

func requireSynthesisCap(t *testing.T, plan runtime.Plan) {
	t.Helper()
	last := plan.Steps[len(plan.Steps)-1]
	if last.Pipe != synthesizer {
		t.Errorf("expected last step to be %q, got %q", synthesizer, last.Pipe)
	}
}

func testPlanner() *Planner {
	templates := config.TemplatesConfig{
		Templates: []config.TemplateEntry{
			{
				Requires: []string{"verb", "type", "source"},
				Plan: []config.PlanStep{
					{Pipe: "{source}", Flags: map[string]string{"action": "retrieve", "sort": "recent", "limit": "10", "topic": "{topic}"}},
					{Pipe: "{verb}", Flags: map[string]string{"type": "{type}"}},
				},
			},
			{
				Requires: []string{"verb", "source", "modifier"},
				Plan: []config.PlanStep{
					{Pipe: "{source}", Flags: map[string]string{"range": "{modifier}"}},
					{Pipe: "{verb}", Flags: map[string]string{"type": "summary"}},
				},
			},
			{
				Requires: []string{"verb", "type"},
				Plan: []config.PlanStep{
					{Pipe: "{verb}", Flags: map[string]string{"type": "{type}", "topic": "{topic}"}},
				},
			},
			{
				Requires: []string{"verb", "source"},
				Plan: []config.PlanStep{
					{Pipe: "{source}", Flags: map[string]string{"action": "retrieve"}},
					{Pipe: "{verb}", Flags: map[string]string{}},
				},
			},
			{
				Requires: []string{"verb"},
				Plan: []config.PlanStep{
					{Pipe: "{verb}"},
				},
			},
		},
	}

	sources := map[string]string{
		"notes":    "memory",
		"calendar": "calendar",
		"memory":   "memory",
	}

	return New(templates, sources, nil)
}

func TestPlanVerbTypeSource(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "draft",
		Type:   "blog",
		Source: "memory",
	}
	route := router.RouteResult{Pipe: "draft"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "memory" {
		t.Errorf("expected step 0 pipe=memory, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["action"] != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", plan.Steps[0].Flags["action"])
	}
	if plan.Steps[0].Flags["sort"] != "recent" {
		t.Errorf("expected sort=recent, got %s", plan.Steps[0].Flags["sort"])
	}
	if plan.Steps[0].Flags["limit"] != "10" {
		t.Errorf("expected limit=10, got %s", plan.Steps[0].Flags["limit"])
	}
	if plan.Steps[1].Pipe != "draft" {
		t.Errorf("expected step 1 pipe=draft, got %s", plan.Steps[1].Pipe)
	}
	if plan.Steps[1].Flags["type"] != "blog" {
		t.Errorf("expected type=blog, got %s", plan.Steps[1].Flags["type"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbType(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:  "draft",
		Type:  "blog",
		Topic: "oauth",
	}
	route := router.RouteResult{Pipe: "draft"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "draft" {
		t.Errorf("expected pipe=draft, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["type"] != "blog" {
		t.Errorf("expected type=blog, got %s", plan.Steps[0].Flags["type"])
	}
	if plan.Steps[0].Flags["topic"] != "oauth" {
		t.Errorf("expected topic=oauth, got %s", plan.Steps[0].Flags["topic"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbSource(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "calendar",
		Source: "calendar",
	}
	route := router.RouteResult{Pipe: "calendar"}

	plan := p.Plan(route, parsed)

	// calendar→calendar collapses to one step, then chat is appended
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "calendar" {
		t.Errorf("expected pipe=calendar, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["action"] != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", plan.Steps[0].Flags["action"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbWithAction(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Action: "store",
		Topic:  "OAuth uses short-lived tokens",
	}
	route := router.RouteResult{Pipe: "memory"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["action"] != "store" {
		t.Errorf("expected action=store, got %s", plan.Steps[0].Flags["action"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanNoMatch(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{}
	route := router.RouteResult{Pipe: "chat"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "chat" {
		t.Errorf("expected pipe=chat, got %s", plan.Steps[0].Pipe)
	}
}

func TestPlanSourceResolution(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "draft",
		Type:   "blog",
		Source: "notes", // should resolve to "memory" pipe
	}
	route := router.RouteResult{Pipe: "draft"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "memory" {
		t.Errorf("expected step 0 pipe=memory (resolved from notes), got %s", plan.Steps[0].Pipe)
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbSourceModifier(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:     "draft",
		Source:   "calendar",
		Modifier: "today",
	}
	route := router.RouteResult{Pipe: "draft"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "calendar" {
		t.Errorf("expected step 0 pipe=calendar, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["range"] != "today" {
		t.Errorf("expected range=today, got %s", plan.Steps[0].Flags["range"])
	}
	if plan.Steps[1].Pipe != "draft" {
		t.Errorf("expected step 1 pipe=draft, got %s", plan.Steps[1].Pipe)
	}
	if plan.Steps[1].Flags["type"] != "summary" {
		t.Errorf("expected type=summary, got %s", plan.Steps[1].Flags["type"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbSourceDifferentPipes(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "draft",
		Source: "calendar",
	}
	route := router.RouteResult{Pipe: "draft"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps (calendar→draft→chat), got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "calendar" {
		t.Errorf("expected step 0 pipe=calendar, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[1].Pipe != "draft" {
		t.Errorf("expected step 1 pipe=draft, got %s", plan.Steps[1].Pipe)
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbSourceSamePipeCollapsed(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Action: "retrieve",
		Source: "memory",
	}
	route := router.RouteResult{Pipe: "memory"}

	plan := p.Plan(route, parsed)

	// verb=memory, source=memory → two steps resolve to memory→memory
	// which should be collapsed to a single step, then chat appended
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps (collapsed memory→chat), got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "memory" {
		t.Errorf("expected pipe=memory, got %s", plan.Steps[0].Pipe)
	}
	if plan.Steps[0].Flags["action"] != "retrieve" {
		t.Errorf("expected action=retrieve, got %s", plan.Steps[0].Flags["action"])
	}
	requireSynthesisCap(t, plan)
}

func TestPlanVerbSourceSamePipeCollapsedMergesFlags(t *testing.T) {
	// Custom planner with a template that produces two same-pipe steps with different flags
	templates := config.TemplatesConfig{
		Templates: []config.TemplateEntry{
			{
				Requires: []string{"verb", "source"},
				Plan: []config.PlanStep{
					{Pipe: "{source}", Flags: map[string]string{"action": "retrieve"}},
					{Pipe: "{verb}", Flags: map[string]string{"sort": "recent"}},
				},
			},
		},
	}
	sources := map[string]string{"memory": "memory"}
	p := New(templates, sources, nil)

	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Source: "memory",
	}
	route := router.RouteResult{Pipe: "memory"}
	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps (collapsed memory + chat), got %d", len(plan.Steps))
	}
	if plan.Steps[0].Flags["action"] != "retrieve" {
		t.Errorf("expected action=retrieve from first step, got %s", plan.Steps[0].Flags["action"])
	}
	if plan.Steps[0].Flags["sort"] != "recent" {
		t.Errorf("expected sort=recent merged from second step, got %s", plan.Steps[0].Flags["sort"])
	}
	requireSynthesisCap(t, plan)
}

func TestEnsureSynthesisSkipsWhenChatIsLast(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{}
	route := router.RouteResult{Pipe: "chat"}

	plan := p.Plan(route, parsed)

	// Chat routed directly — should not get a second chat appended
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "chat" {
		t.Errorf("expected pipe=chat, got %s", plan.Steps[0].Pipe)
	}
}

func TestEnsureSynthesisAppendsChatToSinglePipe(t *testing.T) {
	p := testPlanner()
	parsed := parser.ParsedSignal{
		Verb:   "memory",
		Action: "retrieve",
	}
	route := router.RouteResult{Pipe: "memory"}

	plan := p.Plan(route, parsed)

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps (memory→chat), got %d", len(plan.Steps))
	}
	if plan.Steps[0].Pipe != "memory" {
		t.Errorf("expected step 0 pipe=memory, got %s", plan.Steps[0].Pipe)
	}
	requireSynthesisCap(t, plan)
}
