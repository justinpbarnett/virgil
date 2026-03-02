package runtime

import (
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

func (r *Runtime) Execute(plan Plan, seed envelope.Envelope) envelope.Envelope {
	start := time.Now()
	current := seed

	for _, step := range plan.Steps {
		handler, ok := r.registry.Get(step.Pipe)
		if !ok {
			current.Error = &envelope.EnvelopeError{
				Message:  "pipe not found: " + step.Pipe,
				Severity: "fatal",
			}
			r.observer.OnTransition(step.Pipe, current, time.Since(start))
			return current
		}

		// Merge step flags with any existing args
		flags := make(map[string]string)
		for k, v := range current.Args {
			flags[k] = v
		}
		for k, v := range step.Flags {
			flags[k] = v
		}

		stepStart := time.Now()
		result := handler(current, flags)
		stepDuration := time.Since(stepStart)
		result.Duration = stepDuration

		r.observer.OnTransition(step.Pipe, result, stepDuration)

		if result.Error != nil && result.Error.Severity == "fatal" {
			result.Duration = time.Since(start)
			return result
		}

		current = result
	}

	current.Duration = time.Since(start)
	return current
}
