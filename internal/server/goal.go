package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/runtime"
	"github.com/justinpbarnett/virgil/internal/store"
)

// GoalData is the structured data stored in a goal memory entry's Data column.
type GoalData struct {
	Objective      string      `json:"objective"`
	Status         string      `json:"status"`
	Complexity     string      `json:"complexity"`
	BlockedOn      string      `json:"blocked_on,omitempty"`
	CycleCount     int         `json:"cycle_count"`
	Phases         []GoalPhase `json:"phases,omitempty"`
	OriginalSignal string      `json:"original_signal"`
}

// GoalPhase tracks a single phase within a mission-tier goal.
type GoalPhase struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

// EvaluateOutcome is the result of evaluating execution output against a goal.
type EvaluateOutcome struct {
	Status    string // "met", "not_met", "blocked"
	Summary   string
	BlockedOn string
	NextPhase string
}

// parseGoalData unmarshals GoalData from a Memory's Data column.
func parseGoalData(mem store.Memory) (*GoalData, error) {
	if mem.Data == "" {
		return nil, fmt.Errorf("empty goal data")
	}
	var gd GoalData
	if err := json.Unmarshal([]byte(mem.Data), &gd); err != nil {
		return nil, err
	}
	return &gd, nil
}

// retrieveActiveGoal searches for an active or blocked goal relevant to the signal.
func (s *Server) retrieveActiveGoal(signal string) (*store.Memory, *GoalData, error) {
	if s.store == nil {
		return nil, nil, nil
	}

	seen := make(map[string]bool)
	var candidates []store.Memory

	// FTS search for goals relevant to this signal.
	if ftsGoals, err := s.store.SearchByKind(signal, store.KindGoal, 5); err == nil {
		for _, g := range ftsGoals {
			if !seen[g.ID] {
				seen[g.ID] = true
				candidates = append(candidates, g)
			}
		}
	}

	// Also query by status to catch goals FTS might miss.
	for _, status := range []string{"active", "blocked"} {
		if goals, err := s.store.QueryByKindFiltered(store.KindGoal, map[string]any{"status": status}, 5); err == nil {
			for _, g := range goals {
				if !seen[g.ID] {
					seen[g.ID] = true
					candidates = append(candidates, g)
				}
			}
		}
	}

	// Pick the best match: prefer FTS hits (they appear first), then active over blocked.
	for _, c := range candidates {
		gd, err := parseGoalData(c)
		if err != nil {
			continue
		}
		if gd.Status == "active" || gd.Status == "blocked" {
			return &c, gd, nil
		}
	}

	return nil, nil, nil
}

// deriveGoal creates a goal memory entry if the complexity warrants it.
func (s *Server) deriveGoal(signal string, plan runtime.Plan, existingGoal *GoalData) (*GoalData, string, error) {
	if existingGoal != nil {
		return existingGoal, "", nil
	}

	if plan.Complexity == "trivial" || plan.Complexity == "simple" || plan.Complexity == "" {
		return nil, "", nil
	}

	goal := GoalData{
		Objective:      signal,
		Status:         "active",
		Complexity:     plan.Complexity,
		OriginalSignal: signal,
	}

	if plan.Complexity == "mission" {
		goal.Phases = s.decomposeIntoPhases(signal)
	}

	if s.store == nil {
		return &goal, "", nil
	}

	id, err := s.store.SaveKind(store.KindGoal, signal, goal, []string{"goal"}, nil)
	if err != nil {
		return nil, "", err
	}
	return &goal, id, nil
}

// decomposeIntoPhases calls the decompose pipe to break a mission into phases.
func (s *Server) decomposeIntoPhases(signal string) []GoalPhase {
	seed := envelope.New("goal", "decompose")
	seed.Content = signal
	seed.ContentType = envelope.ContentText
	seed.Args["signal"] = signal
	seed.Args["spec"] = signal

	plan := runtime.Plan{Steps: []runtime.Step{{Pipe: "decompose", Flags: map[string]string{}}}}
	result := s.runtime.Execute(plan, seed)

	// Try to parse structured output into phases.
	if result.ContentType == envelope.ContentStructured && result.Content != nil {
		b, err := json.Marshal(result.Content)
		if err == nil {
			var output struct {
				Tasks []struct {
					Name string `json:"name"`
				} `json:"tasks"`
			}
			if json.Unmarshal(b, &output) == nil && len(output.Tasks) > 0 {
				phases := make([]GoalPhase, len(output.Tasks))
				for i, t := range output.Tasks {
					status := "pending"
					if i == 0 {
						status = "active"
					}
					phases[i] = GoalPhase{Name: t.Name, Status: status}
				}
				return phases
			}
		}
	}

	// Fallback: single phase from the signal.
	return []GoalPhase{{Name: signal, Status: "active"}}
}

const evaluateSystemPrompt = `You are evaluating whether an execution result satisfies a goal.
Respond with exactly one line starting with one of: MET, NOT_MET, BLOCKED, PHASE_COMPLETE, GOAL_COMPLETE.
Follow the keyword with a colon and a brief explanation.`

// buildEvaluatePrompt constructs the evaluation prompt.
func buildEvaluatePrompt(output envelope.Envelope, goal *GoalData) string {
	outputText := envelope.ContentToText(output.Content, output.ContentType)
	if output.Error != nil {
		outputText += "\nError: " + output.Error.Message
	}

	var sb strings.Builder

	// Check if this is a mission with phases.
	var currentPhase string
	for _, p := range goal.Phases {
		if p.Status == "active" {
			currentPhase = p.Name
			break
		}
	}

	if currentPhase != "" {
		fmt.Fprintf(&sb, "Goal: %s\nCurrent phase: %s\nPhase result: %s\n\n", goal.Objective, currentPhase, outputText)
		sb.WriteString("1. Is this phase complete?\n2. If yes, what should the next phase focus on?\n3. Is anything blocked on human input?\n\n")
		sb.WriteString("Respond with exactly one of:\n")
		sb.WriteString("- PHASE_COMPLETE: [summary, next phase recommendation]\n")
		sb.WriteString("- NOT_MET: [what's still needed for this phase]\n")
		sb.WriteString("- BLOCKED: [what human input/action is needed]\n")
		sb.WriteString("- GOAL_COMPLETE: [all phases done, final summary]\n")
	} else {
		fmt.Fprintf(&sb, "Goal: %s\nExecution result: %s\n\n", goal.Objective, outputText)
		sb.WriteString("Has the goal been met?\nRespond with exactly one of:\n")
		sb.WriteString("- MET: [one sentence explaining why]\n")
		sb.WriteString("- NOT_MET: [what's still missing, what to do next]\n")
		sb.WriteString("- BLOCKED: [what human input/action is needed and why]\n")
	}

	return sb.String()
}

// parseEvaluateResponse parses the AI evaluation response.
func parseEvaluateResponse(response string) EvaluateOutcome {
	response = strings.TrimSpace(response)

	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")

		switch {
		case strings.HasPrefix(line, "MET:"):
			return EvaluateOutcome{Status: "met", Summary: strings.TrimSpace(line[4:])}
		case strings.HasPrefix(line, "GOAL_COMPLETE:"):
			return EvaluateOutcome{Status: "met", Summary: strings.TrimSpace(line[14:])}
		case strings.HasPrefix(line, "PHASE_COMPLETE:"):
			summary := strings.TrimSpace(line[15:])
			return EvaluateOutcome{Status: "not_met", Summary: summary, NextPhase: "next"}
		case strings.HasPrefix(line, "NOT_MET:"):
			return EvaluateOutcome{Status: "not_met", Summary: strings.TrimSpace(line[8:])}
		case strings.HasPrefix(line, "BLOCKED:"):
			msg := strings.TrimSpace(line[8:])
			return EvaluateOutcome{Status: "blocked", Summary: msg, BlockedOn: msg}
		}
	}

	// Unparseable response -- fail open to avoid infinite loops.
	return EvaluateOutcome{Status: "met", Summary: "evaluation inconclusive"}
}

// evaluateGoal checks execution output against the goal.
func (s *Server) evaluateGoal(output envelope.Envelope, goal *GoalData) EvaluateOutcome {
	// Nil goal (trivial/simple): implicit check.
	if goal == nil {
		if output.Error != nil && output.Error.Severity == envelope.SeverityFatal {
			return EvaluateOutcome{Status: "blocked", BlockedOn: output.Error.Message}
		}
		return EvaluateOutcome{Status: "met"}
	}

	// Multi-step / Mission: AI evaluation.
	if s.aiPlanner == nil {
		return EvaluateOutcome{Status: "met"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prompt := buildEvaluatePrompt(output, goal)
	response, err := s.aiPlanner.Provider().Complete(ctx, evaluateSystemPrompt, prompt)
	if err != nil {
		return EvaluateOutcome{Status: "blocked", BlockedOn: "evaluation unavailable", Summary: "AI provider error during evaluation"}
	}

	return parseEvaluateResponse(response)
}

// closeGoal marks a goal as complete via supersession.
func (s *Server) closeGoal(goalID string, goal *GoalData) {
	if s.store == nil || goalID == "" {
		return
	}
	goal.Status = "complete"
	s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"}) //nolint:errcheck
}

// blockGoal marks a goal as blocked via supersession.
func (s *Server) blockGoal(goalID string, goal *GoalData, blockedOn string) {
	if s.store == nil || goalID == "" {
		return
	}
	goal.Status = "blocked"
	goal.BlockedOn = blockedOn
	s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"}) //nolint:errcheck
}

// updateGoalProgress records progress via supersession, advancing phases for mission goals.
// Returns the new goal memory ID for use in subsequent supersessions.
func (s *Server) updateGoalProgress(goalID string, goal *GoalData, outcome EvaluateOutcome) string {
	if s.store == nil || goalID == "" {
		return goalID
	}
	if outcome.NextPhase != "" {
		for i := range goal.Phases {
			if goal.Phases[i].Status == "active" {
				goal.Phases[i].Status = "complete"
				goal.Phases[i].Summary = outcome.Summary
				for j := i + 1; j < len(goal.Phases); j++ {
					if goal.Phases[j].Status == "pending" {
						goal.Phases[j].Status = "active"
						break
					}
				}
				break
			}
		}
	}
	newID, err := s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"})
	if err != nil {
		return goalID
	}
	return newID
}

// formatBlockedResponse creates a user-facing response describing what's needed.
func formatBlockedResponse(goal *GoalData, outcome EvaluateOutcome, lastOutput envelope.Envelope) envelope.Envelope {
	out := lastOutput
	text := envelope.ContentToText(lastOutput.Content, lastOutput.ContentType)
	if text != "" {
		text += "\n\n"
	}
	text += "I need your help to continue: " + outcome.BlockedOn
	out.Content = text
	out.ContentType = envelope.ContentText
	return out
}

// formatSafetyBoundResponse creates a response when max cycles are reached.
func formatSafetyBoundResponse(goal *GoalData, lastOutput envelope.Envelope) envelope.Envelope {
	out := lastOutput
	text := envelope.ContentToText(lastOutput.Content, lastOutput.ContentType)
	if text != "" {
		text += "\n\n"
	}
	text += fmt.Sprintf("Reached the maximum of %d autonomous cycles. Here's what was accomplished so far. Follow up to continue.", goal.CycleCount)
	out.Content = text
	out.ContentType = envelope.ContentText
	return out
}

// replan asks the AI planner for a new plan incorporating what was learned.
func (s *Server) replan(signal string, goal *GoalData, lastOutput envelope.Envelope) *runtime.Plan {
	if s.aiPlanner == nil {
		return nil
	}
	outputText := envelope.ContentToText(lastOutput.Content, lastOutput.ContentType)
	replanSignal := fmt.Sprintf(
		"Continue working on: %s\n\nLast result: %s\n\nWhat to do next based on goal progress.",
		goal.Objective, outputText,
	)
	mem := s.buildPlannerMemory()
	plan, _ := s.aiPlanner.Plan(replanSignal, mem)
	return plan
}

// emitGoalProgress sends a goal_progress SSE event via sink.
func emitGoalProgress(sink func(runtime.StreamEvent), payload map[string]any) {
	if sink == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	sink(runtime.StreamEvent{Type: envelope.SSEEventGoalProgress, Data: string(data)})
}

// maxCyclesForComplexity returns the configured max cycles for a complexity tier.
// Falls back to safe defaults if config values are zero.
func (s *Server) maxCyclesForComplexity(complexity string) int {
	defaults := map[string]int{"trivial": 1, "simple": 2, "multi_step": 5, "mission": 10}
	if s.config == nil {
		if d, ok := defaults[complexity]; ok {
			return d
		}
		return 1
	}
	var v int
	switch complexity {
	case "simple":
		v = s.config.Goal.MaxCycles.Simple
	case "multi_step":
		v = s.config.Goal.MaxCycles.MultiStep
	case "mission":
		v = s.config.Goal.MaxCycles.Mission
	default:
		v = s.config.Goal.MaxCycles.Trivial
	}
	if v > 0 {
		return v
	}
	if d, ok := defaults[complexity]; ok {
		return d
	}
	return 1
}

// runWithGoal executes the plan with a goal-aware evaluate/replan loop.
func (s *Server) runWithGoal(
	ctx context.Context,
	signal string,
	plan runtime.Plan,
	seed envelope.Envelope,
	goal *GoalData,
	goalID string,
	sink func(runtime.StreamEvent),
) envelope.Envelope {
	maxCycles := s.maxCyclesForComplexity(plan.Complexity)

	var lastOutput envelope.Envelope
	for cycle := 0; cycle < maxCycles; cycle++ {
		if ctx.Err() != nil {
			break
		}

		if cycle == 0 {
			if sink != nil {
				lastOutput = s.runtime.ExecuteStream(ctx, plan, seed, sink)
			} else {
				lastOutput = s.runtime.Execute(plan, seed)
			}
		} else {
			newPlan := s.replan(signal, goal, lastOutput)
			if newPlan == nil {
				break
			}
			emitGoalProgress(sink, map[string]any{
				"event": "replan", "cycle": cycle + 1,
			})
			if sink != nil {
				lastOutput = s.runtime.ExecuteStream(ctx, *newPlan, seed, sink)
			} else {
				lastOutput = s.runtime.Execute(*newPlan, seed)
			}
		}

		// No explicit goal = trivial/simple, single cycle.
		if goal == nil {
			return lastOutput
		}

		outcome := s.evaluateGoal(lastOutput, goal)
		emitGoalProgress(sink, map[string]any{
			"event":   "evaluate",
			"status":  outcome.Status,
			"summary": outcome.Summary,
		})

		switch outcome.Status {
		case "met":
			s.closeGoal(goalID, goal)
			return lastOutput

		case "blocked":
			s.blockGoal(goalID, goal, outcome.BlockedOn)
			return formatBlockedResponse(goal, outcome, lastOutput)

		case "not_met":
			goal.CycleCount++
			goalID = s.updateGoalProgress(goalID, goal, outcome)
			continue
		}
	}

	// Safety bound hit.
	if goal != nil {
		s.blockGoal(goalID, goal, "Reached maximum autonomous cycles")
		return formatSafetyBoundResponse(goal, lastOutput)
	}
	return lastOutput
}
