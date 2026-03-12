# Feature: Goal-Oriented Planning System

## Metadata

type: `feat`
task_id: `goal-oriented-planner`
prompt: `Convert Virgil's planner so that every invocation is goal-oriented. After every pipeline execution, an evaluate step checks the result against the goal. If not met, the planner replans and executes again within the same invocation -- until the goal is met, the planner is blocked, or a safety bound is reached.`

## Feature Description

The current signal loop is: `signal -> classify -> plan -> execute -> output`. This feature adds an evaluate step after every execution that checks the result against the goal and decides whether to replan:

```
signal -> classify -> derive goal -> plan -> execute -> evaluate
                                      ^                   |
                                      +-- replan <-- not met (autonomous)
                                                          |
                                                met -> done (close goal)
                                                blocked -> return to user (persist goal)
```

The evaluate step has three outcomes:

1. **Met** -- goal satisfied. Close the goal in memory. Return the final output.
2. **Not met, can continue** -- planner can make more autonomous progress. Replan with what was learned and execute again. Loops within the same invocation.
3. **Blocked** -- needs something it can't get autonomously (user input, auth, a decision). Persist goal state to memory. Return with a clear description of what's needed. The user's follow-up will be a new invocation that picks up the goal.

Goals are memory entries with `kind: "goal"` (already exists as `store.KindGoal`). All state lives in the store -- no new stateful types. The goal loop lives in the server's signal handler path where all deps are already available.

---

## Background

### Existing Infrastructure

**`store.KindGoal` already exists.** `store/kind.go` declares `KindGoal = "goal"` with `ConfidenceGoal = 0.9`. The `memories` table's `data` column (JSON blob) can hold goal schema fields with zero migration. `SaveKind`, `QueryByKind`, `QueryByKindFiltered`, `SearchByKind`, `SupersedeMemory`, and `FindByKindAndDataField` all work with goals today.

**The server orchestrates everything.** `server/api.go:handleSSE()` calls `router.Route()`, then `buildPlanForRoute()`, then `runtime.ExecuteStream()`. The server holds all deps via the `Deps` struct: store, planner, runtime, registry.

**The planner is stateless.** `planner.Plan()` does template matching, `aiPlanner.Plan()` is the Layer 4 fallback. Neither has goals, loops, or evaluation. The server's `buildPlanForRoute()` decides which planner to use.

**SSE streaming already supports pipeline progress.** `SSEEventPipelineProgress` exists and is handled by the client. Evaluate/replan transitions are just additional progress events.

**The decompose pipe exists.** It breaks tasks into phases -- reusable for mission-tier goal decomposition.

### What Changes

The server's signal handler gains a goal-aware execution loop. After routing and planning:

1. Query memory for active goals relevant to the signal.
2. Derive a goal if the signal complexity warrants it.
3. Execute the plan.
4. Evaluate the result against the goal.
5. If not met and within safety bounds, replan and loop back to step 3.

Each cycle is a separate `ExecuteStream` call. The evaluate/replan transitions emit SSE pipeline progress events.

---

## Relevant Files

### Existing Files (Modified)

- `internal/server/api.go` -- Add goal retrieval, goal derivation, evaluate/replan loop to `handleSSE` and `handleSignal`. New helper methods on `Server`.
- `internal/server/server.go` -- No structural changes; server already has all needed deps.
- `internal/planner/aiplanner.go` -- Extend AI planner response to include `complexity` field.
- `internal/config/config.go` -- Add `GoalConfig` to `Config` for max cycles per complexity tier.
- `internal/envelope/envelope.go` -- Add `SSEEventGoalProgress` constant.

### Existing Files (Reference)

- `internal/store/store.go` -- `SaveKind`, `QueryByKind`, `QueryByKindFiltered`, `SearchByKind`, `SupersedeMemory`, `FindByKindAndDataField`.
- `internal/store/kind.go` -- `KindGoal = "goal"`, `ConfidenceGoal = 0.9`.
- `internal/store/graph.go` -- `CreateEdge`, `RelationRefinedFrom`.
- `internal/runtime/runtime.go` -- `Plan`, `Step`, `Execute`, `ExecuteStream`, `StreamEvent`, `emitProgress`.
- `internal/planner/planner.go` -- Deterministic planner, `Planner.Plan()`.
- `internal/router/router.go` -- `RouteResult`, `LayerFallback`, `LayerExact`.
- `internal/bridge/bridge.go` -- `Provider`, `Complete`.
- `internal/pipes/decompose/` -- Decompose pipe for mission phase breakdown.

### New Files

- `internal/server/goal.go` -- Goal types (`GoalData`, `GoalPhase`), goal lifecycle methods (`retrieveActiveGoal`, `deriveGoal`, `evaluateGoal`, `updateGoalProgress`, `closeGoal`, `blockGoal`), and the `runWithGoal` loop.
- `internal/server/goal_test.go` -- Tests for goal derivation, evaluation, replan loop, safety bounds, follow-up handling.

---

## Goal Complexity Tiers

| Tier | Signal type | Goal handling |
|------|-------------|---------------|
| trivial | One-pipe query ("what time is it") | No goal object. Evaluate is implicit: pipe returned non-error output. |
| simple | 2-3 pipe chain ("draft a blog from my notes") | No goal object. Evaluate checks final output exists and is non-empty. |
| multi_step | Pipeline-level task ("add OAuth to Keep") | Goal written to memory. Evaluate checks against objective. Replanning possible. |
| mission | Open-ended, multi-invocation ("build me an app") | Goal written to memory immediately. Phase decomposition via decompose pipe. Expect blocked outcomes. |

### Complexity Classification

- **Layers 1-3 (deterministic):** Infer from plan structure. 1 step = trivial. 2-3 steps = simple. Named pipeline = multi_step.
- **Layer 4 (AI planner):** The AI planner already reasons about the signal. Extend its response schema to include a `complexity` field. The planner prompt instructs it to classify using the tier table above.

---

## Goal as a Memory Entry

Goals use `kind: "goal"` with structured data in the JSON `data` column. No migration needed.

### GoalData Schema

```go
type GoalData struct {
    Objective      string      `json:"objective"`
    Status         string      `json:"status"`          // "active", "blocked", "complete"
    Complexity     string      `json:"complexity"`       // "multi_step", "mission"
    BlockedOn      string      `json:"blocked_on,omitempty"`
    CycleCount     int         `json:"cycle_count"`
    Phases         []GoalPhase `json:"phases,omitempty"` // mission-tier only
    OriginalSignal string      `json:"original_signal"`
}

type GoalPhase struct {
    Name    string `json:"name"`
    Status  string `json:"status"`  // "pending", "active", "complete"
    Summary string `json:"summary,omitempty"`
}
```

### Goal Lifecycle in Memory

All state transitions use the chain approach -- new entry + `refined_from` edge, never mutations:

```
1. Planner creates goal
   -> store.SaveKind("goal", objective, goalData, tags, nil)

2. Execution cycle completes progress
   -> store.SupersedeMemory(oldID, updatedContent, updatedGoalData, tags)
   (SupersedeMemory creates refined_from edge + halves old confidence)

3. Planner hits a blocker
   -> store.SupersedeMemory(oldID, content, {status: "blocked", blocked_on: "..."}, tags)

4. User follows up, satisfies blocker
   -> store.SupersedeMemory(oldID, content, {status: "active", blocked_on: ""}, tags)

5. All phases complete
   -> store.SupersedeMemory(oldID, content, {status: "complete"}, tags)
   (goal ages through summarization like everything else)
```

---

## Safety Bounds

Maximum autonomous cycles per complexity tier:

| Complexity | Max cycles | Rationale |
|------------|-----------|-----------|
| trivial | 1 | One shot, no retry |
| simple | 2 | One retry if first attempt has a fixable issue |
| multi_step | 5 | Enough for a build-verify-fix pattern |
| mission | 10 | Enough to complete several phases before checking in |

Configurable in `virgil.yaml`:

```yaml
goal:
  max_cycles:
    trivial: 1
    simple: 2
    multi_step: 5
    mission: 10
```

When the safety bound is hit, the planner writes goal state to memory with `blocked_on: "Reached maximum autonomous cycles"` and returns a summary of what was accomplished.

---

## Implementation Plan

### Phase 1: Goal Types and Config

Create `internal/server/goal.go` with types and config.

**GoalData and GoalPhase structs** as defined above.

**GoalConfig in config.go:**

```go
type GoalConfig struct {
    MaxCycles GoalMaxCycles `yaml:"max_cycles"`
}

type GoalMaxCycles struct {
    Trivial   int `yaml:"trivial"`
    Simple    int `yaml:"simple"`
    MultiStep int `yaml:"multi_step"`
    Mission   int `yaml:"mission"`
}
```

Add `Goal GoalConfig` to `Config` struct. Set defaults in `Load()`:

```go
if cfg.Goal.MaxCycles.Trivial == 0 { cfg.Goal.MaxCycles.Trivial = 1 }
if cfg.Goal.MaxCycles.Simple == 0 { cfg.Goal.MaxCycles.Simple = 2 }
if cfg.Goal.MaxCycles.MultiStep == 0 { cfg.Goal.MaxCycles.MultiStep = 5 }
if cfg.Goal.MaxCycles.Mission == 0 { cfg.Goal.MaxCycles.Mission = 10 }
```

**SSE event constant** in `envelope.go`:

```go
SSEEventGoalProgress = "goal_progress"
```

### Phase 2: AI Planner Complexity Classification

Extend the AI planner to return a complexity field alongside the plan.

**Update `aiPlanResponse`** in `aiplanner.go`:

```go
type aiPlanResponse struct {
    Pipe       string            `json:"pipe"`
    Flags      map[string]string `json:"flags"`
    Steps      []aiPlanStep      `json:"steps"`
    Complexity string            `json:"complexity,omitempty"` // trivial, simple, multi_step, mission
}
```

**Update the system prompt** to include complexity classification instructions:

```
In addition to the plan, classify the signal's complexity:
- "trivial": single pipe, simple query
- "simple": 2-3 pipe chain, straightforward task
- "multi_step": pipeline-level task requiring verification or iteration
- "mission": open-ended, will need multiple sessions or user decisions

Include a "complexity" field in your response.
```

**Update `parseResponse`** to extract complexity from the response and store it on the returned plan.

**Add `Complexity` field to `runtime.Plan`:**

```go
type Plan struct {
    Steps                    []Step
    SkipFirstMemoryInjection bool
    Complexity               string // "trivial", "simple", "multi_step", "mission"
}
```

**Heuristic complexity for Layers 1-3** in `buildPlanForRoute`:

```go
func inferComplexity(plan runtime.Plan, route router.RouteResult) string {
    if pc := pipelineForRoute(route.Pipe); pc != nil {
        return "multi_step"
    }
    switch len(plan.Steps) {
    case 1:
        return "trivial"
    case 2, 3:
        return "simple"
    default:
        return "multi_step"
    }
}
```

### Phase 3: Goal Retrieval

Add goal retrieval to the server's signal handling, before planning.

**`retrieveActiveGoal` method on Server** in `goal.go`:

```go
func (s *Server) retrieveActiveGoal(signal string) (*store.Memory, *GoalData, error) {
    if s.store == nil {
        return nil, nil, nil
    }

    // Search for active/blocked goals relevant to the signal using FTS5
    goals, err := s.store.SearchByKind(signal, store.KindGoal, 5)
    if err != nil {
        return nil, nil, err
    }

    // Also check by status filter for any active goals (in case FTS misses)
    activeGoals, err := s.store.QueryByKindFiltered(store.KindGoal,
        map[string]any{"status": "active"}, 5)
    if err == nil {
        goals = append(goals, activeGoals...)
    }

    blockedGoals, err := s.store.QueryByKindFiltered(store.KindGoal,
        map[string]any{"status": "blocked"}, 5)
    if err == nil {
        goals = append(goals, blockedGoals...)
    }

    // Deduplicate by ID, pick the most relevant
    // Parse GoalData from the data column for each candidate
    // Return the best match (highest FTS relevance or most recent)
    // Return nil if no relevant goal found
}
```

This runs in `handleSSE` after routing, before `buildPlanForRoute`. If a relevant active or blocked goal is found, the planner treats the signal as a continuation.

For blocked goals: check if the user's signal satisfies the `blocked_on` requirement. The evaluate prompt can determine this.

### Phase 4: Goal Derivation

After classification and before execution, derive a goal if the complexity warrants it.

**`deriveGoal` method on Server** in `goal.go`:

```go
func (s *Server) deriveGoal(signal string, plan runtime.Plan, existingGoal *GoalData) (*GoalData, string, error) {
    // If continuing an existing goal, return it
    if existingGoal != nil {
        return existingGoal, existingGoalID, nil
    }

    // Trivial/simple: no goal object needed
    if plan.Complexity == "trivial" || plan.Complexity == "simple" {
        return nil, "", nil
    }

    // Multi-step/mission: create and persist goal
    goal := GoalData{
        Objective:      signal,
        Status:         "active",
        Complexity:     plan.Complexity,
        OriginalSignal: signal,
    }

    // Mission: decompose into phases using the decompose pipe
    if plan.Complexity == "mission" {
        phases := s.decomposeIntoPhases(signal)
        goal.Phases = phases
    }

    id, err := s.store.SaveKind(store.KindGoal, signal, goal, []string{"goal"}, nil)
    if err != nil {
        return nil, "", err
    }
    return &goal, id, nil
}
```

**`decomposeIntoPhases`** calls the decompose pipe as a single-step execution:

```go
func (s *Server) decomposeIntoPhases(signal string) []GoalPhase {
    seed := envelope.New("goal", "decompose")
    seed.Content = signal
    seed.ContentType = envelope.ContentText
    seed.Args["signal"] = signal

    plan := runtime.Plan{Steps: []runtime.Step{{Pipe: "decompose", Flags: map[string]string{}}}}
    result := s.runtime.Execute(plan, seed)

    // Parse decompose output into GoalPhase structs
    // The decompose pipe returns structured content with phase names
    // ...
}
```

### Phase 5: Evaluate Step

The core addition. After every pipeline execution, evaluate the result against the goal.

**`evaluateGoal` method on Server** in `goal.go`:

```go
type EvaluateOutcome struct {
    Status    string // "met", "not_met", "blocked"
    Summary   string
    BlockedOn string
    NextPhase string // for mission: which phase to work on next
}

func (s *Server) evaluateGoal(output envelope.Envelope, goal *GoalData) EvaluateOutcome {
    // Trivial/simple (no explicit goal): implicit check
    if goal == nil {
        if output.Error != nil && output.Error.Severity == envelope.SeverityFatal {
            return EvaluateOutcome{Status: "blocked", BlockedOn: output.Error.Message}
        }
        return EvaluateOutcome{Status: "met"}
    }

    // Multi-step / Mission: AI evaluation using the planner model
    prompt := buildEvaluatePrompt(output, goal)
    return s.aiEvaluate(prompt)
}
```

**`buildEvaluatePrompt`** constructs the evaluation prompt based on goal complexity:

For multi-step goals:

```
Given this goal: {{.Goal.Objective}}
And this execution result: {{.Output.Content}}

Has the goal been met?
Respond with exactly one of:
- MET: [one sentence explaining why]
- NOT_MET: [what's still missing, what to do next]
- BLOCKED: [what human input/action is needed and why]
```

For mission goals with phases:

```
Given this goal: {{.Goal.Objective}}
Current phase: {{.CurrentPhase.Name}}
Phase result: {{.Output.Content}}

1. Is this phase complete?
2. If yes, what should the next phase focus on?
3. Is anything blocked on human input?

Respond with exactly one of:
- PHASE_COMPLETE: [summary of what was accomplished, next phase recommendation]
- PHASE_INCOMPLETE: [what's still needed for this phase]
- BLOCKED: [what human input/action is needed]
- GOAL_COMPLETE: [all phases are done, final summary]
```

**`aiEvaluate`** calls the AI planner's provider (same model, `Config.Planner`) with the evaluate prompt and parses the structured response:

```go
func (s *Server) aiEvaluate(prompt string) EvaluateOutcome {
    if s.aiPlanner == nil {
        // No AI available -- treat as met to avoid infinite loops
        return EvaluateOutcome{Status: "met"}
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    response, err := s.plannerProvider.Complete(ctx, evaluateSystemPrompt, prompt)
    if err != nil {
        return EvaluateOutcome{Status: "met"} // fail open
    }

    return parseEvaluateResponse(response)
}
```

To give `goal.go` access to the planner's provider, expose it on `AIPlanner`:

```go
func (ap *AIPlanner) Provider() bridge.Provider {
    return ap.provider
}
```

### Phase 6: Goal Execution Loop

The core orchestration. Replace the single plan-execute in `handleSSE` with the goal loop.

**`runWithGoal` method on Server** in `goal.go`:

```go
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
        // Execute the current plan
        if cycle == 0 {
            lastOutput = s.runtime.ExecuteStream(ctx, plan, seed, sink)
        } else {
            // Replan: ask the AI planner for a new plan given what we learned
            newPlan := s.replan(signal, goal, lastOutput)
            if newPlan == nil {
                // Can't replan -- blocked
                break
            }
            emitProgress(sink, map[string]any{
                "type": "goal", "event": "replan", "cycle": cycle + 1,
            })
            lastOutput = s.runtime.ExecuteStream(ctx, *newPlan, seed, sink)
        }

        // No explicit goal = trivial/simple evaluate
        if goal == nil {
            return lastOutput
        }

        outcome := s.evaluateGoal(lastOutput, goal)
        emitProgress(sink, map[string]any{
            "type": "goal", "event": "evaluate",
            "status": outcome.Status, "summary": outcome.Summary,
        })

        switch outcome.Status {
        case "met":
            s.closeGoal(goalID, goal)
            return lastOutput

        case "blocked":
            s.blockGoal(goalID, goal, outcome.BlockedOn)
            return s.formatBlockedResponse(goal, outcome, lastOutput)

        case "not_met":
            goal.CycleCount++
            s.updateGoalProgress(goalID, goal, outcome)
            continue
        }
    }

    // Safety bound hit
    s.blockGoal(goalID, goal, "Reached maximum autonomous cycles")
    return s.formatSafetyBoundResponse(goal, lastOutput)
}

func (s *Server) maxCyclesForComplexity(complexity string) int {
    if s.config == nil {
        return 1
    }
    switch complexity {
    case "simple":
        return s.config.Goal.MaxCycles.Simple
    case "multi_step":
        return s.config.Goal.MaxCycles.MultiStep
    case "mission":
        return s.config.Goal.MaxCycles.Mission
    default:
        return s.config.Goal.MaxCycles.Trivial
    }
}
```

**Goal state management helpers:**

```go
func (s *Server) closeGoal(goalID string, goal *GoalData) {
    if s.store == nil || goalID == "" {
        return
    }
    goal.Status = "complete"
    s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"})
}

func (s *Server) blockGoal(goalID string, goal *GoalData, blockedOn string) {
    if s.store == nil || goalID == "" {
        return
    }
    goal.Status = "blocked"
    goal.BlockedOn = blockedOn
    s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"})
}

func (s *Server) updateGoalProgress(goalID string, goal *GoalData, outcome EvaluateOutcome) {
    if s.store == nil || goalID == "" {
        return
    }
    // Update phase status for mission goals
    if outcome.NextPhase != "" {
        for i := range goal.Phases {
            if goal.Phases[i].Status == "active" {
                goal.Phases[i].Status = "complete"
                goal.Phases[i].Summary = outcome.Summary
            }
        }
        for i := range goal.Phases {
            if goal.Phases[i].Name == outcome.NextPhase {
                goal.Phases[i].Status = "active"
                break
            }
        }
    }
    s.store.SupersedeMemory(goalID, goal.Objective, goal, []string{"goal"})
}
```

**`replan` method** -- asks the AI planner for a new plan incorporating what was learned:

```go
func (s *Server) replan(signal string, goal *GoalData, lastOutput envelope.Envelope) *runtime.Plan {
    if s.aiPlanner == nil {
        return nil
    }
    // Build a richer signal that includes goal context and what was learned
    replanSignal := fmt.Sprintf(
        "Continue working on: %s\n\nLast result: %s\n\nWhat to do next based on goal progress.",
        goal.Objective, envelope.ContentString(lastOutput),
    )
    mem := s.buildPlannerMemory()
    plan, _ := s.aiPlanner.Plan(replanSignal, mem)
    return plan
}
```

### Phase 7: Wire Into Signal Handlers

**Modify `handleSSE`** in `api.go` to use the goal loop:

```go
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, req signalRequest, parsed parser.ParsedSignal) {
    // ... existing setup (flusher, seed, route, mu, sseSink) ...

    // Check for active goals relevant to this signal (before planning)
    existingGoalMem, existingGoal, _ := s.retrieveActiveGoal(req.Text)

    // ... existing pipeline check ...

    // ... existing Layer 4 ack + buildPlanForRoute ...

    // Infer complexity for deterministic plans
    if plan.Complexity == "" {
        plan.Complexity = inferComplexity(plan, route)
    }

    // Derive goal if complexity warrants it
    var goalID string
    var goal *GoalData
    if existingGoal != nil {
        goal = existingGoal
        goalID = existingGoalMem.ID
    } else {
        goal, goalID, _ = s.deriveGoal(req.Text, plan, nil)
    }

    // ... existing memory prefetch ...

    // Execute with goal loop instead of single execution
    result := s.runWithGoal(r.Context(), req.Text, plan, execSeed, goal, goalID, sseSink)

    if ackDone != nil {
        <-ackDone
    }
    mu.Lock()
    sse.WriteJSON(w, flusher, envelope.SSEEventDone, result)
    mu.Unlock()
}
```

The sync `handleSignal` path gets the same treatment, but with `runWithGoal` using a nil sink.

---

## How Follow-Ups Work

No special continuation code path. The existing flow handles it naturally:

1. Signal arrives: "here's the WiFi password: hunter2"
2. Router classifies normally.
3. `retrieveActiveGoal` finds the blocked goal via `SearchByKind` (FTS5 matches on objective + blocked_on text) and `QueryByKindFiltered` (status = "blocked").
4. The planner sees the goal is blocked on "WiFi password for Thread bridge" and the user's signal contains the answer.
5. `deriveGoal` returns the existing goal (not a new one).
6. The goal gets unblocked via `SupersedeMemory` (new entry with `status: "active"`, `blocked_on: ""`).
7. `replan` creates a plan for the next phase incorporating the user's input.
8. Execution resumes. Evaluate loop runs as normal.

**Relevance matching between signal and goal:** Uses `SearchByKind(signal, "goal", 5)` -- the existing FTS5 index on `content` and `signal` columns. If the signal is clearly about the goal (mentions same project, answers the blocked question), FTS5 ranks it high. Unrelated signals ("what's on my calendar") don't match active goals, so they process normally.

---

## What the User Sees

### Simple goal (one cycle):

```
> draft a blog post from my recent church tech notes

  Here's a draft:

  [blog post content]
```

No difference from current behavior. Evaluate ran implicitly, determined the goal was met.

### Multi-step goal (multiple cycles, one invocation):

```
> add OAuth login to Keep

  * Starting dev-feature pipeline...
  * spec complete
  * preparing (3 parallel branches)
  * building...
  * verify failed, fixing (attempt 2)
  * verify passed, publishing PR

  dev-feature complete. PR #47 ready for review.
```

Multiple plan-execute-evaluate cycles, all within one invocation. Evaluate/replan transitions appear as pipeline progress events.

### Mission (spans multiple invocations):

```
> build me a home automation app that discovers my network devices
  and has a 3D floorplan of my apartment

  * Decomposed into 5 phases: network discovery, backend, frontend,
    device pairing, 3D floorplan.
  * Starting phase 1: network discovery...
  * Found 6 HAP devices via mDNS
  * Phase 1 complete.
  * Starting phase 2: backend scaffolding...
  * Go backend with REST + WebSocket ready
  * Phase 2 complete.
  * Starting phase 3: frontend...

  I've completed network discovery (16 devices found) and the Go
  backend. I'm starting on the frontend but I need to know: do you
  want React or a different framework? Also, for the Thread bridge
  devices, I'll need your HomeKit pairing code to proceed with
  direct device control.

> React. And the pairing code is 123-45-678

  * Resuming from phase 3: frontend...
  * React frontend scaffolded
  * Phase 3 complete, phase 4 complete.
  * Starting phase 5: 3D floorplan...
```

Each `>` is a separate invocation. The planner picked up the goal from memory, saw the user's response, and continued.

---

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Add Goal Config

- In `internal/config/config.go`, add `GoalConfig` and `GoalMaxCycles` structs
- Add `Goal GoalConfig` field to `Config` struct
- In `Load()`, set defaults: trivial=1, simple=2, multi_step=5, mission=10

### 2. Add Complexity to Plan

- In `internal/runtime/runtime.go`, add `Complexity string` field to `Plan` struct
- In `internal/planner/aiplanner.go`, add `Complexity string` field to `aiPlanResponse`
- Update `parseResponse` to propagate complexity to the returned plan
- Update the AI planner system prompt to instruct complexity classification

### 3. Add SSE Event Constant

- In `internal/envelope/envelope.go`, add `SSEEventGoalProgress = "goal_progress"`

### 4. Add Complexity Inference for Deterministic Plans

- In `internal/server/api.go`, add `inferComplexity(plan, route)` function
- 1 step = trivial, 2-3 steps = simple, named pipeline = multi_step

### 5. Create Goal Types

- Create `internal/server/goal.go` with `GoalData` and `GoalPhase` structs
- Add `parseGoalData(mem store.Memory) (*GoalData, error)` helper to unmarshal from `Memory.Data`

### 6. Add Goal Retrieval

- In `goal.go`, add `retrieveActiveGoal(signal string) (*store.Memory, *GoalData, error)` method on Server
- Search with `SearchByKind(signal, "goal", 5)` for FTS relevance
- Also query `QueryByKindFiltered("goal", {status: "active"}, 5)` and `QueryByKindFiltered("goal", {status: "blocked"}, 5)`
- Deduplicate by ID, pick best match (FTS hit > active > blocked)
- Return nil if no relevant goal found

### 7. Add Goal Derivation

- In `goal.go`, add `deriveGoal(signal string, plan runtime.Plan, existingGoal *GoalData) (*GoalData, string, error)` method on Server
- Trivial/simple complexity: return nil (no goal)
- Multi-step/mission: create GoalData, persist with `store.SaveKind("goal", ...)`
- Mission: call `decomposeIntoPhases(signal)` to get phases
- Add `decomposeIntoPhases(signal string) []GoalPhase` -- executes decompose pipe as single-step plan, parses output into GoalPhase structs

### 8. Add Evaluate Step

- In `goal.go`, add `EvaluateOutcome` struct
- Add `evaluateGoal(output envelope.Envelope, goal *GoalData) EvaluateOutcome`
- Nil goal: implicit check (non-fatal output = met)
- Non-nil goal: call AI planner's provider with evaluate prompt
- Add `buildEvaluatePrompt(output, goal)` -- multi-step prompt for multi_step, phase-aware prompt for mission
- Add `parseEvaluateResponse(response string) EvaluateOutcome` -- parse MET/NOT_MET/BLOCKED/PHASE_COMPLETE/GOAL_COMPLETE
- Expose `Provider()` on `AIPlanner` so `goal.go` can call it for evaluation

### 9. Add Goal State Management

- In `goal.go`, add `closeGoal(goalID string, goal *GoalData)` -- supersede with status=complete
- Add `blockGoal(goalID string, goal *GoalData, blockedOn string)` -- supersede with status=blocked
- Add `updateGoalProgress(goalID string, goal *GoalData, outcome EvaluateOutcome)` -- update phases, supersede
- Add `formatBlockedResponse(goal, outcome, lastOutput) envelope.Envelope` -- conversational response describing what's needed
- Add `formatSafetyBoundResponse(goal, lastOutput) envelope.Envelope` -- summary of accomplished + remaining

### 10. Add Replan Logic

- In `goal.go`, add `replan(signal string, goal *GoalData, lastOutput envelope.Envelope) *runtime.Plan`
- Build enriched signal from goal objective + last result
- Call `aiPlanner.Plan()` with the enriched signal
- Return nil if AI planner unavailable or returns no plan

### 11. Add Goal Execution Loop

- In `goal.go`, add `runWithGoal(ctx, signal, plan, seed, goal, goalID, sink) envelope.Envelope`
- Execute plan, evaluate, replan loop as specified
- Each cycle calls `ExecuteStream` separately
- Emit `goal_progress` SSE events at evaluate/replan transitions
- Add `maxCyclesForComplexity(complexity string) int` helper

### 12. Wire Into Signal Handlers

- In `api.go:handleSSE`, add `retrieveActiveGoal` call before `buildPlanForRoute`
- Add `inferComplexity` call after plan is built
- Add `deriveGoal` call
- Replace `s.runtime.ExecuteStream(...)` with `s.runWithGoal(...)`
- In `api.go:handleSignal`, same changes for the sync path (nil sink)

### 13. Handle SSE Goal Events in Client

- In `internal/tui/` (SSE sink handler in `handleSSE`), add case for `SSEEventGoalProgress`
- Render goal progress events as pipeline notification lines

### 14. Write Tests

- Create `internal/server/goal_test.go`
- `TestGoalDeriveTrivia` -- trivial complexity returns nil goal
- `TestGoalDeriveMultiStep` -- multi_step creates and persists goal
- `TestGoalDeriveMission` -- mission creates goal with phases
- `TestGoalRetrieveActive` -- retrieves active goal matching signal
- `TestGoalRetrieveBlocked` -- retrieves blocked goal, checks blocked_on
- `TestGoalRetrieveUnrelated` -- unrelated signal returns nil
- `TestEvaluateNilGoal` -- implicit check, non-error = met
- `TestEvaluateGoalMet` -- AI returns MET, goal closed
- `TestEvaluateGoalNotMet` -- AI returns NOT_MET, loop continues
- `TestEvaluateGoalBlocked` -- AI returns BLOCKED, goal persisted
- `TestRunWithGoalSingleCycle` -- trivial goal completes in one cycle
- `TestRunWithGoalReplan` -- not_met triggers replan, succeeds on second cycle
- `TestRunWithGoalSafetyBound` -- max cycles reached, goal blocked
- `TestRunWithGoalMissionPhases` -- phase progression through cycles
- `TestFollowUpUnblocksGoal` -- user signal satisfies blocked_on
- `TestInferComplexity` -- 1 step = trivial, 2 steps = simple, pipeline = multi_step

---

## Testing Strategy

### Unit Tests

- **Goal types** (`goal_test.go`): GoalData serialization/deserialization, parseGoalData from Memory.
- **Complexity inference**: Plan step count heuristic, AI planner complexity field propagation.
- **Evaluate parsing**: MET/NOT_MET/BLOCKED/PHASE_COMPLETE/GOAL_COMPLETE response parsing.
- **Goal lifecycle**: create -> active -> blocked -> active -> complete chain with SupersedeMemory.

### Integration Tests

- **Full loop**: Mock AI provider that returns evaluate responses. Verify the replan loop executes correct number of cycles.
- **Goal retrieval**: Seed store with active/blocked goals, verify correct one is retrieved for a signal.
- **Follow-up flow**: Create blocked goal, send follow-up signal, verify goal unblocked and execution resumes.

### Edge Cases

- AI planner unavailable (nil) -- evaluate defaults to "met" (fail open, no infinite loops)
- Store unavailable (nil) -- all goal operations are no-ops, single cycle execution
- Empty output from pipe -- evaluate can still reason about it
- Concurrent goal execution -- each invocation operates independently on store state
- Multiple active goals -- retrieval picks the most relevant one, not all
- User signal unrelated to any active goal -- processed normally, goals not hijacked

---

## Risk Assessment

**Latency.** Each evaluate cycle adds an AI call (~1-2s with Grok 4 Fast). For trivial/simple signals, evaluate is implicit (no AI call) -- zero latency impact. For multi_step, up to 5 cycles x ~2s = ~10s of evaluate overhead. The user sees pipeline progress throughout. For mission, up to 10 cycles -- but mission tasks are expected to take minutes.

**Infinite loops.** Safety bounds prevent runaway execution. On any AI failure, evaluate defaults to "met" (fail open). If the AI planner can't produce a replan, the loop breaks.

**Memory growth.** Each goal state transition creates a new entry via `SupersedeMemory`. Old entries get their confidence halved and eventually age out through summarization. Active goals are bounded by the small number of concurrent tasks a user would have.

**No circular imports.** `goal.go` lives in `internal/server/` and imports `store`, `runtime`, `envelope`, `planner` -- all existing dependencies of the server package. No new import cycles.

---

## Validation Commands

```bash
just build   # compiles cleanly
just test    # all tests pass
just lint    # no lint violations
```

---

## Assumptions

None -- all decisions resolved during exploration.
