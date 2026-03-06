package runtime

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
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

// PipelineExecutor runs a PipelineConfig, handling sequential steps and loops.
// It is a separate type from Runtime — it composes Runtime.runStep internally
// and adds loop execution on top.
type PipelineExecutor struct {
	runtime    *Runtime
	config     config.PipelineConfig
	ctx        map[string]any        // context map: "step.field" -> value
	loops      map[string]*LoopState // loop name -> state
	loopRanges map[int]string        // step index -> loop name (first step of each loop)
	stepIndex  map[string]int        // step name -> index in config.Steps
	observer   Observer
	logger     *slog.Logger
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
		observer:   observer,
		logger:     logger,
	}

	// Build step name -> index map.
	for i, s := range cfg.Steps {
		pe.stepIndex[s.Name] = i
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

	return pe, nil
}

// Execute runs the pipeline from the seed envelope and returns the final output.
// Steps execute sequentially. When a step index is the start of a loop, the
// loop executor takes over until the loop exits, then execution resumes at the
// step after the loop's last step.
func (pe *PipelineExecutor) Execute(seed envelope.Envelope) envelope.Envelope {
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
			continue
		}

		// Execute single step.
		step := pe.config.Steps[i]
		current = pe.executeSingleStep(step, current)
		if isFatal(current) {
			return current
		}
		i++
	}

	return current
}

// executeSingleStep runs one pipeline step, evaluating its condition first.
// If the step has no condition or its condition is true, the step is executed.
// If the condition is false, the step is skipped and the current envelope is
// passed through unchanged.
func (pe *PipelineExecutor) executeSingleStep(step config.PipelineStepConfig, current envelope.Envelope) envelope.Envelope {
	if step.Condition != "" {
		cond, err := ParseCondition(step.Condition)
		if err != nil {
			out := envelope.New("pipeline", "step-error")
			out.Error = envelope.FatalError(fmt.Sprintf("step %q: invalid condition: %v", step.Name, err))
			return out
		}
		if !cond.Evaluate(pe.ctx) {
			pe.logger.Debug("step skipped", "step", step.Name, "condition", step.Condition)
			return current
		}
	}

	resolvedArgs := pe.resolveArgs(step.Args)
	runtimeStep := Step{
		Pipe:  step.Pipe,
		Flags: resolvedArgs,
	}

	result := pe.runtime.runStep(runtimeStep, current)
	pe.updateContext(step.Name, result)
	return result
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

			// Evaluate step condition.
			if step.Condition != "" {
				cond, err := ParseCondition(step.Condition)
				if err != nil {
					out := envelope.New("pipeline", "step-error")
					out.Error = envelope.FatalError(fmt.Sprintf("loop %q, step %q: invalid condition: %v", loop.Config.Name, stepName, err))
					return out
				}
				if !cond.Evaluate(pe.ctx) {
					pe.logger.Debug("loop step skipped", "loop", loop.Config.Name, "step", stepName, "iteration", loop.Iteration)
					stepRecord.Skipped = true
					stepRecord.Duration = time.Since(stepStart)
					record.Steps = append(record.Steps, stepRecord)
					continue
				}
			}

			// Resolve template variables in args and run the step.
			resolvedArgs := pe.resolveArgs(step.Args)
			runtimeStep := Step{
				Pipe:  step.Pipe,
				Flags: resolvedArgs,
			}
			result := pe.runtime.runStep(runtimeStep, current)
			stepRecord.Duration = time.Since(stepStart)
			stepRecord.Error = result.Error

			pe.updateContext(stepName, result)

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

// resolveArgs returns a copy of args with {{key}} placeholders replaced by
// values from the context map. Only top-level context keys are supported.
// Missing keys resolve to an empty string.
func (pe *PipelineExecutor) resolveArgs(args map[string]string) map[string]string {
	if len(args) == 0 {
		return nil
	}

	// Build replacer pairs from context map entries.
	pairs := make([]string, 0, len(pe.ctx)*2)
	for k, v := range pe.ctx {
		pairs = append(pairs, "{{"+k+"}}", fmt.Sprintf("%v", v))
	}
	r := strings.NewReplacer(pairs...)

	resolved := make(map[string]string, len(args))
	for k, v := range args {
		resolved[k] = r.Replace(v)
	}
	return resolved
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
