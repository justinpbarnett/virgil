package runtime

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// buildPipelineRuntime creates a Runtime backed by the provided pipe registry with
// a silent logger so tests don't produce log noise.
func buildPipelineRuntime(reg *pipe.Registry) *Runtime {
	return NewWithLevel(reg, &noopObserver{}, nil, config.Silent)
}

// verifyFixPipelineConfig returns a standard verify-fix pipeline config.
func verifyFixPipelineConfig(maxIter int) config.PipelineConfig {
	return config.PipelineConfig{
		Name: "test-pipeline",
		Steps: []config.PipelineStepConfig{
			{
				Name: "verify",
				Pipe: "verify",
				Args: map[string]string{"cwd": "/tmp"},
			},
			{
				Name:      "fix",
				Pipe:      "fix",
				Args:      map[string]string{"attempt": "{{loop.iteration}}"},
				Condition: "verify.error",
			},
		},
		Loops: []config.LoopConfig{
			{
				Name:  "verify-fix",
				Steps: []string{"verify", "fix"},
				Until: "verify.error == null",
				Max:   maxIter,
			},
		},
	}
}

// mockVerify returns a pipe.Handler that fails the first (failCount) calls,
// then passes. It records call count via the provided pointer.
func mockVerify(failCount int, calls *int) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		*calls++
		out := envelope.New("verify", "verify")
		out.ContentType = envelope.ContentStructured
		if *calls <= failCount {
			out.Content = map[string]any{"passed": false, "summary": fmt.Sprintf("call %d: 1 test failure", *calls)}
			out.Error = &envelope.EnvelopeError{
				Message:   fmt.Sprintf("call %d: 1 test failure", *calls),
				Severity:  envelope.SeverityError,
				Retryable: true,
			}
		} else {
			out.Content = map[string]any{"passed": true, "summary": "all checks passed"}
		}
		return out
	}
}

// mockFix returns a pipe.Handler that always succeeds and records call
// count and the "attempt" flag on each call.
func mockFix(calls *int, attempts *[]string) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		*calls++
		if attempts != nil {
			*attempts = append(*attempts, flags["attempt"])
		}
		out := envelope.New("fix", "fix")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"summary": "fixed"}
		return out
	}
}

// mockFixFatal returns a pipe.Handler that always returns a fatal error.
func mockFixFatal(calls *int) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		*calls++
		out := envelope.New("fix", "fix")
		out.Error = envelope.FatalError("provider unreachable")
		return out
	}
}

// TestLoopSucceedsOnRetry verifies the primary verify-fix loop path:
// verify fails once, fix runs, verify passes on the second call, fix is skipped.
func TestLoopSucceedsOnRetry(t *testing.T) {
	var verifyCalls, fixCalls int
	var fixAttempts []string

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "verify"}, mockVerify(1, &verifyCalls))
	reg.Register(pipe.Definition{Name: "fix"}, mockFix(&fixCalls, &fixAttempts))

	rt := buildPipelineRuntime(reg)
	pe, err := NewPipelineExecutor(rt, verifyFixPipelineConfig(5), nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	seed := envelope.New("test", "run")
	result := pe.Execute(seed)

	if result.Error != nil {
		t.Errorf("expected no error, got: %v", result.Error.Message)
	}
	if verifyCalls != 2 {
		t.Errorf("expected verify called 2 times, got %d", verifyCalls)
	}
	if fixCalls != 1 {
		t.Errorf("expected fix called 1 time, got %d", fixCalls)
	}
	// fix should have received attempt "1" on its one invocation.
	if len(fixAttempts) != 1 || fixAttempts[0] != "1" {
		t.Errorf("expected fix attempt=[\"1\"], got %v", fixAttempts)
	}
	// The final loop iteration should be 2.
	if iterVal := pe.ctx["loop.iteration"]; fmt.Sprintf("%v", iterVal) != "2" {
		t.Errorf("expected loop.iteration=2, got %v", iterVal)
	}
}

// TestLoopExhausted verifies that a loop that never satisfies its condition
// returns a fatal failure envelope after max iterations.
func TestLoopExhausted(t *testing.T) {
	var verifyCalls, fixCalls int
	var fixAttempts []string

	reg := pipe.NewRegistry()
	// Verify always fails (failCount = 999 means it never passes within 3 iterations).
	reg.Register(pipe.Definition{Name: "verify"}, mockVerify(999, &verifyCalls))
	reg.Register(pipe.Definition{Name: "fix"}, mockFix(&fixCalls, &fixAttempts))

	rt := buildPipelineRuntime(reg)
	pe, err := NewPipelineExecutor(rt, verifyFixPipelineConfig(3), nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	seed := envelope.New("test", "run")
	result := pe.Execute(seed)

	if result.Error == nil {
		t.Fatal("expected fatal error on exhaustion, got nil")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	matched, _ := regexp.MatchString(`loop.*exhausted.*3 iterations`, result.Error.Message)
	if !matched {
		t.Errorf("expected error message matching 'loop.*exhausted.*3 iterations', got: %q", result.Error.Message)
	}

	if verifyCalls != 3 {
		t.Errorf("expected verify called 3 times, got %d", verifyCalls)
	}
	if fixCalls != 3 {
		t.Errorf("expected fix called 3 times, got %d", fixCalls)
	}

	// Fix should have received incrementing attempt values: "1", "2", "3".
	wantAttempts := []string{"1", "2", "3"}
	if len(fixAttempts) != len(wantAttempts) {
		t.Fatalf("expected fix attempts %v, got %v", wantAttempts, fixAttempts)
	}
	for i, want := range wantAttempts {
		if fixAttempts[i] != want {
			t.Errorf("fix attempt[%d]: want %q, got %q", i, want, fixAttempts[i])
		}
	}

	// Envelope content should include history with 3 iteration records.
	content, ok := result.Content.(map[string]any)
	if !ok {
		t.Fatalf("expected map content, got %T", result.Content)
	}
	history, ok := content["history"].([]LoopIterationRecord)
	if !ok {
		t.Fatalf("expected history []LoopIterationRecord, got %T", content["history"])
	}
	if len(history) != 3 {
		t.Errorf("expected 3 iteration records in history, got %d", len(history))
	}
}

// TestLoopStepConditionSkip verifies that when verify passes on the first call,
// fix is skipped (its condition "verify.error" is false) and the loop exits.
func TestLoopStepConditionSkip(t *testing.T) {
	var verifyCalls, fixCalls int

	reg := pipe.NewRegistry()
	// failCount=0 means verify always passes.
	reg.Register(pipe.Definition{Name: "verify"}, mockVerify(0, &verifyCalls))
	reg.Register(pipe.Definition{Name: "fix"}, mockFix(&fixCalls, nil))

	rt := buildPipelineRuntime(reg)
	pe, err := NewPipelineExecutor(rt, verifyFixPipelineConfig(5), nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	seed := envelope.New("test", "run")
	result := pe.Execute(seed)

	if result.Error != nil {
		t.Errorf("expected no error, got: %v", result.Error.Message)
	}
	if verifyCalls != 1 {
		t.Errorf("expected verify called 1 time, got %d", verifyCalls)
	}
	if fixCalls != 0 {
		t.Errorf("expected fix called 0 times (skipped), got %d", fixCalls)
	}
	// Loop should exit after 1 iteration.
	if iterVal := pe.ctx["loop.iteration"]; fmt.Sprintf("%v", iterVal) != "1" {
		t.Errorf("expected loop.iteration=1, got %v", iterVal)
	}
}

// TestLoopFatalAbort verifies that a fatal error from a step inside a loop
// aborts the loop immediately, propagating the fatal envelope.
func TestLoopFatalAbort(t *testing.T) {
	var verifyCalls, fixCalls int

	reg := pipe.NewRegistry()
	// Verify fails so fix runs; fix then returns a fatal error.
	reg.Register(pipe.Definition{Name: "verify"}, mockVerify(999, &verifyCalls))
	reg.Register(pipe.Definition{Name: "fix"}, mockFixFatal(&fixCalls))

	rt := buildPipelineRuntime(reg)
	pe, err := NewPipelineExecutor(rt, verifyFixPipelineConfig(5), nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	seed := envelope.New("test", "run")
	result := pe.Execute(seed)

	if result.Error == nil {
		t.Fatal("expected fatal error, got nil")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected severity=fatal, got %s", result.Error.Severity)
	}
	if result.Error.Message != "provider unreachable" {
		t.Errorf("expected 'provider unreachable', got %q", result.Error.Message)
	}
	if verifyCalls != 1 {
		t.Errorf("expected verify called 1 time, got %d", verifyCalls)
	}
	if fixCalls != 1 {
		t.Errorf("expected fix called 1 time, got %d", fixCalls)
	}
}

// TestLoopReset verifies that Reset() clears iteration and history.
// This method exists for the future cycle primitive.
func TestLoopReset(t *testing.T) {
	ls := &LoopState{
		Config:    config.LoopConfig{Name: "verify-fix", Max: 5},
		Iteration: 3,
		History: []LoopIterationRecord{
			{Iteration: 1, Satisfied: false},
			{Iteration: 2, Satisfied: false},
			{Iteration: 3, Satisfied: false},
		},
	}

	ls.Reset()

	if ls.Iteration != 0 {
		t.Errorf("expected Iteration=0 after Reset, got %d", ls.Iteration)
	}
	if len(ls.History) != 0 {
		t.Errorf("expected empty History after Reset, got %d records", len(ls.History))
	}
}

// TestNewPipelineExecutorValidation verifies that NewPipelineExecutor rejects
// invalid configurations at construction time.
func TestNewPipelineExecutorValidation(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "verify"}, mockVerify(0, new(int)))
	rt := buildPipelineRuntime(reg)

	t.Run("empty loop steps", func(t *testing.T) {
		cfg := config.PipelineConfig{
			Steps: []config.PipelineStepConfig{{Name: "verify", Pipe: "verify"}},
			Loops: []config.LoopConfig{{Name: "bad", Steps: []string{}, Until: "verify.error == null", Max: 3}},
		}
		_, err := NewPipelineExecutor(rt, cfg, nil, nil)
		if err == nil {
			t.Error("expected error for empty loop steps")
		}
	})

	t.Run("loop references unknown step", func(t *testing.T) {
		cfg := config.PipelineConfig{
			Steps: []config.PipelineStepConfig{{Name: "verify", Pipe: "verify"}},
			Loops: []config.LoopConfig{{Name: "bad", Steps: []string{"no-such-step"}, Until: "verify.error == null", Max: 3}},
		}
		_, err := NewPipelineExecutor(rt, cfg, nil, nil)
		if err == nil {
			t.Error("expected error for unknown step reference")
		}
	})

	t.Run("invalid until condition", func(t *testing.T) {
		cfg := config.PipelineConfig{
			Steps: []config.PipelineStepConfig{{Name: "verify", Pipe: "verify"}},
			Loops: []config.LoopConfig{{Name: "bad", Steps: []string{"verify"}, Until: "a != b", Max: 3}},
		}
		_, err := NewPipelineExecutor(rt, cfg, nil, nil)
		if err == nil {
			t.Error("expected error for unsupported operator in until condition")
		}
	})
}

// TestPipelineExecutorSequentialSteps verifies that the executor runs
// non-looped steps sequentially in order.
func TestPipelineExecutorSequentialSteps(t *testing.T) {
	var order []string

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "step-a"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		order = append(order, "a")
		out := envelope.New("step-a", "run")
		out.Content = "from-a"
		out.ContentType = envelope.ContentText
		return out
	})
	reg.Register(pipe.Definition{Name: "step-b"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		order = append(order, "b")
		out := envelope.New("step-b", "run")
		out.Content = "from-b"
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "seq",
		Steps: []config.PipelineStepConfig{
			{Name: "a", Pipe: "step-a"},
			{Name: "b", Pipe: "step-b"},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	result := pe.Execute(envelope.New("test", "run"))
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error.Message)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("expected steps executed in order [a, b], got %v", order)
	}
}
