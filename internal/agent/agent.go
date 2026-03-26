package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/memory"
	"github.com/justinpbarnett/virgil/internal/observe"
	"github.com/justinpbarnett/virgil/internal/tools"
	"github.com/justinpbarnett/virgil/internal/trust"
)

const maxToolRounds = 20

// Agent orchestrates model calls, tool execution, and context assembly.
type Agent struct {
	config  *config.Config
	store   *memory.Store
	bridge  *bridge.FallbackBridge
	tools   *tools.Registry
	skills  []*internal.Skill
	events  *observe.EventLog
	session *SessionBuffer
	trust   *trust.Store // optional; nil disables trust checking
	push    func(string) // optional; nil disables push notifications
}

// SetTrust sets the trust store and push function. Both are optional.
func (a *Agent) SetTrust(ts *trust.Store, push func(string)) {
	a.trust = ts
	a.push = push
}

// SetPush sets the push notification function without changing the trust store.
func (a *Agent) SetPush(push func(string)) {
	a.push = push
}

// NewAgent creates an Agent and registers the run_skill meta-tool.
func NewAgent(cfg *config.Config, store *memory.Store, br *bridge.FallbackBridge, reg *tools.Registry, skills []*internal.Skill, events *observe.EventLog) (*Agent, error) {
	if cfg == nil || store == nil || br == nil || reg == nil || events == nil {
		return nil, fmt.Errorf("agent requires non-nil config, store, bridge, registry, and events")
	}

	a := &Agent{
		config:  cfg,
		store:   store,
		bridge:  br,
		tools:   reg,
		skills:  append([]*internal.Skill(nil), skills...),
		events:  events,
		session: NewSessionBuffer(),
	}

	// The agent loop intercepts run_skill before calling Execute, so this Execute
	// is only reached by callers outside the agent (e.g. MCP), where it returns
	// a clear error rather than a nil-pointer panic.
	reg.Register(&internal.Tool{
		Name:        "run_skill",
		Description: "Run a skill by name. Use this when the user's request maps to one of the available skills.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(_ context.Context, _ map[string]any) (*internal.ToolResult, error) {
			return &internal.ToolResult{Error: "run_skill must be dispatched by the Virgil agent"}, nil
		},
	})

	return a, nil
}

// Run handles one interactive signal.
func (a *Agent) Run(ctx context.Context, signal internal.Signal) (string, error) {
	traceID := observe.GenerateTraceID()
	spanID := observe.GenerateSpanID()
	ctx = observe.WithTraceContext(ctx, traceID, spanID)
	start := time.Now()

	assembled := a.assembleContext(signal)

	ms := a.config.ModelFor(signal.Channel)
	primary := bridge.NewModelConfig(a.config, ms.Model)
	var fallbacks []bridge.ModelConfig
	for _, ref := range ms.Fallback {
		fallbacks = append(fallbacks, bridge.NewModelConfig(a.config, ref))
	}

	messages := []bridge.Message{
		{Role: "system", Content: a.systemPrompt(signal)},
	}

	if turns := a.session.Get(signal.Channel); len(turns) > 0 {
		messages = append(messages, turns...)
	}

	userContent := signal.Content
	if assembled != "" {
		userContent = fmt.Sprintf("[Context]\n%s\n\n[Message]\n%s", assembled, signal.Content)
	}
	messages = append(messages, bridge.Message{
		Role:    "user",
		Content: userContent,
	})

	toolDefs := a.tools.Definitions()
	response, err := a.bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
	if err != nil {
		a.logEvent(traceID, spanID, "", "agent", "error", signal.Content, "", err, time.Since(start))
		return "", fmt.Errorf("agent run: %w", err)
	}

	for i := 0; response.HasToolCalls(); i++ {
		if i >= maxToolRounds {
			a.logEvent(traceID, spanID, "", "agent", "error", signal.Content, "",
				fmt.Errorf("tool loop exceeded %d rounds", maxToolRounds), time.Since(start))
			return "", fmt.Errorf("agent run: tool loop exceeded %d rounds", maxToolRounds)
		}
		toolResults := a.executeTools(ctx, response.ToolCalls)
		messages = append(messages, response.ToMessage())
		messages = append(messages, bridge.ToolResultsMessage(toolResults))
		response, err = a.bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
		if err != nil {
			a.logEvent(traceID, spanID, "", "agent", "error", signal.Content, "", err, time.Since(start))
			return "", fmt.Errorf("agent run (tool loop): %w", err)
		}
	}

	if _, err := a.store.Store(memory.StoreParams{
		Type:    memory.TypeInteraction,
		Content: fmt.Sprintf("User: %s\nAssistant: %s", signal.Content, response.Text),
		Source:  "agent:" + signal.Channel,
	}); err != nil {
		slog.Error("failed to store interaction", "channel", signal.Channel, "err", err)
		a.logEvent(traceID, spanID, "", "agent", "memory_error", signal.Content, "", err, time.Since(start))
	}

	a.session.Add(signal.Channel, signal.Content, response.Text)

	a.logEvent(traceID, spanID, "", "agent", "respond", signal.Content, response.Text, nil, time.Since(start))

	return response.Text, nil
}

// RunSkill runs a skill directly (scheduled, CLI, or interactive via run_skill tool).
func (a *Agent) RunSkill(ctx context.Context, skill *internal.Skill, trigger string) (string, error) {
	traceID, parentSpan := observe.TraceFromContext(ctx)
	if traceID == "" {
		traceID = observe.GenerateTraceID()
		parentSpan = ""
	}
	spanID := observe.GenerateSpanID()
	ctx = observe.WithTraceContext(ctx, traceID, spanID)
	ctx = trust.WithAutoApprovals(ctx) // track auto-approvals for rollback on error
	start := time.Now()

	sysPrompt := a.systemPrompt(internal.NewSignal("skill:"+skill.Name, ""))
	sysPrompt += "\n\n" + skill.Prompt
	if skill.Gotchas != "" {
		sysPrompt += "\n\n## Gotchas\n" + skill.Gotchas
	}
	if trigger == "interactive" {
		sysPrompt += "\n\nYou were invoked interactively. Return your results as text. Do not push notifications."
	}

	messages := []bridge.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: "Run now."},
	}

	ms := a.config.ModelFromSkill(skill.Model, skill.Fallback)
	primary := bridge.NewModelConfig(a.config, ms.Model)
	var fallbacks []bridge.ModelConfig
	for _, ref := range ms.Fallback {
		fallbacks = append(fallbacks, bridge.NewModelConfig(a.config, ref))
	}

	// Use skill's declared tool list instead of all tools (run_skill not listed, preventing recursion).
	toolDefs := a.tools.DefinitionsFor(skill.Tools)
	response, err := a.bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
	if err != nil {
		a.logEvent(traceID, spanID, parentSpan, "skill:"+skill.Name, "error", "run", "", err, time.Since(start))
		runErr := fmt.Errorf("run skill %s: %w", skill.Name, err)
		a.recoverError(ctx, skill.Name, runErr)
		return "", runErr
	}

	for i := 0; response.HasToolCalls(); i++ {
		if i >= maxToolRounds {
			loopErr := fmt.Errorf("run skill %s: tool loop exceeded %d rounds", skill.Name, maxToolRounds)
			a.logEvent(traceID, spanID, parentSpan, "skill:"+skill.Name, "error", "run", "",
				fmt.Errorf("tool loop exceeded %d rounds", maxToolRounds), time.Since(start))
			a.recoverError(ctx, skill.Name, loopErr)
			return "", loopErr
		}
		toolResults := a.executeTools(ctx, response.ToolCalls)
		messages = append(messages, response.ToMessage())
		messages = append(messages, bridge.ToolResultsMessage(toolResults))
		response, err = a.bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
		if err != nil {
			a.logEvent(traceID, spanID, parentSpan, "skill:"+skill.Name, "error", "run", "", err, time.Since(start))
			runErr := fmt.Errorf("run skill %s (tool loop): %w", skill.Name, err)
			a.recoverError(ctx, skill.Name, runErr)
			return "", runErr
		}
	}

	a.logEvent(traceID, spanID, parentSpan, "skill:"+skill.Name, "complete", "run", response.Text, nil, time.Since(start))
	return response.Text, nil
}

func (a *Agent) recoverError(ctx context.Context, skillName string, err error) {
	if a.push != nil {
		a.push(fmt.Sprintf("[virgil] Skill %q failed: %v", skillName, err))
	} else {
		slog.Warn("skill failed (no push configured)", "skill", skillName, "err", err)
	}
	if a.trust != nil {
		for _, ap := range trust.DrainAutoApprovals(ctx) {
			if rbErr := a.trust.Rollback(ap.ActionType, ap.Channel, ap.Contact); rbErr != nil {
				slog.Error("trust rollback failed -- score may be inflated",
					"action", ap.ActionType, "channel", ap.Channel, "contact", ap.Contact, "err", rbErr)
			}
		}
	}
}

// FindSkill returns a skill by name, or nil.
func (a *Agent) FindSkill(name string) *internal.Skill {
	for _, s := range a.skills {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (a *Agent) executeTools(ctx context.Context, calls []bridge.ToolCall) []bridge.ToolResult {
	traceID, parentSpan := observe.TraceFromContext(ctx)
	results := make([]bridge.ToolResult, 0, len(calls))
	for _, tc := range calls {
		if tc.Name == "run_skill" {
			results = append(results, a.handleRunSkill(ctx, tc))
			continue
		}

		tool := a.tools.Get(tc.Name)
		if tool == nil || tool.Execute == nil {
			slog.Warn("agent: unknown tool called", "name", tc.Name)
			results = append(results, bridge.ToolResult{
				ToolCallID: tc.ID,
				Content:    fmt.Sprintf("error: unknown tool %q", tc.Name),
				IsError:    true,
			})
			a.logEvent(traceID, observe.GenerateSpanID(), parentSpan, "tool:"+tc.Name, "error",
				bridge.InputToJSON(tc.Input), "", fmt.Errorf("unknown tool %q", tc.Name), 0)
			continue
		}

		result, err := tool.Execute(ctx, tc.Input)
		if err != nil {
			results = append(results, bridge.ToolResult{
				ToolCallID: tc.ID,
				Content:    fmt.Sprintf("error: %v", err),
				IsError:    true,
			})
			continue
		}

		results = append(results, bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    result.Format(),
		})
	}
	return results
}

func (a *Agent) handleRunSkill(ctx context.Context, tc bridge.ToolCall) bridge.ToolResult {
	traceID, parentSpan := observe.TraceFromContext(ctx)
	spanID := observe.GenerateSpanID()
	start := time.Now()
	name, _ := tc.Input["name"].(string)
	if name == "" {
		a.logEvent(traceID, spanID, parentSpan, "tool:run_skill", "error",
			bridge.InputToJSON(tc.Input), "", fmt.Errorf("empty skill name"), time.Since(start))
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    "error: skill name is required",
			IsError:    true,
		}
	}

	skill := a.FindSkill(name)
	if skill == nil {
		a.logEvent(traceID, spanID, parentSpan, "tool:run_skill", "error",
			bridge.InputToJSON(tc.Input), "", fmt.Errorf("skill %q not found", name), time.Since(start))
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("error: skill %q not found", name),
			IsError:    true,
		}
	}

	text, err := a.RunSkill(ctx, skill, "interactive")
	if err != nil {
		a.logEvent(traceID, spanID, parentSpan, "tool:run_skill", "error",
			bridge.InputToJSON(tc.Input), "", err, time.Since(start))
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("error running skill %s: %v", name, err),
			IsError:    true,
		}
	}

	a.logEvent(traceID, spanID, parentSpan, "tool:run_skill", "invoke",
		bridge.InputToJSON(tc.Input), text, nil, time.Since(start))
	return bridge.ToolResult{
		ToolCallID: tc.ID,
		Content:    text,
	}
}

func (a *Agent) logEvent(traceID, spanID, parentSpan, component, action, input, output string, err error, duration time.Duration) {
	ev := &internal.Event{
		Component:  component,
		Action:     action,
		Input:      input,
		Output:     output,
		DurationMs: duration.Milliseconds(),
		TraceID:    traceID,
		SpanID:     spanID,
		ParentSpan: parentSpan,
	}
	if err != nil {
		ev.Error = err.Error()
	}
	if logErr := a.events.Log(ev); logErr != nil {
		slog.Error("failed to log agent event", "component", component, "action", action, "err", logErr)
	}
}
