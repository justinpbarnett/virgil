package decompose

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
)

// Task represents a single node in the decomposition DAG.
type Task struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Spec      string   `json:"spec"`
	Files     []string `json:"files"`
	DependsOn []string `json:"depends_on"`
}

// DecomposeOutput is the structured content of the output envelope.
type DecomposeOutput struct {
	Tasks []Task `json:"tasks"`
}

// ReviewFinding represents a single finding from the review pipe.
// Matches the structure used by the build and review pipes.
type ReviewFinding struct {
	Category string `json:"category"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Action   string `json:"action"`
}

// templateData is the input to prompt templates.
type templateData struct {
	Spec     string
	Context  string
	MaxTasks string
	Findings []ReviewFinding
}

// CompileTemplates re-exports pipeutil.CompileTemplates for use by cmd/main.go.
var CompileTemplates = pipeutil.CompileTemplates

// preparePrompt selects the correct template and renders both system and user prompts.
func preparePrompt(compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string) (systemPrompt, userPrompt string, errEnv *envelope.EnvelopeError) {
	spec := pipeutil.FlagOrDefault(flags, "spec", "")
	if spec == "" {
		return "", "", envelope.FatalError("no spec provided for decompose")
	}

	maxTasks := pipeutil.FlagOrDefault(flags, "max_tasks", "8")
	cxt := envelope.ContentToText(input.Content, input.ContentType)

	var findings []ReviewFinding
	if findingsJSON := flags["findings"]; findingsJSON != "" {
		if err := json.Unmarshal([]byte(findingsJSON), &findings); err != nil {
			return "", "", envelope.FatalError(fmt.Sprintf("invalid findings JSON: %v", err))
		}
	}

	tmplName := "initial"
	if len(findings) > 0 {
		tmplName = "rework"
	}

	systemPrompt = pipeConfig.Prompts.System

	userPrompt, err := pipeutil.ExecuteTemplate(compiled, tmplName, templateData{
		Spec:     spec,
		Context:  cxt,
		MaxTasks: maxTasks,
		Findings: findings,
	})
	if err != nil {
		return "", "", envelope.FatalError(fmt.Sprintf("template execution failed: %v", err))
	}

	return systemPrompt, userPrompt, nil
}

// extractJSON tries three strategies to extract a DecomposeOutput from a model response.
func extractJSON(response string) (DecomposeOutput, error) {
	var output DecomposeOutput

	// Strategy 1: direct parse
	if err := json.Unmarshal([]byte(response), &output); err == nil {
		return output, nil
	}

	// Strategy 2: strip markdown fences then parse
	stripped := pipeutil.StripMarkdownFences(response)
	if err := json.Unmarshal([]byte(stripped), &output); err == nil {
		return output, nil
	}

	// Strategy 3: find first '{' and parse from there
	if idx := strings.Index(response, "{"); idx != -1 {
		if err := json.Unmarshal([]byte(response[idx:]), &output); err == nil {
			return output, nil
		}
	}

	truncated := response
	if len(truncated) > 200 {
		truncated = truncated[:200]
	}
	return DecomposeOutput{}, fmt.Errorf("failed to extract task graph from provider response: %s", truncated)
}

// validateDAG validates the decompose output: required fields, unique IDs,
// max tasks cap, valid dependency references, cycle detection (Kahn's), and
// file-disjoint constraint per dependency level.
func validateDAG(output DecomposeOutput, maxTasks int) error {
	// Step 1: required fields
	for _, t := range output.Tasks {
		if t.ID == "" {
			return fmt.Errorf("task %q: missing required field id", t.Name)
		}
		if t.Name == "" {
			return fmt.Errorf("task %q: missing required field name", t.ID)
		}
		if t.Spec == "" {
			return fmt.Errorf("task %q: missing required field spec", t.ID)
		}
		if len(t.Files) == 0 {
			return fmt.Errorf("task %q: missing required field files", t.ID)
		}
	}

	// Step 2: unique IDs
	idSet := make(map[string]struct{}, len(output.Tasks))
	for _, t := range output.Tasks {
		if _, exists := idSet[t.ID]; exists {
			return fmt.Errorf("duplicate task ID: %s", t.ID)
		}
		idSet[t.ID] = struct{}{}
	}

	// Step 3: max tasks cap
	if len(output.Tasks) > maxTasks {
		return fmt.Errorf("task count %d exceeds max_tasks %d", len(output.Tasks), maxTasks)
	}

	// Step 4: valid dependency references
	for _, t := range output.Tasks {
		for _, dep := range t.DependsOn {
			if _, exists := idSet[dep]; !exists {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}

	// Steps 5 and 6: cycle detection (Kahn's algorithm) + level assignment + file-disjoint check

	// Build adjacency list (dependents: who depends on each task)
	// and reverse map (task index by ID)
	taskByID := make(map[string]int, len(output.Tasks))
	for i, t := range output.Tasks {
		taskByID[t.ID] = i
	}

	inDegree := make([]int, len(output.Tasks))
	// dependents[i] = list of task indices that depend on task i
	dependents := make([][]int, len(output.Tasks))
	for i, t := range output.Tasks {
		for _, dep := range t.DependsOn {
			depIdx := taskByID[dep]
			dependents[depIdx] = append(dependents[depIdx], i)
			inDegree[i]++
		}
	}

	// Level assignment: level[i] = depth from root (0 for tasks with no dependencies)
	level := make([]int, len(output.Tasks))

	// Initialize queue with tasks having in-degree 0
	queue := make([]int, 0, len(output.Tasks))
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
			level[i] = 0
		}
	}

	processed := 0
	// levelFiles tracks files seen at each level: level -> file -> taskID
	levelFiles := make(map[int]map[string]string)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		processed++

		t := output.Tasks[curr]
		lv := level[curr]

		// File-disjoint check at this level
		if levelFiles[lv] == nil {
			levelFiles[lv] = make(map[string]string)
		}
		for _, f := range t.Files {
			if existing, seen := levelFiles[lv][f]; seen {
				return fmt.Errorf("file %q appears in multiple tasks at level %d: %s, %s", f, lv, existing, t.ID)
			}
			levelFiles[lv][f] = t.ID
		}

		// Propagate to dependents
		for _, dep := range dependents[curr] {
			inDegree[dep]--
			// Update level: a task's level is max(level of all its dependencies) + 1
			if level[curr]+1 > level[dep] {
				level[dep] = level[curr] + 1
			}
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if processed != len(output.Tasks) {
		return fmt.Errorf("task graph contains a cycle")
	}

	return nil
}

// runDecompose orchestrates prompt preparation, provider call, JSON extraction, and validation.
func runDecompose(ctx context.Context, provider bridge.Provider, compiled map[string]*template.Template, pipeConfig config.PipeConfig, input envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("decompose", "decompose")
	out.Args = flags

	systemPrompt, userPrompt, errEnv := preparePrompt(compiled, pipeConfig, input, flags)
	if errEnv != nil {
		out.Error = errEnv
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Debug("calling provider", "prompt_len", len(userPrompt))

	response, err := provider.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		logger.Error("provider call failed", "error", err)
		out.Error = envelope.ClassifyError("decompose failed", err)
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	if response == "" {
		out.Error = envelope.FatalError("provider returned empty response")
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	output, err := extractJSON(response)
	if err != nil {
		logger.Error("json extraction failed", "error", err)
		out.Error = envelope.FatalError(err.Error())
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	maxTasks := 8
	if s := pipeutil.FlagOrDefault(flags, "max_tasks", "8"); s != "" {
		if v := parseMaxTasks(s); v > 0 {
			maxTasks = v
		}
	}

	if err := validateDAG(output, maxTasks); err != nil {
		logger.Error("dag validation failed", "error", err)
		out.Error = envelope.FatalError(err.Error())
		out.Duration = time.Since(out.Timestamp)
		return out
	}

	logger.Info("decompose complete", "tasks", len(output.Tasks))
	out.Content = output
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

// parseMaxTasks parses a string to an int, returning 0 on failure.
func parseMaxTasks(s string) int {
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil {
		return 0
	}
	return v
}

// NewHandler creates a decompose pipe Handler that compiles templates on the fly.
func NewHandler(provider bridge.Provider, pipeConfig config.PipeConfig, logger *slog.Logger) pipe.Handler {
	return NewHandlerWith(provider, pipeConfig, CompileTemplates(pipeConfig), logger)
}

// NewHandlerWith creates a decompose pipe Handler with pre-compiled templates.
func NewHandlerWith(provider bridge.Provider, pipeConfig config.PipeConfig, compiled map[string]*template.Template, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		ctx := context.Background()
		return runDecompose(ctx, provider, compiled, pipeConfig, input, flags, logger)
	}
}
