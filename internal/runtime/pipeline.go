package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/slug"
)

// LoopState tracks the execution state of one loop during pipeline execution.
type LoopState struct {
	Config    config.LoopConfig
	Iteration int                   // 1-indexed current iteration
	History   []LoopIterationRecord // results from each completed iteration
	until     Condition             // parsed once at init time
}

// Reset clears the iteration counter and history. Called by the future cycle
// primitive when it jumps back to a step that precedes the loop, giving the
// loop a fresh attempt budget.
func (ls *LoopState) Reset() {
	ls.Iteration = 0
	ls.History = nil
}

// LoopIterationRecord captures the outcome of one complete loop iteration.
type LoopIterationRecord struct {
	Iteration int
	Steps     []StepRecord
	Satisfied bool // true if the until condition was met after this iteration
}

// StepRecord captures one step's execution within a loop iteration.
type StepRecord struct {
	Step     string
	Duration time.Duration
	Error    *envelope.EnvelopeError
	Skipped  bool // true if the step's condition evaluated to false
}

// CycleState tracks one cycle's execution state (iteration count).
type CycleState struct {
	config config.CycleConfig
	cond   Condition
	count  int // how many times this cycle has fired
}

// PipelineExecutor runs a PipelineConfig, handling sequential steps, parallel
// fan-out, graph execution, loops, and cycles. It is a separate type from
// Runtime — it composes Runtime.runStep internally.
type PipelineExecutor struct {
	runtime    *Runtime
	config     config.PipelineConfig
	ctx        map[string]any        // context map: "step.field" -> value
	loops      map[string]*LoopState // loop name -> state
	loopRanges map[int]string        // step index -> loop name (first step of each loop)
	cycles     []*CycleState         // cycle declarations in order
	stepIndex  map[string]int        // step name -> index in config.Steps
	stepConds  map[string]Condition  // step name -> pre-parsed condition (only for steps with conditions)
	observer   Observer
	logger     *slog.Logger
	sink       func(StreamEvent)     // optional SSE sink for streaming progress
}

// NewPipelineExecutor constructs a PipelineExecutor and validates the pipeline
// config. Returns an error if any loop references an unknown step or has an
// unparseable until condition.
func NewPipelineExecutor(rt *Runtime, cfg config.PipelineConfig, observer Observer, logger *slog.Logger) (*PipelineExecutor, error) {
	if observer == nil {
		observer = &noopObserver{}
	}
	if logger == nil {
		logger = slog.Default()
	}

	pe := &PipelineExecutor{
		runtime:    rt,
		config:     cfg,
		ctx:        make(map[string]any),
		loops:      make(map[string]*LoopState),
		loopRanges: make(map[int]string),
		stepIndex:  make(map[string]int),
		stepConds:  make(map[string]Condition),
		observer:   observer,
		logger:     logger,
	}

	// Build step name -> index map and pre-parse step conditions.
	for i, s := range cfg.Steps {
		pe.stepIndex[s.Name] = i
		if s.Condition != "" {
			cond, err := ParseCondition(s.Condition)
			if err != nil {
				return nil, fmt.Errorf("step %q: invalid condition %q: %w", s.Name, s.Condition, err)
			}
			pe.stepConds[s.Name] = cond
		}
	}

	// Validate and pre-compile each loop.
	for _, lc := range cfg.Loops {
		untilCond, err := ParseCondition(lc.Until)
		if err != nil {
			return nil, fmt.Errorf("loop %q: invalid until condition %q: %w", lc.Name, lc.Until, err)
		}

		ls := &LoopState{
			Config: lc,
			until:  untilCond,
		}
		pe.loops[lc.Name] = ls

		// Register the first step of each loop in loopRanges so Execute can
		// detect loop boundaries by step index.
		if len(lc.Steps) == 0 {
			return nil, fmt.Errorf("loop %q: steps list is empty", lc.Name)
		}
		firstName := lc.Steps[0]
		firstIdx, ok := pe.stepIndex[firstName]
		if !ok {
			return nil, fmt.Errorf("loop %q: step %q not found in pipeline steps", lc.Name, firstName)
		}
		// Validate all loop steps exist.
		for _, sn := range lc.Steps {
			if _, ok := pe.stepIndex[sn]; !ok {
				return nil, fmt.Errorf("loop %q: step %q not found in pipeline steps", lc.Name, sn)
			}
		}
		pe.loopRanges[firstIdx] = lc.Name
	}

	// Validate and pre-compile each cycle.
	for _, cc := range cfg.Cycles {
		cond, err := ParseCondition(cc.Condition)
		if err != nil {
			return nil, fmt.Errorf("cycle %q: invalid condition %q: %w", cc.Name, cc.Condition, err)
		}
		pe.cycles = append(pe.cycles, &CycleState{
			config: cc,
			cond:   cond,
		})
	}

	return pe, nil
}

// Execute runs the pipeline from the seed envelope and returns the final output.
// Steps execute sequentially. When a step index is the start of a loop, the
// loop executor takes over until the loop exits, then execution resumes at the
// step after the loop's last step. Cycles jump backward when their condition
// is met after the From step.
func (pe *PipelineExecutor) Execute(seed envelope.Envelope) envelope.Envelope {
	// Seed built-in variables from the envelope args so templates like
	// {{signal}} and {{feature}} resolve correctly.
	for k, v := range seed.Args {
		pe.ctx[k] = v
	}

	current := seed
	i := 0

	for i < len(pe.config.Steps) {
		// Check if this step starts a loop.
		if loopName, isLoopStart := pe.loopRanges[i]; isLoopStart {
			loopState := pe.loops[loopName]
			result := pe.executeLoop(loopState, current)
			if isFatal(result) {
				return result
			}
			current = result
			// Advance past the loop's last step.
			lastStepName := loopState.Config.Steps[len(loopState.Config.Steps)-1]
			lastIdx := pe.stepIndex[lastStepName]
			i = lastIdx + 1

			// Check for cycle after the loop's last step.
			if jumpTo, ok := pe.checkCycle(lastStepName); ok {
				i = jumpTo
			}
			continue
		}

		// Execute single step.
		step := pe.config.Steps[i]
		current = pe.executeSingleStep(step, current)
		if isFatal(current) {
			return current
		}

		// Check for cycle after this step.
		if jumpTo, ok := pe.checkCycle(step.Name); ok {
			i = jumpTo
			continue
		}

		i++
	}

	return current
}

// ExecuteStream runs the pipeline with an SSE sink for progress events.
// Pipeline progress (loop iterations, cycle triggers, graph levels) is
// emitted as SSEEventPipelineProgress events via the sink.
func (pe *PipelineExecutor) ExecuteStream(seed envelope.Envelope, sink func(StreamEvent)) envelope.Envelope {
	pe.sink = sink
	return pe.Execute(seed)
}

// emitProgress sends a pipeline_progress SSE event if a sink is set.
func (pe *PipelineExecutor) emitProgress(payload map[string]any) {
	emitProgress(pe.sink, payload)
}

// evalStepCondition checks whether a step's pre-parsed condition allows execution.
// Returns true if the step should run, false if it should be skipped.
func (pe *PipelineExecutor) evalStepCondition(stepName string) bool {
	cond, ok := pe.stepConds[stepName]
	if !ok {
		return true // no condition — always run
	}
	return cond.Evaluate(pe.ctx)
}

// runPipeStep resolves args, runs the step through the runtime, and updates context.
func (pe *PipelineExecutor) runPipeStep(step config.PipelineStepConfig, current envelope.Envelope) envelope.Envelope {
	resolvedArgs := pe.resolveArgs(step.Args)
	result := pe.runtime.runStep(Step{Pipe: step.Pipe, Flags: resolvedArgs}, current)
	pe.updateContext(step.Name, result)
	return result
}

// executeSingleStep runs one pipeline step, evaluating its condition first.
// Dispatches to the appropriate handler based on step type: parallel, graph,
// or simple pipe step.
func (pe *PipelineExecutor) executeSingleStep(step config.PipelineStepConfig, current envelope.Envelope) envelope.Envelope {
	if !pe.evalStepCondition(step.Name) {
		pe.logger.Debug("step skipped", "step", step.Name, "condition", step.Condition)
		return current
	}
	if len(step.Parallel) > 0 {
		return pe.executeParallel(step, current)
	}
	if step.Graph != nil {
		return pe.executeGraph(step, current)
	}
	return pe.runPipeStep(step, current)
}

// executeParallel fans out branches concurrently and merges their text outputs
// with role headers. On on_branch_failure: halt, returns the first fatal error.
func (pe *PipelineExecutor) executeParallel(step config.PipelineStepConfig, input envelope.Envelope) envelope.Envelope {
	type branchResult struct {
		role   string
		result envelope.Envelope
	}

	results := make([]branchResult, len(step.Parallel))
	var wg sync.WaitGroup

	for i, branch := range step.Parallel {
		i, branch := i, branch
		wg.Add(1)
		go func() {
			defer wg.Done()
			args := pe.resolveArgs(branch.Args)
			result := pe.runtime.runStep(Step{Pipe: branch.Pipe, Flags: args}, input)
			role := branch.Args["role"]
			if role == "" {
				role = branch.Pipe
			}
			results[i] = branchResult{role: role, result: result}
		}()
	}

	wg.Wait()

	// Check for fatal errors in halt mode.
	if step.OnBranchFailure == "halt" {
		for _, r := range results {
			if isFatal(r.result) {
				return r.result
			}
		}
	}

	// Merge text outputs with role headers.
	var sb strings.Builder
	for _, r := range results {
		header := r.role
		if len(header) > 0 {
			header = strings.ToUpper(header[:1]) + header[1:]
		}
		sb.WriteString(fmt.Sprintf("[%s perspective]\n", header))
		text := envelope.ContentToText(r.result.Content, r.result.ContentType)
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}

	out := envelope.New(step.Name, "parallel-merge")
	out.Content = strings.TrimSpace(sb.String())
	out.ContentType = envelope.ContentText
	pe.updateContext(step.Name, out)
	return out
}

// executeGraph reads a task DAG from the context map and runs it through the
// GraphExecutor. The source field specifies which context key holds the tasks.
func (pe *PipelineExecutor) executeGraph(step config.PipelineStepConfig, input envelope.Envelope) envelope.Envelope {
	gcfg := step.Graph

	tasksRaw, ok := pe.ctx[gcfg.Source]
	if !ok {
		out := envelope.New(step.Name, "graph")
		out.Error = envelope.FatalError(fmt.Sprintf("graph source %q not found in context", gcfg.Source))
		return out
	}

	tasks, err := toTaskNodes(tasksRaw)
	if err != nil {
		out := envelope.New(step.Name, "graph")
		out.Error = envelope.FatalError(fmt.Sprintf("graph source %q: %v", gcfg.Source, err))
		return out
	}

	cfg := GraphConfig{
		Pipe:          gcfg.Pipe,
		Args:          pe.resolveArgs(gcfg.Args),
		OnTaskFailure: gcfg.OnTaskFailure,
		MaxParallel:   gcfg.MaxParallel,
	}

	ge := NewGraphExecutor(pe.runtime.registry, pe.observer, pe.logger)
	ge.sink = pe.sink
	result := ge.Execute(context.Background(), cfg, tasks, input)
	pe.updateContext(step.Name, result)
	return result
}

// checkCycle evaluates cycle conditions after a step completes. If a cycle's
// From matches and its condition is true, it extracts carry fields, resets
// intermediate loops, and returns the To step index. Returns false if no
// cycle fires or if the cycle's max has been exceeded.
func (pe *PipelineExecutor) checkCycle(stepName string) (int, bool) {
	for _, cs := range pe.cycles {
		if cs.config.From != stepName {
			continue
		}
		if !cs.cond.Evaluate(pe.ctx) {
			continue
		}
		cs.count++
		if cs.config.Max > 0 && cs.count > cs.config.Max {
			pe.logger.Warn("cycle exhausted",
				"cycle", cs.config.Name,
				"max", cs.config.Max,
			)
			continue
		}

		// Extract carry field from the From step's context.
		if cs.config.Carry != "" {
			carryKey := stepName + "." + cs.config.Carry
			if val, ok := pe.ctx[carryKey]; ok {
				pe.ctx[cs.config.Carry] = val
			}
		}

		// Reset loops between To and From so they get fresh attempt budgets.
		toIdx := pe.stepIndex[cs.config.To]
		fromIdx := pe.stepIndex[cs.config.From]
		for loopStartIdx, loopName := range pe.loopRanges {
			if loopStartIdx >= toIdx && loopStartIdx <= fromIdx {
				pe.loops[loopName].Reset()
			}
		}

		pe.logger.Info("cycle triggered",
			"cycle", cs.config.Name,
			"iteration", cs.count,
			"from", cs.config.From,
			"to", cs.config.To,
		)
		pe.emitProgress(map[string]any{
			"type":  "cycle",
			"name":  cs.config.Name,
			"cycle": cs.count,
			"max":   cs.config.Max,
		})
		return toIdx, true
	}
	return 0, false
}

// executeLoop runs one named loop to completion, returning the final envelope.
// It implements the verify-fix loop algorithm described in the feature spec:
//
//  1. Set ctx["loop.iteration"] at the start of each iteration.
//  2. Execute each step in the loop body (skipping those whose condition is false).
//  3. If any step returns a fatal error, abort immediately.
//  4. Evaluate the until condition; exit if satisfied.
//  5. Increment iteration and repeat up to max times.
//  6. On exhaustion, return a failure envelope with full history.
func (pe *PipelineExecutor) executeLoop(loop *LoopState, input envelope.Envelope) envelope.Envelope {
	loop.Iteration = 0
	current := input

	for {
		loop.Iteration++
		pe.ctx["loop.iteration"] = loop.Iteration

		pe.logger.Info("loop iteration start",
			"loop", loop.Config.Name,
			"iteration", loop.Iteration,
			"max", loop.Config.Max,
		)
		pe.emitProgress(map[string]any{
			"type":      "loop",
			"name":      loop.Config.Name,
			"iteration": loop.Iteration,
			"max":       loop.Config.Max,
		})
		pe.observer.OnTransition(
			fmt.Sprintf("loop:%s", loop.Config.Name),
			envelope.New("pipeline", fmt.Sprintf("iteration-start:%d", loop.Iteration)),
			0,
		)

		// Execute each step in the loop body.
		record := LoopIterationRecord{Iteration: loop.Iteration}
		for _, stepName := range loop.Config.Steps {
			stepIdx := pe.stepIndex[stepName]
			step := pe.config.Steps[stepIdx]

			stepRecord := StepRecord{Step: stepName}
			stepStart := time.Now()

			if !pe.evalStepCondition(stepName) {
				pe.logger.Debug("loop step skipped", "loop", loop.Config.Name, "step", stepName, "iteration", loop.Iteration)
				stepRecord.Skipped = true
				stepRecord.Duration = time.Since(stepStart)
				record.Steps = append(record.Steps, stepRecord)
				continue
			}

			result := pe.runPipeStep(step, current)
			stepRecord.Duration = time.Since(stepStart)
			stepRecord.Error = result.Error

			// Fatal error: abort the loop immediately.
			if isFatal(result) {
				record.Steps = append(record.Steps, stepRecord)
				loop.History = append(loop.History, record)
				pe.logger.Error("loop aborted: fatal step error",
					"loop", loop.Config.Name,
					"step", stepName,
					"iteration", loop.Iteration,
					"error", result.Error.Message,
				)
				return result
			}

			current = result
			record.Steps = append(record.Steps, stepRecord)
		}

		// Evaluate the until condition.
		satisfied := loop.until.Evaluate(pe.ctx)
		record.Satisfied = satisfied
		loop.History = append(loop.History, record)

		pe.observer.OnTransition(
			fmt.Sprintf("loop:%s", loop.Config.Name),
			envelope.New("pipeline", fmt.Sprintf("iteration-end:%d", loop.Iteration)),
			0,
		)
		pe.logger.Info("loop iteration end",
			"loop", loop.Config.Name,
			"iteration", loop.Iteration,
			"satisfied", satisfied,
		)

		if satisfied {
			pe.observer.OnTransition(
				fmt.Sprintf("loop:%s", loop.Config.Name),
				envelope.New("pipeline", "loop-exit:satisfied"),
				0,
			)
			pe.logger.Info("loop exited: condition satisfied",
				"loop", loop.Config.Name,
				"iterations", loop.Iteration,
			)
			return current
		}

		// Check iteration cap.
		if loop.Config.Max > 0 && loop.Iteration >= loop.Config.Max {
			out := loopExhaustedEnvelope(loop)
			pe.observer.OnTransition(
				fmt.Sprintf("loop:%s", loop.Config.Name),
				out,
				0,
			)
			pe.logger.Warn("loop exhausted",
				"loop", loop.Config.Name,
				"iterations", loop.Iteration,
				"max", loop.Config.Max,
			)
			return out
		}
	}
}

// updateContext writes step output fields into the context map under
// "stepname.fieldname" keys, plus "stepname.error" for the error field.
// This makes step results available for condition evaluation and template
// resolution in subsequent steps.
//
// The error key is stored as an untyped nil (not a typed *EnvelopeError nil)
// so that Condition.Evaluate's nil check (`val == nil`) works correctly.
// A typed nil stored as interface{} is not equal to nil — Go's interface nil
// comparison only holds when both the type and value are nil.
func (pe *PipelineExecutor) updateContext(stepName string, result envelope.Envelope) {
	// Store error as untyped nil when absent so null-check conditions work.
	if result.Error != nil {
		pe.ctx[stepName+".error"] = result.Error
	} else {
		pe.ctx[stepName+".error"] = nil
	}

	// For structured content, expand top-level fields into individual keys.
	if result.ContentType == envelope.ContentStructured && result.Content != nil {
		m := normalizeToMap(result.Content)
		for k, v := range m {
			pe.ctx[stepName+"."+k] = v
		}
	}
}

// templateVar matches {{key}} and {{key | filter}} placeholders in arg values.
var templateVar = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// resolveArgs returns a copy of args with {{key}} and {{key | filter}}
// placeholders replaced by values from the context map. Missing keys
// resolve to an empty string. Supported filters: slugify, lower, upper.
func (pe *PipelineExecutor) resolveArgs(args map[string]string) map[string]string {
	if len(args) == 0 {
		return nil
	}

	resolved := make(map[string]string, len(args))
	for k, v := range args {
		resolved[k] = templateVar.ReplaceAllStringFunc(v, func(match string) string {
			inner := strings.TrimSpace(match[2 : len(match)-2])

			key, filter, hasFilter := strings.Cut(inner, "|")
			key = strings.TrimSpace(key)
			if hasFilter {
				filter = strings.TrimSpace(filter)
			}

			val, ok := pe.ctx[key]
			if !ok {
				return ""
			}

			s := contextValueToString(val)
			if hasFilter {
				s = applyFilter(s, filter)
			}
			return s
		})
	}
	return resolved
}

// contextValueToString converts a context map value to a string suitable for
// template interpolation. Complex types (maps, slices) are JSON-serialized.
func contextValueToString(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

// applyFilter applies a named filter to a string value.
func applyFilter(s, filter string) string {
	switch filter {
	case "slugify":
		return slug.Slugify(s)
	case "lower":
		return strings.ToLower(s)
	case "upper":
		return strings.ToUpper(s)
	default:
		return s
	}
}

// toTaskNodes converts a context map value to a slice of TaskNode via JSON
// round-trip. Handles []TaskNode directly, or any JSON-serializable type.
func toTaskNodes(v any) ([]TaskNode, error) {
	if nodes, ok := v.([]TaskNode); ok {
		return nodes, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal tasks: %w", err)
	}
	var nodes []TaskNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("unmarshal tasks: %w", err)
	}
	return nodes, nil
}

// loopExhaustedEnvelope builds a fatal failure envelope when a loop reaches
// its max iteration cap without satisfying the until condition.
func loopExhaustedEnvelope(loop *LoopState) envelope.Envelope {
	out := envelope.New("pipeline", "loop-exhausted")
	out.ContentType = envelope.ContentStructured
	out.Content = map[string]any{
		"loop":       loop.Config.Name,
		"iterations": loop.Iteration,
		"max":        loop.Config.Max,
		"history":    loop.History,
	}
	out.Error = envelope.FatalError(fmt.Sprintf(
		"loop %q exhausted after %d iterations without satisfying: %s",
		loop.Config.Name, loop.Config.Max, loop.Config.Until,
	))
	return out
}
