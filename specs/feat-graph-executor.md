# feat: graph executor

## Summary

The `GraphExecutor` is the engine that runs a pipeline's `graph:` step. It takes a DAG of tasks produced by the `decompose` pipe, topologically sorts them into dependency levels, and dispatches tasks at each level concurrently as separate pipe subprocesses. It enforces a concurrency cap, detects file conflicts, handles task failures according to a configurable policy, and collects results into a structured output envelope.

This spec covers only the graph executor itself (`internal/runtime/graph.go` and `internal/runtime/graph_test.go`). It does not cover the pipeline executor, decompose pipe, template resolver, or any other pipeline infrastructure -- those are separate units of work defined in `specs/pipeline-build.md`.

---

## Integration Point

The graph executor is invoked by the pipeline executor (not yet built; see `specs/pipeline-build.md` section "PipelineExecutor") when a step has a `graph:` config block. The pipeline executor is responsible for:

1. Extracting the `[]TaskNode` from the context map using `graph.source`
2. Resolving `{{worktree.path}}` and other non-task template variables in `graph.args`
3. Calling `GraphExecutor.Execute()` with the resolved config and task list
4. Storing the graph output in the context map under the step name

The graph executor is responsible for:

1. Validating the DAG (no cycles, no unknown dependencies, no file conflicts per level)
2. Topological sorting into levels
3. Running tasks level-by-level with concurrency control via `errgroup`
4. Dispatching each task through the pipe's `StreamHandler` (for context cancellation support)
5. Resolving `{{task.*}}` variables per-task
6. Calling `observer.OnTransition` after each task completes
7. Collecting results and producing the output envelope

---

## Data Structures

All structs live in `internal/runtime/graph.go`.

### TaskNode

Input: a single node in the dependency graph, deserialized from the decompose pipe's structured output.

```go
// TaskNode represents a single task in the dependency graph produced by the
// decompose pipe. The graph executor receives a slice of these and sorts them
// into dependency levels for parallel execution.
type TaskNode struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Spec      string   `json:"spec"`
    Files     []string `json:"files"`
    DependsOn []string `json:"depends_on"`
}
```

### TaskResult

Output: the outcome of executing a single task.

```go
// TaskResult records the outcome of a single task execution within the graph.
type TaskResult struct {
    ID       string        `json:"id"`
    Name     string        `json:"name"`
    Status   string        `json:"status"` // "pass", "fail", "skipped", or "cancelled"
    Duration time.Duration `json:"duration"`
    Error    string        `json:"error,omitempty"`
}
```

### GraphOutput

The structured content of the graph step's output envelope.

```go
// GraphOutput is the structured content placed into the output envelope
// after graph execution completes.
type GraphOutput struct {
    TasksCompleted int          `json:"tasks_completed"`
    TasksFailed    int          `json:"tasks_failed"`
    Levels         int          `json:"levels"`
    Results        []TaskResult `json:"results"`
}
```

### GraphConfig

Already defined in `internal/config/config.go` as part of the pipeline config types (see `specs/pipeline-build.md`). Reproduced here for reference -- the graph executor receives this as input:

```go
type GraphConfig struct {
    Source        string            `yaml:"source"`
    Pipe          string            `yaml:"pipe"`
    Args          map[string]string `yaml:"args"`
    OnTaskFailure string            `yaml:"on_task_failure"` // "halt" or "continue-independent"
    MaxParallel   int               `yaml:"max_parallel"`    // 0 = default (4)
}
```

### GraphExecutor

```go
// GraphExecutor runs a DAG of tasks through a single pipe, dispatching
// independent tasks concurrently and respecting dependency ordering.
type GraphExecutor struct {
    registry *pipe.Registry
    observer Observer
    logger   *slog.Logger
}

func NewGraphExecutor(registry *pipe.Registry, observer Observer, logger *slog.Logger) *GraphExecutor
```

The `observer` field follows the existing runtime pattern: the executor calls `observer.OnTransition()` after each task completes, so the TUI and logging infrastructure see every task result without any pipe opting in. This is consistent with the "observability is infrastructure" principle.

The executor does not own memory injection. The pipeline executor handles memory for the graph step's input envelope. Individual task subprocesses receive the envelope as-is -- they are short-lived build agents that operate on the task spec and worktree, not on conversational memory.

---

## Public API

```go
// Execute runs the task graph to completion. It validates the DAG, sorts tasks
// into levels, and dispatches each level with concurrency control.
//
// The input envelope provides the base context (args, memory) that each task
// subprocess receives. The cfg.Args map may contain {{task.*}} placeholders
// that are resolved per-task before dispatch.
//
// Returns a single envelope with GraphOutput as structured content. On fatal
// errors (cycle detection, file conflicts, halt-mode failure), the envelope
// carries a fatal error and Results contains only tasks that ran.
func (g *GraphExecutor) Execute(
    ctx context.Context,
    cfg GraphConfig,
    tasks []TaskNode,
    input envelope.Envelope,
) envelope.Envelope
```

---

## Algorithm

### 1. Validate the DAG

Before any execution, validate structural invariants:

```
function validateDAG(tasks []TaskNode) error:
    ids = set of all task IDs

    // Check for duplicate IDs
    for each task in tasks:
        if task.ID already in ids-seen:
            return error("duplicate task ID: " + task.ID)
        add task.ID to ids-seen

    // Check for unknown dependencies
    for each task in tasks:
        for each dep in task.DependsOn:
            if dep not in ids:
                return error("task " + task.ID + " depends on unknown task " + dep)

    // Check for self-dependencies
    for each task in tasks:
        if task.ID in task.DependsOn:
            return error("task " + task.ID + " depends on itself")

    // Cycle detection happens during topological sort (step 2)
    return nil
```

### 2. Topological sort via Kahn's algorithm

Group tasks into levels where each level contains only tasks whose dependencies are fully satisfied by prior levels. Tasks within a level are independent and safe to run concurrently.

```
function topoSort(tasks []TaskNode) (levels [][]TaskNode, error):
    // Build adjacency and in-degree maps
    inDegree = map[taskID -> int], initialized to 0 for all tasks
    dependents = map[taskID -> []taskID]  // reverse adjacency

    for each task in tasks:
        for each dep in task.DependsOn:
            inDegree[task.ID]++
            dependents[dep] = append(dependents[dep], task.ID)

    // Seed level 0 with all zero-in-degree tasks
    queue = [task for task in tasks if inDegree[task.ID] == 0]
    if queue is empty and len(tasks) > 0:
        return error("cycle detected: no tasks with zero in-degree")

    levels = []
    processed = 0

    while queue is not empty:
        currentLevel = queue
        queue = []  // next level's candidates

        levels = append(levels, currentLevel)
        processed += len(currentLevel)

        for each task in currentLevel:
            for each dependent in dependents[task.ID]:
                inDegree[dependent]--
                if inDegree[dependent] == 0:
                    queue = append(queue, taskByID[dependent])

    if processed != len(tasks):
        return error("cycle detected: " + (len(tasks) - processed) + " tasks unreachable")

    return levels, nil
```

### 3. File conflict detection

After sorting into levels, validate that no two tasks within the same level share files. This is a hard constraint from the decompose pipe's contract -- if violated, the decompose pipe produced an invalid graph and execution must not proceed.

```
function validateFileConflicts(levels [][]TaskNode) error:
    for levelIdx, level in levels:
        seen = map[filepath -> taskID]
        for each task in level:
            for each file in task.Files:
                if file in seen:
                    return error(
                        "file conflict at level " + levelIdx + ": " +
                        file + " claimed by both " + seen[file] + " and " + task.ID
                    )
                seen[file] = task.ID
    return nil
```

This check runs once, before any tasks execute. It is a fatal error -- the output envelope carries a fatal `EnvelopeError` with the conflict details.

### 4. Level-based parallel dispatch

Execute levels sequentially. Within each level, dispatch tasks concurrently up to `max_parallel`.

Use `golang.org/x/sync/errgroup` with `SetLimit` -- it combines WaitGroup, goroutine dispatch, and concurrency limiting into one construct. The project already has `x/sync` as a dependency.

```
function executeLevels(ctx, cfg, levels, input) ([]TaskResult, error):
    allResults = []
    failedIDs = set()  // tracks tasks that failed (for continue-independent)
    mu = sync.Mutex{}  // guards allResults and failedIDs

    for levelIdx, level in levels:
        observer.OnTransition("graph", levelStartEnvelope(levelIdx), 0)

        // Filter out tasks whose dependencies failed (both modes)
        runnable = []
        for each task in level:
            if any dep in task.DependsOn is in failedIDs:
                result = TaskResult{
                    ID: task.ID, Name: task.Name,
                    Status: "skipped",
                    Error: "dependency failed",
                }
                allResults = append(allResults, result)
                failedIDs.add(task.ID)  // propagate skip to downstream
                continue
            runnable = append(runnable, task)

        if len(runnable) == 0:
            continue

        levelCtx, levelCancel = context.WithCancel(ctx)
        g, gCtx = errgroup.WithContext(levelCtx)
        g.SetLimit(effectiveMaxParallel(cfg))

        for each task in runnable:
            g.Go(func() error:
                // Check if level was cancelled (halt mode)
                if gCtx.Err() != nil:
                    mu.Lock()
                    allResults = append(allResults, TaskResult{
                        ID: task.ID, Name: task.Name,
                        Status: "cancelled",
                    })
                    mu.Unlock()
                    return nil

                result = executeTask(gCtx, cfg, task, input)

                mu.Lock()
                allResults = append(allResults, result)
                if result.Status == "fail":
                    failedIDs.add(result.ID)
                mu.Unlock()

                if result.Status == "fail" && cfg.OnTaskFailure == "halt":
                    levelCancel()  // cancel remaining tasks at this level
                return nil
            )

        g.Wait()
        levelCancel()  // cleanup

        // Check if any task in this level failed
        levelFailed = false
        for each result in level results:
            if result.Status == "fail":
                levelFailed = true

        // In halt mode, stop processing further levels on any failure
        if levelFailed && cfg.OnTaskFailure == "halt":
            return allResults, error("task failed in halt mode")

    return allResults, nil
```

Note: `errgroup.SetLimit` acts as a semaphore internally -- goroutines beyond the limit block in `g.Go()` until a slot opens. This replaces the hand-rolled buffered-channel semaphore. The `errgroup` error return is unused here (we always return nil) because task failures are tracked via `failedIDs`, not Go errors -- the graph executor needs to continue collecting results even after failures.

### 5. Single task execution

Each task is executed by invoking the configured pipe's `StreamHandler` with per-task flags. `StreamHandler` is used (rather than `Handler`) because it accepts a `context.Context`, which is required for cancellation to propagate through to the subprocess.

```
function executeTask(ctx, cfg, task TaskNode, input envelope.Envelope) TaskResult:
    start = time.Now()

    // Resolve {{task.*}} variables in args
    flags = resolveTaskArgs(cfg.Args, task)

    // Get stream handler from registry (context-aware)
    handler, ok = registry.GetStream(cfg.Pipe)
    if not ok:
        return TaskResult{ID: task.ID, Name: task.Name, Status: "fail",
                          Error: "pipe not found: " + cfg.Pipe,
                          Duration: time.Since(start)}

    // Build task-specific envelope
    taskEnv = input  // shallow copy
    taskEnv.Args = mergeFlags(input.Args, flags)

    // Execute (sink discards chunks -- graph tasks don't stream to TUI)
    result = handler(ctx, taskEnv, flags, func(string) {})
    duration = time.Since(start)

    observer.OnTransition(cfg.Pipe, result, duration)

    if isFatal(result):
        return TaskResult{ID: task.ID, Name: task.Name, Status: "fail",
                          Error: result.Error.Message, Duration: duration}

    return TaskResult{ID: task.ID, Name: task.Name, Status: "pass",
                      Duration: duration}
```

### 6. Template variable resolution for `{{task.*}}`

The graph executor resolves `{{task.*}}` placeholders in the `cfg.Args` map for each task. These are simple string replacements, not Go `text/template` -- the pipeline executor has already resolved all non-task variables before calling the graph executor.

Supported task variables:
- `{{task.id}}` -- the task's ID
- `{{task.name}}` -- the task's name
- `{{task.spec}}` -- the task's mini-spec
- `{{task.files}}` -- comma-separated list of the task's files

```go
// resolveTaskArgs returns a copy of args with {{task.*}} placeholders replaced
// by values from the given TaskNode.
func resolveTaskArgs(args map[string]string, task TaskNode) map[string]string {
    resolved := make(map[string]string, len(args))
    replacer := strings.NewReplacer(
        "{{task.id}}", task.ID,
        "{{task.name}}", task.Name,
        "{{task.spec}}", task.Spec,
        "{{task.files}}", strings.Join(task.Files, ","),
    )
    for k, v := range args {
        resolved[k] = replacer.Replace(v)
    }
    return resolved
}
```

This is intentionally simple string replacement rather than `text/template`. The task variables are always known string values and never require filters or conditionals. Using `strings.NewReplacer` avoids template parse errors from specs that contain `{{` or `}}` in their text.

---

## Failure Modes

### `halt` (default)

When a task fails and `on_task_failure` is `halt`:

1. Cancel all other running tasks at the same level via context cancellation
2. Skip all subsequent levels entirely
3. Return a fatal error envelope with the `GraphOutput` containing results for tasks that completed (pass, fail, or cancelled)

Cancelled tasks appear in results with `status: "cancelled"`. They are not counted as failures -- the root cause is the task that actually failed.

### `continue-independent`

When a task fails and `on_task_failure` is `continue-independent`:

1. Let all other tasks at the same level finish normally
2. At subsequent levels, skip any task that transitively depends on the failed task
3. Skipped tasks appear in results with `status: "skipped"` and `error: "dependency failed"`
4. Continue executing all tasks that have no dependency on the failed task
5. After all levels complete, if any task failed, return a non-fatal envelope (the pipeline can decide what to do next -- typically proceed to verify/fix)

Transitive dependency tracking: maintain a `failedIDs` set. When a task fails, add its ID. Before dispatching a task, check if any of its `depends_on` entries are in `failedIDs`. If so, skip it and add its own ID to `failedIDs` (so downstream tasks are also skipped).

### Cancellation

The `Execute` method accepts a `context.Context`. If the parent context is cancelled (e.g., the pipeline is aborted), all running task subprocesses are cancelled via their derived contexts.

The graph executor uses `StreamHandler` (not `Handler`) to dispatch tasks because `StreamHandler` accepts a `context.Context`, which is required for cancellation to propagate to subprocesses. The `Handler` type does not accept a context -- it creates its own internal timeout context -- so it cannot be cancelled externally. Persistent processes expose both handler types; the graph executor always uses the stream variant.

---

## Output Envelope

The graph executor produces a single envelope regardless of outcome:

```go
func buildGraphOutput(stepName string, results []TaskResult, levels int, fatalErr error) envelope.Envelope {
    out := envelope.New(stepName, "graph-complete")

    var completed, failed int
    for _, r := range results {
        switch r.Status {
        case "pass":
            completed++
        case "fail":
            failed++
        }
    }

    out.Content = GraphOutput{
        TasksCompleted: completed,
        TasksFailed:    failed,
        Levels:         levels,
        Results:        results,
    }
    out.ContentType = envelope.ContentStructured

    if fatalErr != nil {
        out.Error = envelope.FatalError(fatalErr.Error())
    }

    return out
}
```

The pipeline executor stores `GraphOutput` fields in the context map under the step name (e.g., `ctx["build-tasks.tasks_completed"]`, `ctx["build-tasks.results"]`). No downstream step in the current pipeline definition references these fields, but they are available for future use and for TUI display.

---

## Concurrency Control

The `max_parallel` field caps the number of concurrent pipe subprocesses. Implementation uses `errgroup.SetLimit()` from `golang.org/x/sync/errgroup`, which is already an indirect dependency of the project.

`errgroup.SetLimit` blocks inside `g.Go()` when the limit is reached, so goroutines are not created until a slot opens. This means context cancellation (halt mode via `levelCancel()`) prevents queued tasks from starting -- they check `gCtx.Err()` at the top of the goroutine body.

Default value: **4** when `max_parallel` is 0 or unset. This default lives in the graph executor, not in config parsing:

```go
const defaultMaxParallel = 4

func effectiveMaxParallel(cfg GraphConfig) int {
    if cfg.MaxParallel <= 0 {
        return defaultMaxParallel
    }
    return cfg.MaxParallel
}
```

---

## Error Handling Strategy

| Error | Severity | Behavior |
|---|---|---|
| Duplicate task ID in input | Fatal | Return immediately, no tasks run |
| Unknown dependency reference | Fatal | Return immediately, no tasks run |
| Cycle detected (Kahn's algorithm) | Fatal | Return immediately, no tasks run |
| File conflict within a level | Fatal | Return immediately, no tasks run |
| StreamHandler not found in registry | Fatal (per-task) | Task fails; halt/continue policy applies |
| Subprocess timeout | Fatal (per-task) | Task fails; halt/continue policy applies |
| Subprocess non-zero exit | Fatal (per-task) | Task fails; halt/continue policy applies |
| Parent context cancelled | Fatal | All running tasks cancelled, return partial results |
| Empty task list | Non-error | Return envelope with zero counts, zero levels |

All validation errors (first four rows) are caught before any subprocess is spawned. The output envelope contains a fatal error and an empty `Results` slice.

---

## File Locations

| File | Purpose |
|---|---|
| `internal/runtime/graph.go` | `GraphExecutor`, `TaskNode`, `TaskResult`, `GraphOutput`, topological sort, level dispatch, file conflict detection, task variable resolution |
| `internal/runtime/graph_test.go` | All test cases below |

No other files are created or modified. The graph executor depends only on:
- `internal/pipe` (Registry, StreamHandler)
- `internal/envelope` (Envelope, error constructors)
- `golang.org/x/sync/errgroup` (concurrency-limited dispatch)
- Standard library (`context`, `sync`, `strings`, `time`, `fmt`, `log/slog`)

---

## Test Cases

All tests in `internal/runtime/graph_test.go`. Tests register mock `StreamHandler` functions in a `pipe.Registry` via `RegisterStream` -- no real subprocesses. A mock `Observer` records `OnTransition` calls for verification.

### DAG Validation

**TestValidateDAG_DuplicateID** -- Two tasks with the same ID. Expect fatal error mentioning the duplicate ID.

**TestValidateDAG_UnknownDependency** -- Task depends on an ID not present in the task list. Expect fatal error mentioning the unknown ID.

**TestValidateDAG_SelfDependency** -- Task lists its own ID in `depends_on`. Expect fatal error.

**TestValidateDAG_EmptyList** -- Zero tasks. Expect success with zero levels.

### Topological Sort

**TestTopoSort_Linear** -- t1 -> t2 -> t3 (linear chain). Expect 3 levels, one task each.

**TestTopoSort_Diamond** -- t1 -> t2, t1 -> t3, t2 -> t4, t3 -> t4 (diamond). Expect 3 levels: [t1], [t2, t3], [t4].

**TestTopoSort_Wide** -- t1, t2, t3, t4 all independent (no deps). Expect 1 level with all 4 tasks.

**TestTopoSort_CycleDetection** -- t1 -> t2 -> t3 -> t1. Expect fatal error mentioning "cycle".

**TestTopoSort_MultipleRoots** -- t1 (no deps), t2 (no deps), t3 depends on t1, t4 depends on t2. Expect 2 levels: [t1, t2], [t3, t4].

### File Conflict Detection

**TestFileConflict_SameLevel** -- Two tasks at the same level both list `internal/foo.go`. Expect fatal error mentioning the file and both task IDs.

**TestFileConflict_DifferentLevels** -- Two tasks at different levels share a file. Expect success (different levels run sequentially, so this is safe).

**TestFileConflict_NoFiles** -- Tasks with empty `files` lists. Expect success.

### Execution -- Halt Mode

**TestExecute_HaltMode_AllPass** -- 3-level diamond, all tasks succeed. Expect `GraphOutput` with 4 completed, 0 failed, 3 levels.

**TestExecute_HaltMode_TaskFails** -- Level 1 has two tasks; one fails. Expect the other task at level 1 to be cancelled (or complete if it finished first), level 2 skipped entirely, fatal error on output envelope.

**TestExecute_HaltMode_Level0Fails** -- Single task at level 0 fails. Expect all subsequent levels skipped, fatal error.

### Execution -- Continue-Independent Mode

**TestExecute_Continue_SkipsDependents** -- Diamond: t1 passes, t2 fails, t3 passes, t4 depends on t2 and t3. Expect t4 skipped (dependency t2 failed), t3 passes, output has 2 completed + 1 failed + 1 skipped.

**TestExecute_Continue_IndependentBranchContinues** -- t1 fails, t2 (independent, no deps on t1) passes. Expect both results present, no fatal error on envelope.

**TestExecute_Continue_TransitiveSkip** -- t1 -> t2 -> t3, t1 fails. Expect t2 skipped, t3 skipped (transitive). Both show `status: "skipped"`.

### Concurrency Control

**TestExecute_MaxParallel** -- 6 tasks at level 0, `max_parallel: 2`. Use a mock stream handler that records concurrent execution count (e.g., atomic counter + max tracker). Expect max concurrent never exceeds 2.

**TestExecute_DefaultMaxParallel** -- `max_parallel: 0`. Same 6-task setup. Expect max concurrent never exceeds 4 (the default).

### Task Variable Resolution

**TestResolveTaskArgs** -- Args map contains `{{task.id}}`, `{{task.name}}`, `{{task.spec}}`, `{{task.files}}`. Verify all are replaced with task values. `{{task.files}}` becomes comma-separated.

**TestResolveTaskArgs_NoPlaceholders** -- Args with no `{{task.*}}` references. Verify args are returned unchanged.

**TestResolveTaskArgs_SpecWithBraces** -- Task spec contains literal `{{` (e.g., Go template code in the spec). Verify it passes through without error (this is why we use `strings.NewReplacer` instead of `text/template`).

### Context Cancellation

**TestExecute_ParentContextCancelled** -- Start execution, cancel parent context during level 1. Expect running tasks cancelled, partial results returned, fatal error on envelope.

### Observability

**TestExecute_ObserverCalledPerTask** -- 3-task diamond, all pass. Verify `observer.OnTransition` is called once per task (3 times total) with the pipe name and a non-zero duration.

### Edge Cases

**TestExecute_SingleTask** -- One task, no dependencies. Expect 1 level, 1 completed.

**TestExecute_EmptyTaskList** -- Zero tasks. Expect success envelope with 0 completed, 0 levels.

**TestExecute_PipeNotFound** -- `cfg.Pipe` references a pipe not in the registry. Expect task failure.

---

## Non-Goals

These are explicitly out of scope for this spec:

- **Pipeline executor** (`internal/runtime/pipeline.go`) -- separate spec/implementation
- **Decompose pipe** (`internal/pipes/decompose/`) -- separate spec/implementation
- **Template resolver** for `{{stepname.field}}` variables -- belongs to pipeline executor
- **Context map management** -- belongs to pipeline executor
- **Loop and cycle logic** -- belongs to pipeline executor
- **Streaming/TUI integration** -- the graph executor does not stream; the pipeline executor wraps it with observer calls for TUI updates
- **Retry logic for failed tasks** -- not in v1; the verify-fix loop handles correctness after the graph completes
- **Nested graphs** -- a graph step cannot contain another graph step
- **Dynamic max_parallel** -- the value is fixed for the entire graph execution

---

## Review Notes

Changes made during review:

1. **Handler -> StreamHandler for task dispatch.** The spec originally used `registry.Get()` (which returns `Handler`) and claimed `SubprocessHandler` already uses `exec.CommandContext` for cancellation. This was incorrect -- `Handler` does not accept `context.Context`; it creates its own internal timeout context and cannot be cancelled externally. Changed to `registry.GetStream()` / `StreamHandler` throughout, which accepts `context.Context` and propagates cancellation to subprocesses. Affected sections: Single task execution, Cancellation, Error handling table, File locations, Test cases.

2. **Replaced hand-rolled semaphore with `errgroup.SetLimit`.** The level dispatch pseudocode used a buffered channel as a counting semaphore plus a manual `sync.WaitGroup` plus a results channel. `golang.org/x/sync/errgroup` (already an indirect dependency in go.mod) combines all three into one construct with `SetLimit()` for concurrency control. This reduces the concurrency boilerplate and eliminates a class of bugs (forgetting `wg.Add`, channel sizing, etc.).

3. **Fixed `TaskResult.Status` comment.** Struct comment said `"pass" or "fail"` but the pseudocode also produces `"skipped"` and `"cancelled"`. Updated to list all four values.

4. **Added `observer.OnTransition` calls.** The spec declared an `observer Observer` field on `GraphExecutor` but never called it. This violated the "observability is infrastructure" principle from `docs/virgil.md`. Added `observer.OnTransition()` calls after each task completes in `executeTask`, matching the existing `Runtime.runStep` pattern. Added `TestExecute_ObserverCalledPerTask` test case.

5. **Used `isFatal()` helper.** The `executeTask` pseudocode checked `result.Error.Severity == "fatal"` directly. The existing runtime has an `isFatal()` helper for this. Changed to use it for consistency.

6. **Added mutex to level dispatch.** With `errgroup`, goroutines write results concurrently into a shared slice. Added `sync.Mutex` to guard `allResults` and `failedIDs` in the pseudocode.

**Not changed (reviewed and confirmed sound):**

- **Kahn's algorithm for toposort.** Considered suggesting a third-party DAG library but the implementation is ~30 lines of straightforward pseudocode. A library dependency would be heavier than the code it replaces.
- **Buffered-channel semaphore in Concurrency Control section.** Removed in favor of errgroup, but the `effectiveMaxParallel` helper was kept as-is -- it's clean and appropriate.
- **Scope boundaries.** The spec is tightly scoped to just the graph executor. Non-goals are explicit and appropriate.
- **Template variable resolution via `strings.NewReplacer`.** The justification for avoiding `text/template` (specs containing literal `{{`) is sound.
- **File conflict detection as pre-execution validation.** Running it once before any tasks execute is correct -- it's a contract violation by the decompose pipe, not a runtime condition.
- **Test coverage.** The test cases are focused and sufficient. Each covers a distinct behavior without redundancy.
