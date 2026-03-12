package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

type Step struct {
	Pipe  string
	Flags map[string]string
}

type Plan struct {
	Steps []Step
	// SkipFirstMemoryInjection instructs Execute/ExecuteStream to skip memory
	// injection for the first step. Used when the caller has already injected
	// memory into the seed envelope (e.g. via PrefetchMemory).
	SkipFirstMemoryInjection bool
	// Complexity classifies the signal: "trivial", "simple", "multi_step", "mission".
	// Set by the AI planner (Layer 4) or inferred heuristically (Layers 1-3).
	Complexity string
}

// StreamEvent is sent by ExecuteStream to report chunks and step transitions.
type StreamEvent struct {
	Type string // "chunk" or "step"
	Data string
}

// emitProgress sends a pipeline_progress SSE event via sink. No-op if sink is nil.
func emitProgress(sink func(StreamEvent), payload map[string]any) {
	if sink == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	sink(StreamEvent{Type: envelope.SSEEventPipelineProgress, Data: string(data)})
}

type Runtime struct {
	registry      *pipe.Registry
	observer      Observer
	logger        *slog.Logger
	level         config.LogLevel
	formats       map[string]map[string]*template.Template
	injector      MemoryInjector
	saver         MemorySaver
	memoryConfigs map[string]config.MemoryConfig
}

func New(registry *pipe.Registry, observer Observer, logger *slog.Logger) *Runtime {
	return NewWithLevel(registry, observer, logger, config.Info)
}

func NewWithLevel(registry *pipe.Registry, observer Observer, logger *slog.Logger, level config.LogLevel) *Runtime {
	if observer == nil {
		observer = &noopObserver{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		registry: registry,
		observer: observer,
		logger:   logger,
		level:    level,
	}
}

func NewWithFormats(registry *pipe.Registry, observer Observer, logger *slog.Logger, level config.LogLevel, rawFormats map[string]map[string]string) (*Runtime, error) {
	rt := NewWithLevel(registry, observer, logger, level)
	if len(rawFormats) > 0 {
		compiled, err := compileFormats(rawFormats)
		if err != nil {
			return nil, fmt.Errorf("compiling format templates: %w", err)
		}
		rt.formats = compiled
	}
	return rt, nil
}

// WithMemory configures memory injection and auto-save on the runtime.
func (r *Runtime) WithMemory(injector MemoryInjector, saver MemorySaver, memConfigs map[string]config.MemoryConfig) {
	r.injector = injector
	r.saver = saver
	r.memoryConfigs = memConfigs
}

// resolveMemory decides how to inject memory for a given step. It handles
// three cases: skip (caller already injected), prefetch channel available,
// or synchronous injection.
func (r *Runtime) resolveMemory(step Step, env envelope.Envelope, stepIdx int, skipFirst bool, prefetchCh <-chan envelope.Envelope) envelope.Envelope {
	switch {
	case stepIdx == 0 && skipFirst:
		return env
	case stepIdx == 0 && prefetchCh != nil:
		return <-prefetchCh
	default:
		return r.injectMemory(step, env)
	}
}

func (r *Runtime) injectMemory(step Step, env envelope.Envelope) envelope.Envelope {
	if r.injector == nil {
		return env
	}
	return r.injector.InjectContext(env, r.memoryConfigs[step.Pipe])
}

// injectMemoryAsync starts memory injection in a goroutine and returns a
// channel. If no injector is configured the channel immediately yields env.
func (r *Runtime) injectMemoryAsync(step Step, env envelope.Envelope) <-chan envelope.Envelope {
	ch := make(chan envelope.Envelope, 1)
	if r.injector == nil {
		ch <- env
		return ch
	}
	go func() { ch <- r.injector.InjectContext(env, r.memoryConfigs[step.Pipe]) }()
	return ch
}

// PrefetchMemory starts memory injection for the given pipe asynchronously and
// returns a channel that receives the injected envelope. Returns immediately;
// the caller can await the result just before executing the pipe. If no
// injector is configured the channel immediately contains the original env.
func (r *Runtime) PrefetchMemory(seed envelope.Envelope, pipeName string) <-chan envelope.Envelope {
	return r.injectMemoryAsync(Step{Pipe: pipeName}, seed)
}

func (r *Runtime) autoSave(pipe, signal string, result envelope.Envelope, contextIDs []string) {
	if r.saver == nil {
		return
	}
	output := envelope.ContentToText(result.Content, result.ContentType)
	go func() {
		if err := r.saver.SaveInvocation(pipe, signal, output, contextIDs); err != nil {
			r.logger.Warn("auto-save failed", "error", err)
		}
	}()
}

func (r *Runtime) logEnvelope(label string, env envelope.Envelope) {
	if r.level < config.Verbose {
		return
	}
	logEnvelopeJSON(r.logger, label, env)
}

// mergeFlags combines envelope args with step flags, with step flags taking precedence.
func mergeFlags(args, stepFlags map[string]string) map[string]string {
	flags := maps.Clone(args)
	if flags == nil {
		flags = make(map[string]string, len(stepFlags))
	}
	maps.Copy(flags, stepFlags)
	return flags
}

// isFatal reports whether an envelope carries a fatal error.
func isFatal(env envelope.Envelope) bool {
	return env.Error != nil && env.Error.Severity == envelope.SeverityFatal
}

// extractContextIDs collects IDs from store-backed memory entries.
// Codebase entries are excluded because their IDs are file paths, not memory row IDs.
func extractContextIDs(entries []envelope.MemoryEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.ID != "" && e.Type != "codebase" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// runStep executes a single pipeline step using the already-injected envelope.
func (r *Runtime) runStep(step Step, env envelope.Envelope) envelope.Envelope {
	handler, ok := r.registry.Get(step.Pipe)
	if !ok {
		env.Error = envelope.FatalError("pipe not found: " + step.Pipe)
		r.observer.OnTransition(step.Pipe, env, 0)
		return env
	}

	r.logEnvelope("step input", env)

	flags := mergeFlags(env.Args, step.Flags)

	stepStart := time.Now()
	result := handler(env, flags)
	stepDuration := time.Since(stepStart)
	result.Duration = stepDuration

	if err := envelope.Validate(result); err != nil {
		r.logger.Error("envelope validation failed", "pipe", step.Pipe, "error", err)
		result.Error = envelope.FatalError("validation: " + err.Error())
	}

	r.observer.OnTransition(step.Pipe, result, stepDuration)
	return result
}

func (r *Runtime) Execute(plan Plan, seed envelope.Envelope) envelope.Envelope {
	start := time.Now()
	current := seed
	seedSignal := envelope.ContentToText(seed.Content, seed.ContentType)

	if len(plan.Steps) == 0 {
		current.Duration = time.Since(start)
		return current
	}

	r.logger.Info("plan started", "steps", len(plan.Steps))
	r.logEnvelope("seed envelope", seed)

	// For single-step plans, prefetch memory asynchronously so it runs
	// concurrently with any serial overhead instead of blocking the step.
	var firstMemCh <-chan envelope.Envelope
	if len(plan.Steps) == 1 && !plan.SkipFirstMemoryInjection {
		firstMemCh = r.injectMemoryAsync(plan.Steps[0], current)
	}

	var lastPipe string
	var lastContextIDs []string
	for i, step := range plan.Steps {
		lastPipe = step.Pipe

		injected := r.resolveMemory(step, current, i, plan.SkipFirstMemoryInjection, firstMemCh)

		if i == len(plan.Steps)-1 {
			lastContextIDs = extractContextIDs(injected.Memory)
		}
		current = r.runStep(step, injected)
		if isFatal(current) {
			current.Duration = time.Since(start)
			return current
		}
	}

	current = formatTerminal(current, lastPipe, r.formats)
	current.Duration = time.Since(start)
	r.logger.Info("plan complete", "duration", current.Duration.String())

	r.autoSave(lastPipe, seedSignal, current, lastContextIDs)

	return current
}

func (r *Runtime) ExecuteStream(ctx context.Context, plan Plan, seed envelope.Envelope, sink func(StreamEvent)) envelope.Envelope {
	start := time.Now()
	current := seed
	seedSignal := envelope.ContentToText(seed.Content, seed.ContentType)

	if len(plan.Steps) == 0 {
		current.Duration = time.Since(start)
		return current
	}

	lastIdx := len(plan.Steps) - 1

	r.logger.Info("plan started", "steps", len(plan.Steps), "streaming", true)
	r.logEnvelope("seed envelope", seed)

	chunkSink := func(chunk string) {
		if strings.HasPrefix(chunk, bridge.ToolChunkPrefix) {
			payload := strings.TrimPrefix(chunk, bridge.ToolChunkPrefix)
			name, summary, _ := strings.Cut(payload, "\t")
			data, _ := json.Marshal(map[string]string{"name": name, "summary": summary})
			sink(StreamEvent{Type: envelope.SSEEventTool, Data: string(data)})
			return
		}
		sink(StreamEvent{Type: envelope.SSEEventChunk, Data: chunk})
	}

	// For single-step plans, prefetch memory asynchronously so it runs
	// concurrently with any setup work. Multi-step plans inject memory
	// synchronously at each step to avoid using a stale seed.
	var firstMemCh <-chan envelope.Envelope
	if lastIdx == 0 && !plan.SkipFirstMemoryInjection {
		firstMemCh = r.injectMemoryAsync(plan.Steps[0], current)
	}

	for i, step := range plan.Steps {
		// For the last step, try the stream handler
		if i == lastIdx {
			injected := r.resolveMemory(step, current, i, plan.SkipFirstMemoryInjection, firstMemCh)
			contextIDs := extractContextIDs(injected.Memory)

			if sh, ok := r.registry.GetStream(step.Pipe); ok {
				flags := mergeFlags(injected.Args, step.Flags)

				stepStart := time.Now()
				result := sh(ctx, injected, flags, chunkSink)
				stepDuration := time.Since(stepStart)

				if err := envelope.Validate(result); err != nil {
					r.logger.Error("envelope validation failed", "pipe", step.Pipe, "error", err)
					result.Error = envelope.FatalError("validation: " + err.Error())
				}

				r.observer.OnTransition(step.Pipe, result, stepDuration)

				if isFatal(result) {
					result.Duration = time.Since(start)
					return result
				}

				result = formatTerminal(result, step.Pipe, r.formats)
				result.Duration = time.Since(start)
				r.logger.Info("plan complete", "duration", result.Duration.String())

				r.autoSave(step.Pipe, seedSignal, result, contextIDs)

				return result
			}

			// No stream handler — fall through to regular handler
			current = r.runStep(step, injected)
			if isFatal(current) {
				current.Duration = time.Since(start)
				return current
			}

			current = formatTerminal(current, step.Pipe, r.formats)
			current.Duration = time.Since(start)
			r.logger.Info("plan complete", "duration", current.Duration.String())

			r.autoSave(step.Pipe, seedSignal, current, contextIDs)
			return current
		}

		// Non-terminal step
		stepStart := time.Now()
		injected := r.injectMemory(step, current)
		current = r.runStep(step, injected)
		stepDuration := time.Since(stepStart)
		if isFatal(current) {
			current.Duration = time.Since(start)
			return current
		}

		stepData, _ := json.Marshal(map[string]string{"pipe": step.Pipe, "duration": stepDuration.String()})
		sink(StreamEvent{
			Type: envelope.SSEEventStep,
			Data: string(stepData),
		})
	}

	// Unreachable with non-empty plan, but handle defensively
	lastPipe := plan.Steps[lastIdx].Pipe
	current = formatTerminal(current, lastPipe, r.formats)
	current.Duration = time.Since(start)
	r.logger.Info("plan complete", "duration", current.Duration.String())

	r.autoSave(lastPipe, seedSignal, current, nil)

	return current
}
