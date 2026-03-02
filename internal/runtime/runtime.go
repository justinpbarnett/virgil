package runtime

import (
	"context"
	"maps"
	"time"

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
	registry *pipe.Registry
	observer Observer
}

func New(registry *pipe.Registry, observer Observer) *Runtime {
	if observer == nil {
		observer = &noopObserver{}
	}
	return &Runtime{
		registry: registry,
		observer: observer,
	}
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

	flags := mergeFlags(current.Args, step.Flags)

	stepStart := time.Now()
	result := handler(current, flags)
	stepDuration := time.Since(stepStart)
	result.Duration = stepDuration

	r.observer.OnTransition(step.Pipe, result, stepDuration)
	return result
}

func (r *Runtime) Execute(plan Plan, seed envelope.Envelope) envelope.Envelope {
	start := time.Now()
	current := seed

	for _, step := range plan.Steps {
		current = r.runStep(step, current)
		if isFatal(current) {
			current.Duration = time.Since(start)
			return current
		}
	}

	current.Duration = time.Since(start)
	return current
}

func (r *Runtime) ExecuteStream(ctx context.Context, plan Plan, seed envelope.Envelope, sink func(chunk string)) envelope.Envelope {
	start := time.Now()
	current := seed
	lastIdx := len(plan.Steps) - 1

	for i, step := range plan.Steps {
		// For the last step, try the stream handler
		if i == lastIdx {
			if sh, ok := r.registry.GetStream(step.Pipe); ok {
				flags := mergeFlags(current.Args, step.Flags)

				stepStart := time.Now()
				result := sh(ctx, current, flags, sink)
				stepDuration := time.Since(stepStart)

				r.observer.OnTransition(step.Pipe, result, stepDuration)
				result.Duration = time.Since(start)
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

	current.Duration = time.Since(start)
	return current
}
