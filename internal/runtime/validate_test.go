package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// envelopeObserver captures full envelopes from each transition.
type envelopeObserver struct {
	transitions []envelope.Envelope
}

func (o *envelopeObserver) OnTransition(_ string, env envelope.Envelope, _ time.Duration) {
	o.transitions = append(o.transitions, env)
}

func goodEnvelope(name string) envelope.Envelope {
	out := envelope.New(name, "run")
	out.Content = "result"
	out.ContentType = envelope.ContentText
	return out
}

// badEnvelope returns an envelope that fails validation (list type with string content).
func badEnvelope(name string) envelope.Envelope {
	out := envelope.New(name, "run")
	out.Content = "not a list"
	out.ContentType = envelope.ContentList
	return out
}

func TestValidationHaltsPipelineOnBadEnvelope(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "bad"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		return badEnvelope("bad")
	})
	reg.Register(pipe.Definition{Name: "after"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("after", "run")
		out.Content = "should not reach"
		out.ContentType = envelope.ContentText
		return out
	})

	obs := &envelopeObserver{}
	rt := New(reg, obs, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "bad"},
		{Pipe: "after"},
	}}, envelope.New("input", "test"))

	if result.Error == nil {
		t.Fatal("expected a fatal error, got nil")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "validation:") {
		t.Errorf("expected 'validation:' in error message, got: %s", result.Error.Message)
	}

	// Observer should have been notified for the "bad" pipe only
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 observer transition (pipeline halted), got %d", len(obs.transitions))
	}
}

func TestValidationPassesOnGoodEnvelope(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "good"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		return goodEnvelope("good")
	})

	obs := &envelopeObserver{}
	rt := New(reg, obs, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "good"},
	}}, envelope.New("input", "test"))

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 transition, got %d", len(obs.transitions))
	}
}

func TestValidationErrorAppearsInObserver(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "bad"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		return badEnvelope("bad")
	})

	obs := &envelopeObserver{}
	rt := New(reg, obs, nil)

	rt.Execute(Plan{Steps: []Step{{Pipe: "bad"}}}, envelope.New("input", "test"))

	if len(obs.transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(obs.transitions))
	}
	env := obs.transitions[0]
	if env.Error == nil {
		t.Fatal("expected observer to see error envelope")
	}
	if !strings.Contains(env.Error.Message, "validation:") {
		t.Errorf("expected 'validation:' in observer error message, got: %s", env.Error.Message)
	}
}

func TestValidationStreamTerminalBadEnvelope(t *testing.T) {
	reg := pipe.NewRegistry()
	// Register a sync handler too so the pipe exists in the registry
	reg.Register(pipe.Definition{Name: "stream-bad"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		return goodEnvelope("stream-bad")
	})
	// Register a stream handler that returns a bad envelope
	reg.RegisterStream("stream-bad", func(_ context.Context, _ envelope.Envelope, _ map[string]string, _ func(string)) envelope.Envelope {
		return badEnvelope("stream-bad")
	})

	obs := &envelopeObserver{}
	rt := New(reg, obs, nil)

	result := rt.ExecuteStream(context.Background(), Plan{Steps: []Step{
		{Pipe: "stream-bad"},
	}}, envelope.New("input", "test"), func(_ string) {})

	if result.Error == nil {
		t.Fatal("expected fatal error from stream validation, got nil")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
	if !strings.Contains(result.Error.Message, "validation:") {
		t.Errorf("expected 'validation:' in error message, got: %s", result.Error.Message)
	}

	// Observer should have been notified with the validation error (matches sync path behavior)
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 observer transition, got %d", len(obs.transitions))
	}
	if obs.transitions[0].Error == nil || !strings.Contains(obs.transitions[0].Error.Message, "validation:") {
		t.Errorf("expected observer to see validation error, got: %v", obs.transitions[0].Error)
	}
}

func TestValidationStreamTerminalGoodEnvelope(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "stream-good"}, func(_ envelope.Envelope, _ map[string]string) envelope.Envelope {
		return goodEnvelope("stream-good")
	})
	reg.RegisterStream("stream-good", func(_ context.Context, _ envelope.Envelope, _ map[string]string, sink func(string)) envelope.Envelope {
		sink("streamed chunk")
		return goodEnvelope("stream-good")
	})

	obs := &envelopeObserver{}
	rt := New(reg, obs, nil)

	var chunks []string
	result := rt.ExecuteStream(context.Background(), Plan{Steps: []Step{
		{Pipe: "stream-good"},
	}}, envelope.New("input", "test"), func(c string) {
		chunks = append(chunks, c)
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(chunks) != 1 || chunks[0] != "streamed chunk" {
		t.Errorf("expected 1 chunk 'streamed chunk', got %v", chunks)
	}
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 observer transition, got %d", len(obs.transitions))
	}
}
