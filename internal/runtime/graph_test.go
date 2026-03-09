package runtime

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// mockObserver records OnTransition calls for verification.
type mockObserver struct {
	mu    sync.Mutex
	calls []observerCall
}

type observerCall struct {
	pipe     string
	env      envelope.Envelope
	duration time.Duration
}

func (o *mockObserver) OnTransition(p string, env envelope.Envelope, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, observerCall{pipe: p, env: env, duration: d})
}

func (o *mockObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.calls)
}

// passHandler returns a StreamHandler that always succeeds.
func passHandler() pipe.StreamHandler {
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	}
}

// failHandler returns a StreamHandler that always returns a fatal error.
func failHandler() pipe.StreamHandler {
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		return envelope.NewFatalError("test-pipe", "task error")
	}
}

// newRegistry creates a registry with a single StreamHandler registered under
// the given pipe name.
func newRegistry(pipeName string, handler pipe.StreamHandler) *pipe.Registry {
	reg := pipe.NewRegistry()
	reg.RegisterStream(pipeName, handler)
	return reg
}

func seedEnvelope() envelope.Envelope {
	return envelope.New("input", "test")
}

// ---- DAG Validation ----

func TestValidateDAG_DuplicateID(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1", Name: "Task 1"},
		{ID: "t1", Name: "Task 1 dup"},
	}
	err := validateDAG(tasks)
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
	if !strings.Contains(err.Error(), "t1") {
		t.Errorf("expected error to mention duplicate ID 't1', got: %s", err)
	}
}

func TestValidateDAG_UnknownDependency(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1", Name: "Task 1", DependsOn: []string{"ghost"}},
	}
	err := validateDAG(tasks)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected error to mention unknown dep 'ghost', got: %s", err)
	}
}

func TestValidateDAG_SelfDependency(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1", Name: "Task 1", DependsOn: []string{"t1"}},
	}
	err := validateDAG(tasks)
	if err == nil {
		t.Fatal("expected error for self-dependency, got nil")
	}
	if !strings.Contains(err.Error(), "t1") {
		t.Errorf("expected error to mention task ID 't1', got: %s", err)
	}
}

func TestValidateDAG_EmptyList(t *testing.T) {
	if err := validateDAG(nil); err != nil {
		t.Errorf("expected nil error for empty task list, got: %s", err)
	}
}

// ---- Topological Sort ----

func TestTopoSort_Linear(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t2"}},
	}
	levels, err := topoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(levels))
	}
	for i, level := range levels {
		if len(level) != 1 {
			t.Errorf("level %d: expected 1 task, got %d", i, len(level))
		}
	}
	if levels[0][0].ID != "t1" || levels[1][0].ID != "t2" || levels[2][0].ID != "t3" {
		t.Errorf("unexpected level ordering: %v", levels)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t1"}},
		{ID: "t4", DependsOn: []string{"t2", "t3"}},
	}
	levels, err := topoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d: %v", len(levels), levels)
	}
	if len(levels[0]) != 1 || levels[0][0].ID != "t1" {
		t.Errorf("level 0 should be [t1], got %v", levels[0])
	}
	if len(levels[1]) != 2 {
		t.Errorf("level 1 should have 2 tasks (t2, t3), got %v", levels[1])
	}
	if len(levels[2]) != 1 || levels[2][0].ID != "t4" {
		t.Errorf("level 2 should be [t4], got %v", levels[2])
	}
}

func TestTopoSort_Wide(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2"},
		{ID: "t3"},
		{ID: "t4"},
	}
	levels, err := topoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("expected 1 level, got %d", len(levels))
	}
	if len(levels[0]) != 4 {
		t.Errorf("expected 4 tasks in level 0, got %d", len(levels[0]))
	}
}

func TestTopoSort_CycleDetection(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1", DependsOn: []string{"t3"}},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t2"}},
	}
	_, err := topoSort(tasks)
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Errorf("expected error to mention 'cycle', got: %s", err)
	}
}

func TestTopoSort_MultipleRoots(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2"},
		{ID: "t3", DependsOn: []string{"t1"}},
		{ID: "t4", DependsOn: []string{"t2"}},
	}
	levels, err := topoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(levels))
	}
	if len(levels[0]) != 2 {
		t.Errorf("level 0 should have 2 tasks, got %d", len(levels[0]))
	}
	if len(levels[1]) != 2 {
		t.Errorf("level 1 should have 2 tasks, got %d", len(levels[1]))
	}
}

// ---- File Conflict Detection ----

func TestFileConflict_SameLevel(t *testing.T) {
	levels := [][]TaskNode{
		{
			{ID: "t1", Files: []string{"internal/foo.go"}},
			{ID: "t2", Files: []string{"internal/foo.go"}},
		},
	}
	err := validateFileConflicts(levels)
	if err == nil {
		t.Fatal("expected file conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "internal/foo.go") {
		t.Errorf("expected error to mention conflicting file, got: %s", err)
	}
	if !strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), "t2") {
		t.Errorf("expected error to mention both task IDs, got: %s", err)
	}
}

func TestFileConflict_DifferentLevels(t *testing.T) {
	levels := [][]TaskNode{
		{{ID: "t1", Files: []string{"internal/foo.go"}}},
		{{ID: "t2", Files: []string{"internal/foo.go"}}},
	}
	if err := validateFileConflicts(levels); err != nil {
		t.Errorf("expected no conflict for different levels, got: %s", err)
	}
}

func TestFileConflict_NoFiles(t *testing.T) {
	levels := [][]TaskNode{
		{
			{ID: "t1", Files: nil},
			{ID: "t2", Files: []string{}},
		},
	}
	if err := validateFileConflicts(levels); err != nil {
		t.Errorf("expected no conflict for tasks with no files, got: %s", err)
	}
}

// ---- Execution: Halt Mode ----

func TestExecute_HaltMode_AllPass(t *testing.T) {
	// Diamond: t1 -> t2, t1 -> t3, t2 -> t4, t3 -> t4
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t1"}},
		{ID: "t4", DependsOn: []string{"t2", "t3"}},
	}
	reg := newRegistry("test-pipe", passHandler())
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 4}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("expected no fatal error, got: %s", out.Error.Message)
	}
	gout, ok := out.Content.(GraphOutput)
	if !ok {
		t.Fatalf("expected GraphOutput content, got %T", out.Content)
	}
	if gout.TasksCompleted != 4 {
		t.Errorf("expected 4 completed, got %d", gout.TasksCompleted)
	}
	if gout.TasksFailed != 0 {
		t.Errorf("expected 0 failed, got %d", gout.TasksFailed)
	}
	if gout.Levels != 3 {
		t.Errorf("expected 3 levels, got %d", gout.Levels)
	}
}

func TestExecute_HaltMode_TaskFails(t *testing.T) {
	// Level 0: t1 (fail), t2 (pass) — level 1: t3 (should be skipped)
	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		if flags["task_id"] == "t1" {
			return envelope.NewFatalError("test-pipe", "task error")
		}
		// t2 sleeps briefly to allow t1's failure to cancel the level
		time.Sleep(5 * time.Millisecond)
		if ctx.Err() != nil {
			return envelope.NewFatalError("test-pipe", "cancelled")
		}
		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2"},
		{ID: "t3", DependsOn: []string{"t1", "t2"}},
	}
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{
		Pipe:          "test-pipe",
		OnTaskFailure: "halt",
		MaxParallel:   4,
		Args:          map[string]string{"task_id": "{{task.id}}"},
	}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if !isFatal(out) {
		t.Fatal("expected fatal error envelope in halt mode, got none")
	}
	gout, ok := out.Content.(GraphOutput)
	if !ok {
		t.Fatalf("expected GraphOutput content, got %T", out.Content)
	}
	if gout.TasksFailed == 0 {
		t.Error("expected at least 1 failed task")
	}
	// t3 must not have passed
	for _, r := range gout.Results {
		if r.ID == "t3" && r.Status == "pass" {
			t.Error("t3 should not have passed after t1 failed in halt mode")
		}
	}
}

func TestExecute_HaltMode_Level0Fails(t *testing.T) {
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t2"}},
	}
	reg := newRegistry("test-pipe", failHandler())
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 1}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if !isFatal(out) {
		t.Fatal("expected fatal error in halt mode after level 0 failure")
	}
	gout := out.Content.(GraphOutput)
	if gout.TasksFailed != 1 {
		t.Errorf("expected 1 failed task, got %d", gout.TasksFailed)
	}
	// t2 and t3 should not appear as passed.
	for _, r := range gout.Results {
		if (r.ID == "t2" || r.ID == "t3") && r.Status == "pass" {
			t.Errorf("task %s should not have passed after halt", r.ID)
		}
	}
}

// ---- Execution: Continue-Independent Mode ----

func TestExecute_Continue_SkipsDependents(t *testing.T) {
	// Diamond: t1 passes, t2 fails, t3 passes, t4 depends on t2 and t3 -> skipped
	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		if flags["task_id"] == "t2" {
			return envelope.NewFatalError("test-pipe", "t2 error")
		}
		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t1"}},
		{ID: "t4", DependsOn: []string{"t2", "t3"}},
	}
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{
		Pipe:          "test-pipe",
		OnTaskFailure: "continue-independent",
		MaxParallel:   4,
		Args:          map[string]string{"task_id": "{{task.id}}"},
	}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	// continue-independent: non-fatal envelope even on failure
	gout, ok := out.Content.(GraphOutput)
	if !ok {
		t.Fatalf("expected GraphOutput, got %T", out.Content)
	}
	if gout.TasksCompleted != 2 { // t1 and t3
		t.Errorf("expected 2 completed (t1, t3), got %d", gout.TasksCompleted)
	}
	if gout.TasksFailed != 1 { // t2
		t.Errorf("expected 1 failed (t2), got %d", gout.TasksFailed)
	}

	statusFor := func(id string) string {
		for _, r := range gout.Results {
			if r.ID == id {
				return r.Status
			}
		}
		return "not-found"
	}
	if s := statusFor("t4"); s != "skipped" {
		t.Errorf("expected t4 skipped, got %s", s)
	}
}

func TestExecute_Continue_IndependentBranchContinues(t *testing.T) {
	// t1 fails, t2 independent (no dep on t1) should still pass
	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		if flags["task_id"] == "t1" {
			return envelope.NewFatalError("test-pipe", "t1 error")
		}
		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2"},
	}
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{
		Pipe:          "test-pipe",
		OnTaskFailure: "continue-independent",
		MaxParallel:   4,
		Args:          map[string]string{"task_id": "{{task.id}}"},
	}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	// No fatal error in continue-independent mode
	if isFatal(out) {
		t.Errorf("expected non-fatal envelope in continue-independent mode, got: %s", out.Error.Message)
	}
	gout := out.Content.(GraphOutput)
	if gout.TasksCompleted != 1 {
		t.Errorf("expected 1 completed (t2), got %d", gout.TasksCompleted)
	}
	if gout.TasksFailed != 1 {
		t.Errorf("expected 1 failed (t1), got %d", gout.TasksFailed)
	}
}

func TestExecute_Continue_TransitiveSkip(t *testing.T) {
	// t1 -> t2 -> t3, t1 fails; t2 and t3 should both be skipped
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t2"}},
	}
	reg := newRegistry("test-pipe", failHandler())
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{
		Pipe:          "test-pipe",
		OnTaskFailure: "continue-independent",
		MaxParallel:   1,
	}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	gout := out.Content.(GraphOutput)
	statusFor := func(id string) string {
		for _, r := range gout.Results {
			if r.ID == id {
				return r.Status
			}
		}
		return "not-found"
	}
	if s := statusFor("t2"); s != "skipped" {
		t.Errorf("expected t2 skipped, got %s", s)
	}
	if s := statusFor("t3"); s != "skipped" {
		t.Errorf("expected t3 skipped, got %s", s)
	}
}

// ---- Concurrency Control ----

func TestExecute_MaxParallel(t *testing.T) {
	var current, maxSeen atomic.Int64

	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		n := current.Add(1)
		if n > maxSeen.Load() {
			maxSeen.Store(n)
		}
		time.Sleep(10 * time.Millisecond)
		current.Add(-1)

		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{
		{ID: "t1"}, {ID: "t2"}, {ID: "t3"},
		{ID: "t4"}, {ID: "t5"}, {ID: "t6"},
	}
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 2}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if max := maxSeen.Load(); max > 2 {
		t.Errorf("max concurrent tasks exceeded limit of 2, got %d", max)
	}
}

func TestExecute_DefaultMaxParallel(t *testing.T) {
	var current, maxSeen atomic.Int64

	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		n := current.Add(1)
		if n > maxSeen.Load() {
			maxSeen.Store(n)
		}
		time.Sleep(10 * time.Millisecond)
		current.Add(-1)

		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{
		{ID: "t1"}, {ID: "t2"}, {ID: "t3"},
		{ID: "t4"}, {ID: "t5"}, {ID: "t6"},
	}
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 0} // 0 = use default (4)

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if max := maxSeen.Load(); max > int64(defaultMaxParallel) {
		t.Errorf("max concurrent tasks exceeded default limit of %d, got %d", defaultMaxParallel, max)
	}
}

// ---- Task Variable Resolution ----

func TestResolveTaskArgs(t *testing.T) {
	task := TaskNode{
		ID:    "task-1",
		Name:  "My Task",
		Spec:  "do something",
		Files: []string{"foo.go", "bar.go"},
	}
	args := map[string]string{
		"id":    "{{task.id}}",
		"name":  "{{task.name}}",
		"spec":  "{{task.spec}}",
		"files": "{{task.files}}",
	}
	resolved := resolveTaskArgs(args, task)

	if resolved["id"] != "task-1" {
		t.Errorf("expected id=task-1, got %q", resolved["id"])
	}
	if resolved["name"] != "My Task" {
		t.Errorf("expected name='My Task', got %q", resolved["name"])
	}
	if resolved["spec"] != "do something" {
		t.Errorf("expected spec='do something', got %q", resolved["spec"])
	}
	if resolved["files"] != "foo.go,bar.go" {
		t.Errorf("expected files='foo.go,bar.go', got %q", resolved["files"])
	}
}

func TestResolveTaskArgs_NoPlaceholders(t *testing.T) {
	task := TaskNode{ID: "t1", Name: "Task"}
	args := map[string]string{
		"key": "static-value",
		"foo": "bar",
	}
	resolved := resolveTaskArgs(args, task)
	if resolved["key"] != "static-value" {
		t.Errorf("expected key unchanged, got %q", resolved["key"])
	}
	if resolved["foo"] != "bar" {
		t.Errorf("expected foo unchanged, got %q", resolved["foo"])
	}
}

func TestResolveTaskArgs_SpecWithBraces(t *testing.T) {
	// Task spec contains literal {{ }} (e.g., Go template code). Should not error.
	task := TaskNode{
		ID:   "t1",
		Name: "Template Task",
		Spec: "use {{ .Value }} in template",
	}
	args := map[string]string{
		"spec": "{{task.spec}}",
	}
	resolved := resolveTaskArgs(args, task)
	if resolved["spec"] != "use {{ .Value }} in template" {
		t.Errorf("expected literal braces to pass through, got %q", resolved["spec"])
	}
}

// ---- Context Cancellation ----

func TestExecute_ParentContextCancelled(t *testing.T) {
	started := make(chan struct{})
	reg := pipe.NewRegistry()
	reg.RegisterStream("test-pipe", func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(string)) envelope.Envelope {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return envelope.NewFatalError("test-pipe", "cancelled")
		case <-time.After(5 * time.Second):
		}
		out := envelope.New("test-pipe", "run")
		out.Content = "ok"
		out.ContentType = envelope.ContentText
		return out
	})

	tasks := []TaskNode{{ID: "t1"}}
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 1}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan envelope.Envelope, 1)
	go func() {
		done <- g.Execute(ctx, cfg, tasks, seedEnvelope())
	}()

	// Wait for task to start, then cancel.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start in time")
	}
	cancel()

	select {
	case out := <-done:
		// We expect either a fatal envelope or partial results.
		_ = out // cancellation may produce a fatal or halt error
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return after context cancellation")
	}
}

// ---- Observability ----

func TestExecute_ObserverCalledPerTask(t *testing.T) {
	// 3-task diamond: t1 (level 0), t2+t3 (level 1, both dep on t1) — 3 tasks total
	tasks := []TaskNode{
		{ID: "t1"},
		{ID: "t2", DependsOn: []string{"t1"}},
		{ID: "t3", DependsOn: []string{"t1"}},
	}
	reg := newRegistry("test-pipe", passHandler())
	obs := &mockObserver{}
	g := NewGraphExecutor(reg, obs, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 4}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	if count := obs.count(); count != 3 {
		t.Errorf("expected observer called 3 times (once per task), got %d", count)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	for _, call := range obs.calls {
		if call.pipe != "test-pipe" {
			t.Errorf("expected pipe name 'test-pipe', got %q", call.pipe)
		}
		if call.duration <= 0 {
			t.Errorf("expected non-zero duration for task, got %v", call.duration)
		}
	}
}

// ---- Edge Cases ----

func TestExecute_SingleTask(t *testing.T) {
	tasks := []TaskNode{{ID: "t1", Name: "Only Task"}}
	reg := newRegistry("test-pipe", passHandler())
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 1}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("unexpected error: %s", out.Error.Message)
	}
	gout := out.Content.(GraphOutput)
	if gout.TasksCompleted != 1 {
		t.Errorf("expected 1 completed, got %d", gout.TasksCompleted)
	}
	if gout.Levels != 1 {
		t.Errorf("expected 1 level, got %d", gout.Levels)
	}
}

func TestExecute_EmptyTaskList(t *testing.T) {
	reg := newRegistry("test-pipe", passHandler())
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "test-pipe", OnTaskFailure: "halt", MaxParallel: 1}

	out := g.Execute(context.Background(), cfg, nil, seedEnvelope())

	if isFatal(out) {
		t.Fatalf("unexpected error for empty task list: %s", out.Error.Message)
	}
	gout := out.Content.(GraphOutput)
	if gout.TasksCompleted != 0 {
		t.Errorf("expected 0 completed, got %d", gout.TasksCompleted)
	}
	if gout.Levels != 0 {
		t.Errorf("expected 0 levels, got %d", gout.Levels)
	}
}

func TestExecute_PipeNotFound(t *testing.T) {
	tasks := []TaskNode{{ID: "t1", Name: "Task"}}
	reg := pipe.NewRegistry() // empty — pipe not registered
	g := NewGraphExecutor(reg, nil, nil)
	cfg := GraphConfig{Pipe: "nonexistent", OnTaskFailure: "halt", MaxParallel: 1}

	out := g.Execute(context.Background(), cfg, tasks, seedEnvelope())

	gout := out.Content.(GraphOutput)
	if gout.TasksFailed != 1 {
		t.Errorf("expected 1 failed task (pipe not found), got %d", gout.TasksFailed)
	}
}
