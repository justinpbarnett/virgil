package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"text/template"
	"time"

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
}

type Runtime struct {
	registry     *pipe.Registry
	observer     Observer
	logger       *slog.Logger
	level        config.LogLevel
	formats      map[string]map[string]*template.Template
	injector     MemoryInjector
	saver        MemorySaver
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

func (r *Runtime) injectMemory(step Step, env envelope.Envelope) envelope.Envelope {
	if r.injector == nil {
		return env
	}
	return r.injector.InjectContext(env, r.memoryConfigs[step.Pipe])
}

func (r *Runtime) autoSave(pipe, signal string, result envelope.Envelope) {
	if r.saver == nil {
		return
	}
	output := envelope.ContentToText(result.Content, result.ContentType)
	go func() {
		if err := r.saver.SaveInvocation(pipe, signal, output); err != nil {
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

// runStep executes a single pipeline step using the sync handler.
func (r *Runtime) runStep(step Step, current envelope.Envelope) envelope.Envelope {
	handler, ok := r.registry.Get(step.Pipe)
	if !ok {
		current.Error = envelope.FatalError("pipe not found: " + step.Pipe)
		r.observer.OnTransition(step.Pipe, current, 0)
		return current
	}

	current = r.injectMemory(step, current)

	r.logEnvelope("step input", current)

	flags := mergeFlags(current.Args, step.Flags)

	stepStart := time.Now()
	result := handler(current, flags)
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

	var lastPipe string
	for _, step := range plan.Steps {
		lastPipe = step.Pipe
		current = r.runStep(step, current)
		if isFatal(current) {
			current.Duration = time.Since(start)
			return current
		}
	}

	current = formatTerminal(current, lastPipe, r.formats)
	current.Duration = time.Since(start)
	r.logger.Info("plan complete", "duration", current.Duration.String())

	r.autoSave(lastPipe, seedSignal, current)

	return current
}

func (r *Runtime) ExecuteStream(ctx context.Context, plan Plan, seed envelope.Envelope, sink func(chunk string)) envelope.Envelope {
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

	for i, step := range plan.Steps {
		// For the last step, try the stream handler
		if i == lastIdx {
			if sh, ok := r.registry.GetStream(step.Pipe); ok {
				current = r.injectMemory(step, current)

				flags := mergeFlags(current.Args, step.Flags)

				stepStart := time.Now()
				result := sh(ctx, current, flags, sink)
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

				r.autoSave(step.Pipe, seedSignal, result)

				return result
			}
		}

		// Regular handler path (shared with Execute)
		current = r.runStep(step, current)
		if isFatal(current) {
			current.Duration = time.Since(start)
			return current
		}
	}

	lastPipe := plan.Steps[lastIdx].Pipe
	current = formatTerminal(current, lastPipe, r.formats)
	current.Duration = time.Since(start)
	r.logger.Info("plan complete", "duration", current.Duration.String())

	r.autoSave(lastPipe, seedSignal, current)

	return current
}
