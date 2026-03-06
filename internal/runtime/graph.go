package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

const defaultMaxParallel = 4

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

// TaskResult records the outcome of a single task execution within the graph.
type TaskResult struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Status   string        `json:"status"` // "pass", "fail", "skipped", or "cancelled"
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// GraphOutput is the structured content placed into the output envelope
// after graph execution completes.
type GraphOutput struct {
	TasksCompleted int          `json:"tasks_completed"`
	TasksFailed    int          `json:"tasks_failed"`
	Levels         int          `json:"levels"`
	Results        []TaskResult `json:"results"`
}

// GraphConfig holds the configuration for a graph execution step. It mirrors
// the pipeline YAML graph: block and is reproduced here until the pipeline
// executor (internal/runtime/pipeline.go) is built.
type GraphConfig struct {
	Source        string            `yaml:"source"`
	Pipe          string            `yaml:"pipe"`
	Args          map[string]string `yaml:"args"`
	OnTaskFailure string            `yaml:"on_task_failure"` // "halt" or "continue-independent"
	MaxParallel   int               `yaml:"max_parallel"`    // 0 = default (4)
}

// GraphExecutor runs a DAG of tasks through a single pipe, dispatching
// independent tasks concurrently and respecting dependency ordering.
type GraphExecutor struct {
	registry *pipe.Registry
	observer Observer
	logger   *slog.Logger
}

// NewGraphExecutor creates a GraphExecutor. If observer is nil a noop observer
// is used; if logger is nil slog.Default() is used.
func NewGraphExecutor(registry *pipe.Registry, observer Observer, logger *slog.Logger) *GraphExecutor {
	if observer == nil {
		observer = &noopObserver{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &GraphExecutor{
		registry: registry,
		observer: observer,
		logger:   logger,
	}
}

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
) envelope.Envelope {
	// Empty task list is not an error.
	if len(tasks) == 0 {
		return buildGraphOutput("graph", nil, 0, nil)
	}

	// Step 1: validate the DAG.
	if err := validateDAG(tasks); err != nil {
		return buildGraphOutput("graph", nil, 0, err)
	}

	// Step 2: topological sort into levels.
	levels, err := topoSort(tasks)
	if err != nil {
		return buildGraphOutput("graph", nil, 0, err)
	}

	// Step 3: file conflict detection.
	if err := validateFileConflicts(levels); err != nil {
		return buildGraphOutput("graph", nil, len(levels), err)
	}

	// Step 4: execute levels.
	results, execErr := g.executeLevels(ctx, cfg, levels, input)
	return buildGraphOutput("graph", results, len(levels), execErr)
}

// validateDAG checks structural invariants before any execution begins.
func validateDAG(tasks []TaskNode) error {
	ids := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		if _, seen := ids[t.ID]; seen {
			return fmt.Errorf("duplicate task ID: %s", t.ID)
		}
		ids[t.ID] = struct{}{}
	}

	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				return fmt.Errorf("task %s depends on itself", t.ID)
			}
			if _, ok := ids[dep]; !ok {
				return fmt.Errorf("task %s depends on unknown task %s", t.ID, dep)
			}
		}
	}
	return nil
}

// topoSort groups tasks into levels using Kahn's algorithm. Tasks within a
// level are independent of each other and safe to run concurrently.
func topoSort(tasks []TaskNode) ([][]TaskNode, error) {
	byID := make(map[string]TaskNode, len(tasks))
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))

	for _, t := range tasks {
		byID[t.ID] = t
		if _, ok := inDegree[t.ID]; !ok {
			inDegree[t.ID] = 0
		}
		for _, dep := range t.DependsOn {
			inDegree[t.ID]++
			dependents[dep] = append(dependents[dep], t.ID)
		}
	}

	// Seed with zero-in-degree tasks.
	var queue []TaskNode
	for _, t := range tasks {
		if inDegree[t.ID] == 0 {
			queue = append(queue, t)
		}
	}
	if len(queue) == 0 {
		return nil, fmt.Errorf("cycle detected: no tasks with zero in-degree")
	}

	var levels [][]TaskNode
	processed := 0

	for len(queue) > 0 {
		currentLevel := queue
		queue = nil

		levels = append(levels, currentLevel)
		processed += len(currentLevel)

		for _, t := range currentLevel {
			for _, depID := range dependents[t.ID] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					queue = append(queue, byID[depID])
				}
			}
		}
	}

	if processed != len(tasks) {
		return nil, fmt.Errorf("cycle detected: %d tasks unreachable", len(tasks)-processed)
	}

	return levels, nil
}

// validateFileConflicts ensures no two tasks within the same level claim the
// same file. Tasks at different levels run sequentially, so sharing is safe.
func validateFileConflicts(levels [][]TaskNode) error {
	for levelIdx, level := range levels {
		seen := make(map[string]string) // filepath -> taskID
		for _, t := range level {
			for _, f := range t.Files {
				if ownerID, conflict := seen[f]; conflict {
					return fmt.Errorf(
						"file conflict at level %d: %s claimed by both %s and %s",
						levelIdx, f, ownerID, t.ID,
					)
				}
				seen[f] = t.ID
			}
		}
	}
	return nil
}

// executeLevels runs each level sequentially, dispatching tasks within a level
// concurrently up to the configured max_parallel limit.
func (g *GraphExecutor) executeLevels(
	ctx context.Context,
	cfg GraphConfig,
	levels [][]TaskNode,
	input envelope.Envelope,
) ([]TaskResult, error) {
	var allResults []TaskResult
	failedIDs := make(map[string]struct{})
	var mu sync.Mutex

	limit := effectiveMaxParallel(cfg)

	for _, level := range levels {
		// Filter out tasks whose dependencies failed (both failure modes).
		var runnable []TaskNode
		for _, t := range level {
			if depFailed := anyDepFailed(t, failedIDs); depFailed != "" {
				result := TaskResult{
					ID:     t.ID,
					Name:   t.Name,
					Status: "skipped",
					Error:  "dependency failed",
				}
				mu.Lock()
				allResults = append(allResults, result)
				failedIDs[t.ID] = struct{}{} // propagate skip downstream
				mu.Unlock()
				continue
			}
			runnable = append(runnable, t)
		}

		if len(runnable) == 0 {
			continue
		}

		levelCtx, levelCancel := context.WithCancel(ctx)
		eg, gCtx := errgroup.WithContext(levelCtx)
		eg.SetLimit(limit)

		for _, task := range runnable {
			task := task // capture
			eg.Go(func() error {
				// If level was cancelled (halt mode), record as cancelled.
				if gCtx.Err() != nil {
					mu.Lock()
					allResults = append(allResults, TaskResult{
						ID:     task.ID,
						Name:   task.Name,
						Status: "cancelled",
					})
					mu.Unlock()
					return nil
				}

				result := g.executeTask(gCtx, cfg, task, input)

				mu.Lock()
				allResults = append(allResults, result)
				if result.Status == "fail" {
					failedIDs[result.ID] = struct{}{}
				}
				mu.Unlock()

				if result.Status == "fail" && cfg.OnTaskFailure == "halt" {
					levelCancel() // cancel remaining tasks at this level
				}
				return nil
			})
		}

		_ = eg.Wait()
		levelCancel() // cleanup

		// In halt mode, stop processing further levels if any task failed.
		if cfg.OnTaskFailure == "halt" || cfg.OnTaskFailure == "" {
			mu.Lock()
			levelHasFailed := false
			for _, r := range allResults {
				if r.Status == "fail" {
					levelHasFailed = true
					break
				}
			}
			mu.Unlock()
			if levelHasFailed {
				return allResults, fmt.Errorf("task failed in halt mode")
			}
		}
	}

	return allResults, nil
}

// anyDepFailed returns the first failed/skipped dependency ID found, or empty
// string if all dependencies are clean.
func anyDepFailed(task TaskNode, failedIDs map[string]struct{}) string {
	for _, dep := range task.DependsOn {
		if _, failed := failedIDs[dep]; failed {
			return dep
		}
	}
	return ""
}

// executeTask invokes the configured pipe's StreamHandler with per-task flags.
func (g *GraphExecutor) executeTask(
	ctx context.Context,
	cfg GraphConfig,
	task TaskNode,
	input envelope.Envelope,
) TaskResult {
	start := time.Now()

	handler, ok := g.registry.GetStream(cfg.Pipe)
	if !ok {
		duration := time.Since(start)
		result := envelope.NewFatalError(cfg.Pipe, "pipe not found: "+cfg.Pipe)
		g.observer.OnTransition(cfg.Pipe, result, duration)
		return TaskResult{
			ID:       task.ID,
			Name:     task.Name,
			Status:   "fail",
			Error:    "pipe not found: " + cfg.Pipe,
			Duration: duration,
		}
	}

	// Resolve {{task.*}} variables per-task.
	flags := resolveTaskArgs(cfg.Args, task)

	// Build task-specific envelope with merged flags.
	taskEnv := input
	taskEnv.Args = mergeFlags(input.Args, flags)

	// Execute; graph tasks don't stream to TUI so sink discards chunks.
	result := handler(ctx, taskEnv, flags, func(string) {})
	duration := time.Since(start)

	g.observer.OnTransition(cfg.Pipe, result, duration)

	if isFatal(result) {
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Message
		}
		return TaskResult{
			ID:       task.ID,
			Name:     task.Name,
			Status:   "fail",
			Error:    errMsg,
			Duration: duration,
		}
	}

	return TaskResult{
		ID:       task.ID,
		Name:     task.Name,
		Status:   "pass",
		Duration: duration,
	}
}

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

// effectiveMaxParallel returns the concurrency limit, defaulting to
// defaultMaxParallel when cfg.MaxParallel is 0 or negative.
func effectiveMaxParallel(cfg GraphConfig) int {
	if cfg.MaxParallel <= 0 {
		return defaultMaxParallel
	}
	return cfg.MaxParallel
}

// buildGraphOutput constructs the output envelope from the collected results.
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
