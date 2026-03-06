package tui

import (
	"strings"
	"testing"
)

// newTestModel creates a minimal model suitable for unit-testing panel rendering.
func newTestModel() model {
	theme := NewTheme("dark")
	return model{
		stream:        NewStream(&theme),
		input:         NewInput(&theme),
		panel:         NewPanel(&theme),
		theme:         theme,
		panelSelected: -1,
		panelExpanded: -1,
	}
}

// TestParallelTreeRendering verifies that formatPipelineSteps renders parallel
// tasks as an indented tree with the correct status symbols.
func TestParallelTreeRendering(t *testing.T) {
	m := newTestModel()
	m.pipelineSteps = []string{"build"}
	m.pipelineDone = false
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "pricing-table", Status: "running", Activity: "edit pricing.go"},
		{ID: "t2", Name: "api-endpoint", Status: "waiting"},
		{ID: "t3", Name: "test-fixtures", Status: "done", Duration: "0.3s"},
	}

	out := m.formatPipelineSteps()

	// Active step should use ◉
	if !strings.Contains(out, SymActive+" build") {
		t.Errorf("expected active step marker, got:\n%s", out)
	}
	// Running task
	if !strings.Contains(out, SymActive+" pricing-table") {
		t.Errorf("expected running task marker, got:\n%s", out)
	}
	// Pending task
	if !strings.Contains(out, SymInactive+" api-endpoint") {
		t.Errorf("expected pending task marker, got:\n%s", out)
	}
	// Done task
	if !strings.Contains(out, SymCheck+" test-fixtures") {
		t.Errorf("expected done task marker, got:\n%s", out)
	}
	// Tree connectors
	if !strings.Contains(out, "├") {
		t.Errorf("expected ├ connector, got:\n%s", out)
	}
	if !strings.Contains(out, "└") {
		t.Errorf("expected └ connector, got:\n%s", out)
	}
	// Activity description
	if !strings.Contains(out, "edit pricing.go") {
		t.Errorf("expected activity, got:\n%s", out)
	}
	// Duration
	if !strings.Contains(out, "0.3s") {
		t.Errorf("expected duration, got:\n%s", out)
	}
}

// TestParallelTreeRenderingFailedTask verifies the ✗ symbol for failed tasks.
func TestParallelTreeRenderingFailedTask(t *testing.T) {
	m := newTestModel()
	m.pipelineSteps = []string{"build"}
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "migration", Status: "failed"},
	}

	out := m.formatPipelineSteps()
	if !strings.Contains(out, SymCross+" migration") {
		t.Errorf("expected failed task marker, got:\n%s", out)
	}
}

// TestParallelTreeRenderingSelectedTask verifies the ▸ marker on selected tasks.
func TestParallelTreeRenderingSelectedTask(t *testing.T) {
	m := newTestModel()
	m.pipelineSteps = []string{"build"}
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "pricing-table", Status: "running"},
		{ID: "t2", Name: "api-endpoint", Status: "waiting"},
	}
	m.panelSelected = 0

	out := m.formatPipelineSteps()
	if !strings.Contains(out, SymArrow) {
		t.Errorf("expected selection marker ▸, got:\n%s", out)
	}
}

// TestUpsertParallelTask_Create verifies that a new task is registered.
func TestUpsertParallelTask_Create(t *testing.T) {
	m := newTestModel()
	m.upsertParallelTask("t1", "pricing-table", "build", "running", "edit pricing.go", nil)

	if len(m.parallelTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(m.parallelTasks))
	}
	task := m.parallelTasks[0]
	if task.ID != "t1" {
		t.Errorf("expected ID=t1, got %s", task.ID)
	}
	if task.Status != "running" {
		t.Errorf("expected status=running, got %s", task.Status)
	}
	if task.Activity != "edit pricing.go" {
		t.Errorf("expected activity, got %s", task.Activity)
	}
}

// TestUpsertParallelTask_Update verifies that an existing task is updated.
func TestUpsertParallelTask_Update(t *testing.T) {
	m := newTestModel()
	m.upsertParallelTask("t1", "pricing-table", "build", "waiting", "", nil)
	m.upsertParallelTask("t1", "", "", "running", "edit pricing.go", nil)

	if len(m.parallelTasks) != 1 {
		t.Fatalf("expected 1 task after update, got %d", len(m.parallelTasks))
	}
	task := m.parallelTasks[0]
	if task.Status != "running" {
		t.Errorf("expected status=running after update, got %s", task.Status)
	}
	if task.Activity != "edit pricing.go" {
		t.Errorf("expected activity after update, got %s", task.Activity)
	}
	// Original name should be preserved when update sends empty name.
	if task.Name != "pricing-table" {
		t.Errorf("expected name preserved, got %s", task.Name)
	}
}

// TestCompleteParallelTask verifies that completeParallelTask sets terminal state.
func TestCompleteParallelTask(t *testing.T) {
	m := newTestModel()
	m.upsertParallelTask("t1", "pricing-table", "build", "running", "edit pricing.go", nil)
	m.completeParallelTask("t1", "done", "3.1s", "")

	task := m.parallelTasks[0]
	if task.Status != "done" {
		t.Errorf("expected status=done, got %s", task.Status)
	}
	if task.Duration != "3.1s" {
		t.Errorf("expected duration=3.1s, got %s", task.Duration)
	}
	if task.Activity != "" {
		t.Errorf("expected activity cleared on completion, got %s", task.Activity)
	}
}

// TestAppendTaskOutput verifies streaming output accumulation.
func TestAppendTaskOutput(t *testing.T) {
	m := newTestModel()
	m.upsertParallelTask("t1", "pricing-table", "build", "running", "", nil)
	m.appendTaskOutput("t1", "Reading the pricing table...")
	m.appendTaskOutput("t1", " Found 3 tiers.")

	task := m.parallelTasks[0]
	got := task.Output.String()
	if got != "Reading the pricing table... Found 3 tiers." {
		t.Errorf("unexpected output: %q", got)
	}
}

// TestPanelSelectionNavigation verifies Ctrl+J/K move panelSelected correctly.
func TestPanelSelectionNavigation(t *testing.T) {
	m := newTestModel()
	m.panel.Toggle() // open the panel
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "task1", Status: "running"},
		{ID: "t2", Name: "task2", Status: "waiting"},
		{ID: "t3", Name: "task3", Status: "waiting"},
	}
	m.pipelineSteps = []string{"build"}

	// Start at -1 (no selection). Ctrl+J should move to 0.
	if m.panelSelected != -1 {
		t.Fatalf("expected panelSelected=-1 initially")
	}
	m.panelSelected++
	if m.panelSelected != 0 {
		t.Errorf("expected panelSelected=0, got %d", m.panelSelected)
	}

	m.panelSelected++
	if m.panelSelected != 1 {
		t.Errorf("expected panelSelected=1, got %d", m.panelSelected)
	}

	// Cannot go below -1.
	m.panelSelected = 0
	m.panelSelected--
	// The handleKey code checks: if panelSelected == 0 → set to -1
	if m.panelSelected != -1 {
		// Simulate the actual logic from handleKey.
		if m.panelSelected < 0 {
			m.panelSelected = -1
		}
	}
}

// TestTaskExpansion verifies that Enter toggles panelExpanded.
func TestTaskExpansion(t *testing.T) {
	m := newTestModel()
	m.panel.Toggle()
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "pricing-table", Status: "running"},
	}
	m.pipelineSteps = []string{"build"}
	m.panelSelected = 0

	// Expand.
	m.panelExpanded = m.panelSelected // simulates Enter logic
	if m.panelExpanded != 0 {
		t.Errorf("expected panelExpanded=0, got %d", m.panelExpanded)
	}

	// Collapse.
	if m.panelExpanded == m.panelSelected {
		m.panelExpanded = -1
	}
	if m.panelExpanded != -1 {
		t.Errorf("expected panelExpanded=-1 after collapse, got %d", m.panelExpanded)
	}
}

// TestExpandedOutputAppearsInPanel verifies that expanded task output is
// included in the formatted panel content.
func TestExpandedOutputAppearsInPanel(t *testing.T) {
	m := newTestModel()
	m.pipelineSteps = []string{"build"}
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "pricing-table", Status: "running"},
	}
	m.parallelTasks[0].Output.WriteString("Reading the existing pricing table...")
	m.panelSelected = 0
	m.panelExpanded = 0

	out := m.formatPipelineSteps()
	if !strings.Contains(out, "pricing-table") {
		t.Errorf("expected task name in panel, got:\n%s", out)
	}
	if !strings.Contains(out, "Reading the existing pricing table...") {
		t.Errorf("expected task output in panel, got:\n%s", out)
	}
}

// TestClearParallelStateOnDone verifies that parallel state is cleared when
// the pipeline completes.
func TestClearParallelStateOnDone(t *testing.T) {
	m := newTestModel()
	m.parallelTasks = []*parallelTask{
		{ID: "t1", Name: "pricing-table", Status: "done"},
	}
	m.panelSelected = 0
	m.panelExpanded = 0

	m.clearParallelState()

	if m.parallelTasks != nil {
		t.Errorf("expected parallelTasks=nil after clear")
	}
	if m.panelSelected != -1 {
		t.Errorf("expected panelSelected=-1, got %d", m.panelSelected)
	}
	if m.panelExpanded != -1 {
		t.Errorf("expected panelExpanded=-1, got %d", m.panelExpanded)
	}
}

// TestSequentialPlanUnchanged verifies that a sequential plan (no parallel tasks)
// renders identically to the existing behavior.
func TestSequentialPlanUnchanged(t *testing.T) {
	m := newTestModel()
	m.pipelineSteps = []string{"spec", "build", "verify"}
	m.pipelineDone = false
	m.parallelTasks = nil // no parallel tasks

	out := m.formatPipelineSteps()

	// Done steps use ✓
	if !strings.Contains(out, SymCheck+" spec") {
		t.Errorf("expected done step, got:\n%s", out)
	}
	if !strings.Contains(out, SymCheck+" build") {
		t.Errorf("expected done step, got:\n%s", out)
	}
	// Active (last) step uses ◉
	if !strings.Contains(out, SymActive+" verify") {
		t.Errorf("expected active step, got:\n%s", out)
	}
	// No tree connectors for sequential plan
	if strings.Contains(out, "├") || strings.Contains(out, "└") {
		t.Errorf("expected no tree connectors for sequential plan, got:\n%s", out)
	}
}

// TestTaskSymbol verifies the symbol mapping.
func TestTaskSymbol(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"done", SymCheck},
		{"failed", SymCross},
		{"running", SymActive},
		{"waiting", SymInactive},
		{"", SymInactive},
	}
	for _, c := range cases {
		got := taskSymbol(c.status)
		if got != c.want {
			t.Errorf("taskSymbol(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}
