package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/runtime"
)

// PlannerMemory provides recent context so the planner can handle follow-ups.
type PlannerMemory struct {
	RecentSignals []RecentSignal // last 3 signal→pipe pairs (most recent first)
	WorkingState  []string       // working state summaries (e.g. "calendar/last_query: tomorrow's events")
}

type RecentSignal struct {
	Signal string
	Pipe   string
}

// AIPlanner uses an AI provider to produce execution plans for signals that
// the deterministic router (Layers 1-3) could not match.
type AIPlanner struct {
	provider     bridge.Provider
	catalogue    string
	systemPrompt string
	pipeNames    map[string]bool
	logger       *slog.Logger
}

// NewAIPlanner constructs an AIPlanner from a provider and pipe definitions.
// The catalogue string and pipe name set are built once at init time.
func NewAIPlanner(provider bridge.Provider, defs []pipe.Definition, logger *slog.Logger) *AIPlanner {
	if logger == nil {
		logger = slog.Default()
	}
	ap := &AIPlanner{
		provider:  provider,
		pipeNames: make(map[string]bool),
		logger:    logger,
	}
	ap.catalogue = buildCatalogue(defs, ap.pipeNames)
	ap.systemPrompt = fmt.Sprintf(aiPlannerSystemPromptTemplate, ap.catalogue)
	return ap
}

// buildCatalogue constructs the pipe catalogue string for the system prompt.
// chat is excluded because it is the implicit fallback the AI can always select.
func buildCatalogue(defs []pipe.Definition, pipeNames map[string]bool) string {
	var sb strings.Builder
	for _, def := range defs {
		pipeNames[def.Name] = true
		if def.Name == "chat" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(def.Name)
		if def.Description != "" {
			sb.WriteString(": ")
			sb.WriteString(def.Description)
		}
		if len(def.Flags) > 0 {
			sb.WriteString("\n  flags: ")
			parts := make([]string, 0, len(def.Flags))
			for flagName, flag := range def.Flags {
				part := flagName
				if len(flag.Values) > 0 {
					part += " (" + strings.Join(flag.Values, "|") + ")"
				}
				if flag.Default != "" {
					part += ", default: " + flag.Default
				}
				parts = append(parts, part)
			}
			sb.WriteString(strings.Join(parts, ", "))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

const aiPlannerSystemPromptTemplate = `You are an intent planner for a personal assistant called Virgil. Given a user's
signal, determine which pipe(s) to invoke and with what flags.

Available pipes:
%s
You may select a single pipe or compose a pipeline of multiple pipes. In a pipeline,
each pipe's output feeds as input to the next pipe. Common patterns:
- study → chat: gather context from codebase/memory first, then chat answers using that context
- calendar → draft: retrieve calendar data, then draft a summary
- study → code: gather context, then generate code

Respond with ONLY valid JSON. No markdown, no explanation.

For a single pipe:
{"pipe": "name", "flags": {"key": "value"}}

For a multi-step pipeline:
{"steps": [{"pipe": "name", "flags": {"key": "value"}}, {"pipe": "name2", "flags": {}}]}

Rules:
- Use "chat" only when the signal is genuinely conversational and no other pipe fits.
- When the user asks about the codebase, project, or code: use study(source=codebase) before chat.
- When the user asks about their data from a specific source: use that source pipe first.
- Only include flags that are relevant. Omit flags you're unsure about.
- The last pipe in a pipeline produces the user-facing response.
- Use the recent history to understand follow-up signals. If the user's signal is ambiguous
  but the recent history shows a specific pipe, prefer that pipe for continuity.
- If the user mentions a specific external service or platform (e.g., Jira, Slack, GitHub,
  email, Trello) that is NOT listed as an available pipe, respond with "chat". Do not map
  service-specific requests to loosely related pipes.`

// aiPlanResponse is the JSON structure returned by the AI planner.
type aiPlanResponse struct {
	// Single-pipe response
	Pipe  string            `json:"pipe"`
	Flags map[string]string `json:"flags"`
	// Multi-step pipeline response
	Steps []aiPlanStep `json:"steps"`
}

type aiPlanStep struct {
	Pipe  string            `json:"pipe"`
	Flags map[string]string `json:"flags"`
}

// Plan calls the AI provider to produce an execution plan for the given signal.
// Returns nil, 0.0 on any failure — Layer 4 must never block or panic.
func (ap *AIPlanner) Plan(signal string, memory PlannerMemory) (*runtime.Plan, float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userMessage := buildPlannerUserMessage(signal, memory)

	response, err := ap.provider.Complete(ctx, ap.systemPrompt, userMessage)
	if err != nil {
		ap.logger.Warn("AI planner provider error", "error", err)
		return nil, 0.0
	}

	plan, conf := ap.parseResponse(response)
	return plan, conf
}

// buildPlannerUserMessage constructs the user message from signal + memory context.
func buildPlannerUserMessage(signal string, memory PlannerMemory) string {
	var sb strings.Builder
	sb.WriteString(signal)

	if len(memory.RecentSignals) > 0 {
		sb.WriteString("\n\nRecent history:")
		for _, rs := range memory.RecentSignals {
			sb.WriteString("\n- \"")
			sb.WriteString(rs.Signal)
			sb.WriteString("\" → ")
			sb.WriteString(rs.Pipe)
		}
	}

	if len(memory.WorkingState) > 0 {
		sb.WriteString("\n\nWorking state:")
		for _, ws := range memory.WorkingState {
			sb.WriteString("\n- ")
			sb.WriteString(ws)
		}
	}

	return sb.String()
}

// parseResponse parses the AI provider's JSON response into a runtime.Plan.
// Returns nil, 0.0 on any parse error or validation failure.
func (ap *AIPlanner) parseResponse(response string) (*runtime.Plan, float64) {
	// Strip potential markdown code fences
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```") {
		lines := strings.SplitN(response, "\n", 2)
		if len(lines) > 1 {
			response = lines[1]
		}
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	var parsed aiPlanResponse
	if err := json.Unmarshal([]byte(response), &parsed); err != nil {
		ap.logger.Warn("AI planner JSON parse error", "error", err, "response", response)
		return nil, 0.0
	}

	// Multi-step pipeline
	if len(parsed.Steps) > 0 {
		steps := make([]runtime.Step, 0, len(parsed.Steps))
		for _, s := range parsed.Steps {
			if !ap.pipeNames[s.Pipe] {
				ap.logger.Warn("AI planner returned unknown pipe", "pipe", s.Pipe)
				return nil, 0.0
			}
			steps = append(steps, runtime.Step{Pipe: s.Pipe, Flags: s.Flags})
		}
		plan := &runtime.Plan{Steps: steps}
		ap.logger.Info("AI planner produced multi-step plan", "steps", len(steps))
		return plan, 0.7
	}

	// Single-pipe response
	if parsed.Pipe == "" {
		ap.logger.Warn("AI planner returned empty response")
		return nil, 0.0
	}

	if !ap.pipeNames[parsed.Pipe] {
		ap.logger.Warn("AI planner returned unknown pipe", "pipe", parsed.Pipe)
		return nil, 0.0
	}

	// Chat-only single-step plan: let the existing fallback handle it
	if parsed.Pipe == "chat" {
		ap.logger.Debug("AI planner returned chat-only, deferring to fallback")
		return nil, 0.0
	}

	plan := &runtime.Plan{
		Steps: []runtime.Step{
			{Pipe: parsed.Pipe, Flags: parsed.Flags},
		},
	}
	ap.logger.Info("AI planner produced single-pipe plan", "pipe", parsed.Pipe)
	return plan, 0.7
}
