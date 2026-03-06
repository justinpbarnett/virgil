package runtime

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
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

// ---- Parallel Step Tests ----

func TestParallelStep_MergesOutputs(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "study"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("study", "run")
		out.Content = "context from " + flags["role"]
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "par-test",
		Steps: []config.PipelineStepConfig{
			{
				Name: "study",
				Parallel: []config.ParallelBranch{
					{Pipe: "study", Args: map[string]string{"role": "builder"}},
					{Pipe: "study", Args: map[string]string{"role": "reviewer"}},
				},
			},
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

	text, ok := result.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", result.Content)
	}
	if !strings.Contains(text, "[Builder perspective]") {
		t.Error("expected builder role header in merged output")
	}
	if !strings.Contains(text, "[Reviewer perspective]") {
		t.Error("expected reviewer role header in merged output")
	}
	if !strings.Contains(text, "context from builder") {
		t.Error("expected builder content in merged output")
	}
	if !strings.Contains(text, "context from reviewer") {
		t.Error("expected reviewer content in merged output")
	}
}

func TestParallelStep_BothBranchesRun(t *testing.T) {
	var mu sync.Mutex
	var ran []string

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "study"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		mu.Lock()
		ran = append(ran, flags["role"])
		mu.Unlock()
		out := envelope.New("study", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "par-both",
		Steps: []config.PipelineStepConfig{
			{
				Name: "study",
				Parallel: []config.ParallelBranch{
					{Pipe: "study", Args: map[string]string{"role": "builder"}},
					{Pipe: "study", Args: map[string]string{"role": "reviewer"}},
				},
			},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	pe.Execute(envelope.New("test", "run"))
	if len(ran) != 2 {
		t.Errorf("expected 2 branches to run, got %d", len(ran))
	}
}

func TestParallelStep_HaltOnFatal(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "study"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		if flags["role"] == "builder" {
			return envelope.NewFatalError("study", "builder crashed")
		}
		out := envelope.New("study", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "par-halt",
		Steps: []config.PipelineStepConfig{
			{
				Name: "study",
				Parallel: []config.ParallelBranch{
					{Pipe: "study", Args: map[string]string{"role": "builder"}},
					{Pipe: "study", Args: map[string]string{"role": "reviewer"}},
				},
				OnBranchFailure: "halt",
			},
			{Name: "next", Pipe: "study", Args: map[string]string{"role": "never"}},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	result := pe.Execute(envelope.New("test", "run"))
	if result.Error == nil {
		t.Fatal("expected fatal error from halted parallel step")
	}
}

// ---- Graph Step Tests ----

func TestGraphStep_ExecutesTaskDAG(t *testing.T) {
	var mu sync.Mutex
	var taskIDs []string

	reg := pipe.NewRegistry()
	reg.RegisterStream("build", func(ctx context.Context, in envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		mu.Lock()
		taskIDs = append(taskIDs, flags["task_id"])
		mu.Unlock()
		out := envelope.New("build", "run")
		out.Content = "built"
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "graph-test",
		Steps: []config.PipelineStepConfig{
			{Name: "decompose", Pipe: "build"}, // dummy to populate context
			{
				Name: "build-tasks",
				Graph: &config.GraphStepConfig{
					Source:        "decompose.tasks",
					Pipe:          "build",
					Args:          map[string]string{"task_id": "{{task.id}}"},
					OnTaskFailure: "halt",
					MaxParallel:   1,
				},
			},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	// Seed the context with a task DAG (simulating decompose output).
	pe.ctx["decompose.tasks"] = []TaskNode{
		{ID: "t1", Name: "task-1", Spec: "do stuff"},
		{ID: "t2", Name: "task-2", Spec: "do more", DependsOn: []string{"t1"}},
	}

	seed := envelope.New("test", "run")
	// Skip decompose step by jumping directly to the graph step.
	step := pe.config.Steps[1]
	result := pe.executeGraph(step, seed)

	if isFatal(result) {
		t.Fatalf("unexpected fatal error: %s", result.Error.Message)
	}

	if len(taskIDs) != 2 {
		t.Errorf("expected 2 tasks executed, got %d", len(taskIDs))
	}
}

func TestGraphStep_MissingSource(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "graph-missing",
		Steps: []config.PipelineStepConfig{
			{
				Name: "build-tasks",
				Graph: &config.GraphStepConfig{
					Source: "nonexistent.tasks",
					Pipe:   "build",
				},
			},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	result := pe.executeSingleStep(pe.config.Steps[0], envelope.New("test", "run"))
	if result.Error == nil {
		t.Fatal("expected fatal error for missing graph source")
	}
	if !strings.Contains(result.Error.Message, "not found in context") {
		t.Errorf("expected 'not found in context' error, got: %s", result.Error.Message)
	}
}

// ---- Cycle Tests ----

func TestCycle_ReviewRework(t *testing.T) {
	var decomposeCalls int
	var lastFindings string

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "decompose"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		decomposeCalls++
		lastFindings = flags["findings"]
		out := envelope.New("decompose", "run")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"tasks": []any{}}
		return out
	})
	reg.Register(pipe.Definition{Name: "publish"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("publish", "run")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"pr_number": 42, "pr_url": "https://example.com/pr/42"}
		return out
	})

	reviewCalls := 0
	reg.Register(pipe.Definition{Name: "review"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		reviewCalls++
		out := envelope.New("review", "run")
		out.ContentType = envelope.ContentStructured
		if reviewCalls <= 1 {
			out.Content = map[string]any{
				"outcome":  "fail",
				"findings": []any{map[string]any{"issue": "missing validation"}},
			}
		} else {
			out.Content = map[string]any{"outcome": "pass"}
		}
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "cycle-test",
		Steps: []config.PipelineStepConfig{
			{Name: "decompose", Pipe: "decompose", Args: map[string]string{"findings": "{{findings}}"}},
			{Name: "publish", Pipe: "publish"},
			{Name: "review", Pipe: "review"},
		},
		Cycles: []config.CycleConfig{
			{
				Name:      "review-rework",
				From:      "review",
				To:        "decompose",
				Condition: `review.outcome == "fail"`,
				Carry:     "findings",
				Max:       3,
			},
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

	// Decompose should have been called twice (initial + rework).
	if decomposeCalls != 2 {
		t.Errorf("expected decompose called 2 times, got %d", decomposeCalls)
	}

	// On the second call, findings should be non-empty (carried from review).
	if lastFindings == "" {
		t.Error("expected findings to be carried to decompose on rework cycle")
	}

	// Review should have been called twice.
	if reviewCalls != 2 {
		t.Errorf("expected review called 2 times, got %d", reviewCalls)
	}
}

func TestCycle_Exhaustion(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "decompose"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("decompose", "run")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"tasks": []any{}}
		return out
	})
	reg.Register(pipe.Definition{Name: "review"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("review", "run")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"outcome": "fail", "findings": []any{}}
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "cycle-exhaust",
		Steps: []config.PipelineStepConfig{
			{Name: "decompose", Pipe: "decompose"},
			{Name: "review", Pipe: "review"},
		},
		Cycles: []config.CycleConfig{
			{
				Name:      "review-rework",
				From:      "review",
				To:        "decompose",
				Condition: `review.outcome == "fail"`,
				Carry:     "findings",
				Max:       2,
			},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	result := pe.Execute(envelope.New("test", "run"))
	// When the cycle exhausts, the pipeline should complete normally (not fatally).
	// The last review output is the final result.
	if result.Error != nil && result.Error.Severity == envelope.SeverityFatal {
		t.Errorf("expected non-fatal completion on cycle exhaustion, got fatal: %s", result.Error.Message)
	}
}

func TestCycle_ResetsLoops(t *testing.T) {
	var verifyCalls int

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "decompose"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("decompose", "run")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"tasks": []any{}}
		return out
	})
	reg.Register(pipe.Definition{Name: "verify"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		verifyCalls++
		out := envelope.New("verify", "verify")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"passed": true}
		return out
	})
	reg.Register(pipe.Definition{Name: "fix"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		out := envelope.New("fix", "fix")
		out.ContentType = envelope.ContentStructured
		out.Content = map[string]any{"summary": "fixed"}
		return out
	})

	reviewCalls := 0
	reg.Register(pipe.Definition{Name: "review"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		reviewCalls++
		out := envelope.New("review", "run")
		out.ContentType = envelope.ContentStructured
		if reviewCalls <= 1 {
			out.Content = map[string]any{"outcome": "fail", "findings": []any{}}
		} else {
			out.Content = map[string]any{"outcome": "pass"}
		}
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "cycle-loop-reset",
		Steps: []config.PipelineStepConfig{
			{Name: "decompose", Pipe: "decompose"},
			{Name: "verify", Pipe: "verify"},
			{Name: "fix", Pipe: "fix", Condition: "verify.error"},
			{Name: "review", Pipe: "review"},
		},
		Loops: []config.LoopConfig{
			{Name: "verify-fix", Steps: []string{"verify", "fix"}, Until: "verify.error == null", Max: 5},
		},
		Cycles: []config.CycleConfig{
			{
				Name:      "review-rework",
				From:      "review",
				To:        "decompose",
				Condition: `review.outcome == "fail"`,
				Carry:     "findings",
				Max:       3,
			},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	pe.Execute(envelope.New("test", "run"))

	// Verify should run once per cycle (passes immediately each time).
	// 2 cycles = 2 verify calls.
	if verifyCalls != 2 {
		t.Errorf("expected verify called 2 times (once per cycle), got %d", verifyCalls)
	}
}

// ---- Template Filter Tests ----

func TestResolveArgs_SimpleSubstitution(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{Name: "test"}
	pe, _ := NewPipelineExecutor(rt, cfg, nil, nil)

	pe.ctx["worktree.path"] = "/tmp/wt"
	pe.ctx["publish.pr_number"] = 42

	result := pe.resolveArgs(map[string]string{
		"cwd": "{{worktree.path}}",
		"pr":  "{{publish.pr_number}}",
	})

	if result["cwd"] != "/tmp/wt" {
		t.Errorf("expected cwd=/tmp/wt, got %q", result["cwd"])
	}
	if result["pr"] != "42" {
		t.Errorf("expected pr=42, got %q", result["pr"])
	}
}

func TestResolveArgs_SlugifyFilter(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{Name: "test"}
	pe, _ := NewPipelineExecutor(rt, cfg, nil, nil)

	pe.ctx["feature"] = "OAuth Login"

	result := pe.resolveArgs(map[string]string{
		"branch": "feat/{{feature | slugify}}",
	})

	if result["branch"] != "feat/oauth-login" {
		t.Errorf("expected branch=feat/oauth-login, got %q", result["branch"])
	}
}

func TestResolveArgs_MissingKey(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{Name: "test"}
	pe, _ := NewPipelineExecutor(rt, cfg, nil, nil)

	result := pe.resolveArgs(map[string]string{
		"findings": "{{findings}}",
	})

	if result["findings"] != "" {
		t.Errorf("expected empty string for missing key, got %q", result["findings"])
	}
}

func TestResolveArgs_ComplexValue(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{Name: "test"}
	pe, _ := NewPipelineExecutor(rt, cfg, nil, nil)

	pe.ctx["findings"] = []map[string]any{{"issue": "bug"}}

	result := pe.resolveArgs(map[string]string{
		"findings": "{{findings}}",
	})

	if !strings.Contains(result["findings"], "bug") {
		t.Errorf("expected JSON-serialized findings, got %q", result["findings"])
	}
}

func TestResolveArgs_UpperLowerFilters(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{Name: "test"}
	pe, _ := NewPipelineExecutor(rt, cfg, nil, nil)

	pe.ctx["name"] = "Hello World"

	result := pe.resolveArgs(map[string]string{
		"upper": "{{name | upper}}",
		"lower": "{{name | lower}}",
	})

	if result["upper"] != "HELLO WORLD" {
		t.Errorf("expected HELLO WORLD, got %q", result["upper"])
	}
	if result["lower"] != "hello world" {
		t.Errorf("expected hello world, got %q", result["lower"])
	}
}

// ---- Context Seeding Tests ----

func TestExecute_SeedsContextFromArgs(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, func(in envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("echo", "run")
		out.Content = flags["text"]
		out.ContentType = envelope.ContentText
		return out
	})

	rt := buildPipelineRuntime(reg)
	cfg := config.PipelineConfig{
		Name: "seed-test",
		Steps: []config.PipelineStepConfig{
			{Name: "echo", Pipe: "echo", Args: map[string]string{"text": "{{signal}}"}},
		},
	}

	pe, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewPipelineExecutor: %v", err)
	}

	seed := envelope.New("test", "run")
	seed.Args = map[string]string{"signal": "hello world"}

	result := pe.Execute(seed)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error.Message)
	}
	if text, ok := result.Content.(string); !ok || text != "hello world" {
		t.Errorf("expected content 'hello world', got %v", result.Content)
	}
}

// ---- Cycle Condition Validation ----

func TestNewPipelineExecutor_InvalidCycleCondition(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "step-a"}, func(in envelope.Envelope, _ map[string]string) envelope.Envelope {
		return envelope.New("step-a", "run")
	})
	rt := buildPipelineRuntime(reg)

	cfg := config.PipelineConfig{
		Steps: []config.PipelineStepConfig{
			{Name: "a", Pipe: "step-a"},
			{Name: "b", Pipe: "step-a"},
		},
		Cycles: []config.CycleConfig{
			{Name: "bad", From: "b", To: "a", Condition: "a != b", Max: 3},
		},
	}

	_, err := NewPipelineExecutor(rt, cfg, nil, nil)
	if err == nil {
		t.Error("expected error for invalid cycle condition")
	}
}

// ---- toTaskNodes Tests ----

func TestToTaskNodes_DirectSlice(t *testing.T) {
	input := []TaskNode{
		{ID: "t1", Name: "Task 1"},
		{ID: "t2", Name: "Task 2", DependsOn: []string{"t1"}},
	}
	nodes, err := toTaskNodes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].ID != "t1" {
		t.Errorf("expected first node ID=t1, got %s", nodes[0].ID)
	}
}

func TestToTaskNodes_MapSlice(t *testing.T) {
	// Simulates what comes from JSON-decoded structured content.
	input := []any{
		map[string]any{"id": "t1", "name": "Task 1", "spec": "spec1", "files": []any{"a.go"}, "depends_on": []any{}},
		map[string]any{"id": "t2", "name": "Task 2", "spec": "spec2", "files": []any{"b.go"}, "depends_on": []any{"t1"}},
	}
	nodes, err := toTaskNodes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[1].DependsOn[0] != "t1" {
		t.Errorf("expected t2 to depend on t1, got %v", nodes[1].DependsOn)
	}
}
