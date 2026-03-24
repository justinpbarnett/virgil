package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/memory"
	"github.com/justinpbarnett/virgil/internal/observe"
	"github.com/justinpbarnett/virgil/internal/tools"
)

// Agent orchestrates model calls, tool execution, and context assembly.
type Agent struct {
	Config  *config.Config
	Store   *memory.Store
	Bridge  *bridge.FallbackBridge
	Tools   *tools.Registry
	Skills  []*internal.Skill
	Events  *observe.EventLog
	Session *SessionBuffer
}

// NewAgent creates an Agent and registers the run_skill meta-tool.
func NewAgent(cfg *config.Config, store *memory.Store, br *bridge.FallbackBridge, reg *tools.Registry, skills []*internal.Skill, events *observe.EventLog) *Agent {
	a := &Agent{
		Config:  cfg,
		Store:   store,
		Bridge:  br,
		Tools:   reg,
		Skills:  skills,
		Events:  events,
		Session: NewSessionBuffer(),
	}

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
	})

	return a
}

// Run handles one interactive signal.
func (a *Agent) Run(ctx context.Context, signal internal.Signal) (string, error) {
	traceID := observe.GenerateTraceID()
	spanID := observe.GenerateSpanID()
	start := time.Now()

	assembled := a.assembleContext(signal)

	ms := a.Config.ModelFor(signal.Channel)
	primary := bridge.NewModelConfig(a.Config, ms.Model)
	var fallbacks []bridge.ModelConfig
	for _, ref := range ms.Fallback {
		fallbacks = append(fallbacks, bridge.NewModelConfig(a.Config, ref))
	}

	messages := []bridge.Message{
		{Role: "system", Content: a.systemPrompt(signal)},
	}

	if turns := a.Session.Get(signal.Channel); len(turns) > 0 {
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

	toolDefs := a.Tools.Definitions()
	response, err := a.Bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
	if err != nil {
		a.logEvent(traceID, spanID, "agent", "error", signal.Content, "", err, time.Since(start))
		return "", fmt.Errorf("agent run: %w", err)
	}

	for response.HasToolCalls() {
		toolResults := a.executeTools(ctx, traceID, response.ToolCalls)
		messages = append(messages, response.ToMessage())
		messages = append(messages, bridge.ToolResultsMessage(toolResults))
		response, err = a.Bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
		if err != nil {
			a.logEvent(traceID, spanID, "agent", "error", signal.Content, "", err, time.Since(start))
			return "", fmt.Errorf("agent run (tool loop): %w", err)
		}
	}

	a.Store.Store(memory.StoreParams{
		Type:    memory.TypeInteraction,
		Content: fmt.Sprintf("User: %s\nAssistant: %s", signal.Content, response.Text),
		Source:  "agent:" + signal.Channel,
	})

	a.Session.Add(signal.Channel, signal.Content, response.Text)

	a.logEvent(traceID, spanID, "agent", "respond", signal.Content, response.Text, nil, time.Since(start))

	return response.Text, nil
}

// RunSkill runs a skill directly (scheduled, CLI, or interactive via run_skill tool).
func (a *Agent) RunSkill(ctx context.Context, skill *internal.Skill, trigger string) (string, error) {
	traceID := observe.GenerateTraceID()
	spanID := observe.GenerateSpanID()
	start := time.Now()

	sysPrompt := a.systemPrompt(internal.Signal{Channel: "skill:" + skill.Name})
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

	ms := a.Config.ModelFromSkill(skill.Model, skill.Fallback)
	primary := bridge.NewModelConfig(a.Config, ms.Model)
	var fallbacks []bridge.ModelConfig
	for _, ref := range ms.Fallback {
		fallbacks = append(fallbacks, bridge.NewModelConfig(a.Config, ref))
	}

	toolDefs := a.Tools.DefinitionsFor(skill.Tools)
	response, err := a.Bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
	if err != nil {
		a.logEvent(traceID, spanID, "skill:"+skill.Name, "error", "run", "", err, time.Since(start))
		return "", fmt.Errorf("run skill %s: %w", skill.Name, err)
	}

	for response.HasToolCalls() {
		toolResults := a.executeTools(ctx, traceID, response.ToolCalls)
		messages = append(messages, response.ToMessage())
		messages = append(messages, bridge.ToolResultsMessage(toolResults))
		response, err = a.Bridge.Complete(ctx, primary, messages, toolDefs, fallbacks)
		if err != nil {
			a.logEvent(traceID, spanID, "skill:"+skill.Name, "error", "run", "", err, time.Since(start))
			return "", fmt.Errorf("run skill %s (tool loop): %w", skill.Name, err)
		}
	}

	a.logEvent(traceID, spanID, "skill:"+skill.Name, "complete", "run", response.Text, nil, time.Since(start))
	return response.Text, nil
}

// FindSkill returns a skill by name, or nil.
func (a *Agent) FindSkill(name string) *internal.Skill {
	for _, s := range a.Skills {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (a *Agent) executeTools(ctx context.Context, traceID string, calls []bridge.ToolCall) []bridge.ToolResult {
	results := make([]bridge.ToolResult, 0, len(calls))
	for _, tc := range calls {
		spanID := observe.GenerateSpanID()
		start := time.Now()

		if tc.Name == "run_skill" {
			result := a.handleRunSkill(ctx, traceID, tc)
			results = append(results, result)
			continue
		}

		tool := a.Tools.Get(tc.Name)
		if tool == nil || tool.Execute == nil {
			slog.Warn("agent: unknown tool called", "name", tc.Name)
			results = append(results, bridge.ToolResult{
				ToolCallID: tc.ID,
				Content:    fmt.Sprintf("error: unknown tool %q", tc.Name),
				IsError:    true,
			})
			a.logEvent(traceID, spanID, "tool:"+tc.Name, "error",
				inputJSON(tc.Input), "", fmt.Errorf("unknown tool %q", tc.Name), time.Since(start))
			continue
		}

		result, err := tool.Execute(ctx, tc.Input)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool execution failed", "tool", tc.Name, "err", err)
			results = append(results, bridge.ToolResult{
				ToolCallID: tc.ID,
				Content:    fmt.Sprintf("error: %v", err),
				IsError:    true,
			})
			a.logEvent(traceID, spanID, "tool:"+tc.Name, "error",
				inputJSON(tc.Input), "", err, duration)
			continue
		}

		content := formatToolResult(result)
		results = append(results, bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    content,
		})
		a.logEvent(traceID, spanID, "tool:"+tc.Name, "invoke",
			inputJSON(tc.Input), content, nil, duration)
	}
	return results
}

func (a *Agent) handleRunSkill(ctx context.Context, traceID string, tc bridge.ToolCall) bridge.ToolResult {
	name, _ := tc.Input["name"].(string)
	if name == "" {
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    "error: skill name is required",
			IsError:    true,
		}
	}

	skill := a.FindSkill(name)
	if skill == nil {
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("error: skill %q not found", name),
			IsError:    true,
		}
	}

	text, err := a.RunSkill(ctx, skill, "interactive")
	if err != nil {
		return bridge.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("error running skill %s: %v", name, err),
			IsError:    true,
		}
	}

	return bridge.ToolResult{
		ToolCallID: tc.ID,
		Content:    text,
	}
}

func (a *Agent) logEvent(traceID, spanID, component, action, input, output string, err error, duration time.Duration) {
	ev := &internal.Event{
		Component:  component,
		Action:     action,
		Input:      input,
		Output:     output,
		DurationMs: duration.Milliseconds(),
		TraceID:    traceID,
		SpanID:     spanID,
	}
	if err != nil {
		ev.Error = err.Error()
	}
	if logErr := a.Events.Log(ev); logErr != nil {
		slog.Error("failed to log agent event", "component", component, "action", action, "err", logErr)
	}
}

func formatToolResult(result *internal.ToolResult) string {
	if result.Error != "" {
		return "error: " + result.Error
	}
	if result.Text != "" {
		return result.Text
	}
	if result.Data != nil {
		data, err := json.Marshal(result.Data)
		if err != nil {
			return fmt.Sprintf("%v", result.Data)
		}
		return string(data)
	}
	return "ok"
}

func inputJSON(input map[string]any) string {
	if input == nil {
		return "{}"
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}
