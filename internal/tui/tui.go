package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/sse"
	"github.com/justinpbarnett/virgil/internal/version"
	"github.com/justinpbarnett/virgil/internal/voice"
)

// Layout mode based on terminal width.
type layoutMode int

const (
	layoutFull   layoutMode = iota // 120+ cols, panel side-by-side
	layoutMedium                   // 80-119, compressed panel
	layoutNarrow                   // <80, panel as overlay
)

const (
	escapeWindow  = 400 * time.Millisecond
	minPanelWidth = 30
	panelFraction = 3 // panel gets 1/panelFraction of width
	minWidthWarn  = 60
	minHeightWarn = 15
)

type model struct {
	stream    Stream
	input     Input
	panel     Panel
	theme     Theme
	cmds      *CommandRegistry
	completer *Completer

	serverAddr string
	width      int
	height     int
	layout     layoutMode
	ghost      string // autocomplete ghost text
	keysShown  bool   // ctrl+k debounce

	// Streaming state
	pending        strings.Builder // response chunks
	ackBuf         strings.Builder // ack chunks (separate from response)
	waiting        bool
	dotPhase       int
	activeStreamID int
	cancelFn       context.CancelFunc
	lastEscTime    time.Time

	// Reconnection state
	connected    bool
	reconnecting bool
	inputQueue   []string

	// Pipeline step tracking for panel display
	pipelineSteps []string
	pipelineTools []string // tool calls within the current (last) step
	pipelineDone  bool

	// Parallel task tracking (nil when no parallel execution active)
	parallelTasks []*parallelTask
	panelSelected int // cursor within parallel task list (-1 = none)
	panelExpanded int // index of expanded task (-1 = none)

	// Session cost accumulator — reset on :clear and app start
	sessionCost float64

	// Voice status
	voiceRecording    bool
	voiceMode         config.VoiceOutputMode // transient — cleared after 3s (display only)
	voiceModeGen      int
	currentVoiceMode  config.VoiceOutputMode // persists — used for TTS decisions
	currentVoiceModel string                 // persists — model override for voice signals
	voiceStreamID     int                    // activeStreamID of the voice-initiated stream
}

type tickMsg struct{}

type streamChunkMsg struct {
	text     string
	isAck    bool
	streamID int
	reader   *sse.Reader
}

type streamDoneMsg struct {
	env      envelope.Envelope
	streamID int
	err      error
}

type reconnectMsg struct {
	ok  bool
	err error
}

type streamStepMsg struct {
	pipe     string
	streamID int
	reader   *sse.Reader
}

type streamToolMsg struct {
	name     string
	summary  string
	streamID int
	reader   *sse.Reader
}

type streamRouteMsg struct {
	pipe     string
	streamID int
	reader   *sse.Reader
}

type goalProgressMsg struct {
	event    string
	status   string
	summary  string
	streamID int
	reader   *sse.Reader
}

type pipesLoadedMsg struct {
	defs []pipe.Definition
}

// parallelTask tracks a single task within a parallel pipeline step.
type parallelTask struct {
	ID        string
	Name      string
	Pipe      string
	Status    string // "waiting", "running", "done", "failed"
	Activity  string // last tool/activity description
	Duration  string // set on completion, e.g. "0.3s"
	Error     string // non-empty on failure
	Output    strings.Builder
	DependsOn []string
}

type taskStatusMsg struct {
	ts struct {
		TaskID    string   `json:"task_id"`
		Name      string   `json:"name"`
		Pipe      string   `json:"pipe"`
		Status    string   `json:"status"`
		Activity  string   `json:"activity"`
		DependsOn []string `json:"depends_on"`
	}
	streamID int
	reader   *sse.Reader
}

type taskChunkMsg struct {
	taskID   string
	text     string
	streamID int
	reader   *sse.Reader
}

type taskDoneMsg struct {
	td struct {
		TaskID   string `json:"task_id"`
		Status   string `json:"status"`
		Duration string `json:"duration"`
		Error    string `json:"error"`
	}
	streamID int
	reader   *sse.Reader
}

func RunSession(serverAddr string) error {
	theme := NewTheme("dark")
	cmds := NewCommandRegistry()
	comp := NewCompleter(cmds.List())
	m := model{
		stream:        NewStream(&theme),
		input:         NewInput(&theme),
		panel:         NewPanel(&theme),
		theme:         theme,
		cmds:          cmds,
		completer:     comp,
		serverAddr:    serverAddr,
		connected:     true,
		panelSelected: -1,
		panelExpanded: -1,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.input.Focus(),
		m.loadPipes(),
		connectVoiceStatus(m.serverAddr),
		connectVoiceInput(m.serverAddr),
	)
}

func (m model) loadPipes() tea.Cmd {
	return func() tea.Msg {
		return pipesLoadedMsg{defs: FetchPipes(m.serverAddr)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		if m.waiting {
			m.dotPhase = (m.dotPhase + 1) % 4
			return m, tickCmd()
		}
		return m, nil

	case streamChunkMsg:
		return m.handleStreamChunk(msg)

	case streamDoneMsg:
		return m.handleStreamDone(msg)

	case reconnectMsg:
		cmds := []tea.Cmd{}
		mdl, cmd := m.handleReconnect(msg)
		m = mdl.(model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if msg.ok {
			cmds = append(cmds, connectVoiceStatus(m.serverAddr))
		}
		return m, tea.Batch(cmds...)

	case voiceStatusMsg:
		m.voiceRecording = msg.Recording
		if msg.Model != "" {
			m.currentVoiceModel = msg.Model
		}
		var cmds []tea.Cmd
		mode := config.VoiceOutputMode(msg.Mode)
		if mode != "" && mode != m.currentVoiceMode {
			m.currentVoiceMode = mode
			m.voiceMode = mode
			m.voiceModeGen++
			gen := m.voiceModeGen
			cmds = append(cmds, tea.Tick(3*time.Second, func(_ time.Time) tea.Msg {
				return voiceModeExpiredMsg{generation: gen}
			}))
		} else if mode != "" {
			m.currentVoiceMode = mode
		}
		cmds = append(cmds, readNextVoiceEventCmd(msg.reader))
		return m, tea.Batch(cmds...)

	case voiceModeExpiredMsg:
		if msg.generation == m.voiceModeGen {
			m.voiceMode = ""
		}

		return m, nil

	case voiceReconnectMsg:
		m.currentVoiceMode = ""
		m.voiceRecording = false
		return m, connectVoiceStatus(m.serverAddr)

	case voiceInputMsg:
		m.stream.Append(KindInput, SymPrompt+" "+msg.Text)
		m.waiting = true
		m.pending.Reset()
		m.ackBuf.Reset()
		m.dotPhase = 0
		m.activeStreamID++
		m.voiceStreamID = m.activeStreamID
		streamID := m.activeStreamID
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelFn = cancel
		return m, tea.Batch(
			startStream(ctx, m.serverAddr, msg.Text, streamID, m.currentVoiceModel),
			tickCmd(),
			readNextVoiceInputCmd(msg.reader),
		)

	case voiceInputReconnectMsg:
		return m, connectVoiceInput(m.serverAddr)

	case streamRouteMsg:
		if msg.streamID == m.activeStreamID {
			m.pipelineSteps = []string{msg.pipe}
			m.pipelineTools = nil
			m.pipelineDone = false
			m.showPipelinePanel()
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case streamToolMsg:
		if msg.streamID == m.activeStreamID {
			label := toolLabel(msg.name, msg.summary)
			m.pipelineTools = append(m.pipelineTools, label)
			m.showPipelinePanel()
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case streamStepMsg:
		if msg.streamID != m.activeStreamID {
			return m, readNextEvent(msg.reader, msg.streamID)
		}
		m.pipelineSteps = append(m.pipelineSteps, msg.pipe)
		m.pipelineTools = nil
		m.showPipelinePanel()
		var cmds []tea.Cmd
		if m.currentVoiceMode == config.VoiceModeSteps || m.currentVoiceMode == config.VoiceModeFull {
			ann := voice.StepAnnouncement(msg.pipe)
			cmds = append(cmds, postVoiceSpeak(m.serverAddr, ann, envelope.VoicePriorityAnnouncement))
		}
		cmds = append(cmds, readNextEvent(msg.reader, msg.streamID))
		return m, tea.Batch(cmds...)

	case taskStatusMsg:
		if msg.streamID == m.activeStreamID {
			m.upsertParallelTask(msg.ts.TaskID, msg.ts.Name, msg.ts.Pipe, msg.ts.Status, msg.ts.Activity, msg.ts.DependsOn)
			m.showPipelinePanel()
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case taskChunkMsg:
		if msg.streamID == m.activeStreamID {
			m.appendTaskOutput(msg.taskID, msg.text)
			if m.panelExpanded >= 0 && m.panelExpanded < len(m.parallelTasks) && m.parallelTasks[m.panelExpanded].ID == msg.taskID {
				m.showPipelinePanel()
			}
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case taskDoneMsg:
		if msg.streamID == m.activeStreamID {
			m.completeParallelTask(msg.td.TaskID, msg.td.Status, msg.td.Duration, msg.td.Error)
			m.showPipelinePanel()
			if msg.td.Status == "failed" {
				label := m.taskLabel(msg.td.TaskID)
				m.stream.Append(KindNotification, SymArrow+" "+label+": failed")
			}
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case goalProgressMsg:
		if msg.streamID == m.activeStreamID {
			label := "goal " + msg.event
			if msg.summary != "" {
				label += ": " + msg.summary
			}
			m.stream.Append(KindNotification, SymArrow+" "+label)
		}
		return m, readNextEvent(msg.reader, msg.streamID)

	case pipesLoadedMsg:
		m.completer.SetPipes(msg.defs)
		return m, nil

	case serverCmdMsg:
		if msg.err != nil {
			m.stream.Append(KindError, fmt.Sprintf("error: %v", msg.err))
		} else {
			m.stream.Append(KindNotification, msg.output)
		}
		return m, nil
	}

	// Delegate to sub-models
	var cmds []tea.Cmd
	var cmd tea.Cmd

	m.stream, cmd = m.stream.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	m.input, cmd = m.input.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	m.panel, cmd = m.panel.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	switch {
	case m.width >= 120:
		m.layout = layoutFull
	case m.width >= 80:
		m.layout = layoutMedium
	default:
		m.layout = layoutNarrow
	}

	m.updateLayout()
	return m, nil
}

// mainColumnWidth returns the width available for the stream and input areas,
// accounting for an open side-by-side panel.
func (m *model) mainColumnWidth() int {
	if m.panel.IsOpen() && m.layout != layoutNarrow {
		panelWidth := m.width / panelFraction
		if panelWidth < minPanelWidth {
			panelWidth = minPanelWidth
		}
		w := m.width - panelWidth - 1 // -1 for border
		if w < 20 {
			w = 20
		}
		return w
	}
	return m.width
}

func (m *model) updateLayout() {
	streamWidth := m.mainColumnWidth()

	// Set input width before measuring its rendered height.
	m.input.SetWidth(streamWidth)

	// Compute stream height: total minus separator (1 line) minus rendered input.
	inputViewHeight := lipgloss.Height(m.input.View())
	if inputViewHeight < 1 {
		inputViewHeight = 1
	}
	streamHeight := m.height - 1 - inputViewHeight // 1 for separator
	if streamHeight < 1 {
		streamHeight = 1
	}

	m.stream.SetSize(streamWidth, streamHeight)
	if m.panel.IsOpen() && m.layout != layoutNarrow {
		panelWidth := m.width - streamWidth - 1
		m.panel.SetSize(panelWidth, streamHeight)
	} else {
		m.panel.SetSize(m.width, streamHeight)
	}
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Reset escape timer on non-escape keys
	if msg.Type != tea.KeyEsc {
		m.lastEscTime = time.Time{}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		v := m.input.Value()
		if strings.TrimSpace(v) != "" {
			m.input.textarea.SetValue("")
			if m.voiceStreamID != 0 {
				return m, postVoiceStop(m.serverAddr)
			}
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		m.stream.ScrollDown(m.stream.PageSize() / 2)
		return m, nil

	case tea.KeyCtrlU:
		m.stream.ScrollUp(m.stream.PageSize() / 2)
		return m, nil

	case tea.KeyEsc:
		// Collapse expanded task output first (before cancel handling).
		if m.panelExpanded >= 0 {
			m.panelExpanded = -1
			m.showPipelinePanel()
			return m, nil
		}
		if m.cancelFn != nil {
			now := time.Now()
			if m.lastEscTime.IsZero() || now.Sub(m.lastEscTime) > escapeWindow {
				m.lastEscTime = now
				return m, nil
			}
			m.cancelFn()
			m.activeStreamID++
			m.waiting = false
			m.pending.Reset()
			m.cancelFn = nil
			m.lastEscTime = time.Time{}
			m.stream.Append(KindNotification, SymPrompt+" [cancelled]")
			wasVoice := m.voiceStreamID != 0
			m.voiceStreamID = 0
			if wasVoice {
				return m, postVoiceStop(m.serverAddr)
			}
			return m, nil
		}
		// Clear input when idle
		m.input.textarea.SetValue("")
		return m, nil

	case tea.KeyTab:
		text := strings.TrimSpace(m.input.Value())
		if text != "" {
			completed, ghost := m.completer.Complete(text)
			if ghost != "" {
				m.ghost = ghost
				m.input.textarea.SetValue(completed)
			}
		}
		return m, nil

	case tea.KeyUp:
		m.input.HistoryUp()
		return m, nil

	case tea.KeyDown:
		m.input.HistoryDown()
		return m, nil

	case tea.KeyPgUp:
		m.stream.ScrollUp(m.stream.PageSize() / 2)
		return m, nil

	case tea.KeyPgDown:
		m.stream.ScrollDown(m.stream.PageSize() / 2)
		return m, nil

	case tea.KeyEnter:
		if msg.Alt {
			// Alt+Enter (or Shift+Enter in kitty-protocol terminals) → newline
			m.input.textarea.InsertRune('\n')
			m.updateLayout()
			return m, nil
		}
		// Expand/collapse a selected parallel task in the panel.
		if m.panel.IsOpen() && m.panelSelected >= 0 && len(m.parallelTasks) > 0 {
			if m.panelExpanded == m.panelSelected {
				m.panelExpanded = -1
			} else {
				m.panelExpanded = m.panelSelected
			}
			m.showPipelinePanel()
			return m, nil
		}
		return m.handleSubmit()

	case tea.KeyCtrlJ:
		if m.panel.IsOpen() && len(m.parallelTasks) > 0 {
			// Move selection down through the task list.
			if m.panelSelected < len(m.parallelTasks)-1 {
				m.panelSelected++
			}
			m.showPipelinePanel()
			return m, nil
		}
		if m.panel.IsOpen() {
			m.panel.ScrollDown(3)
			return m, nil
		}

	case tea.KeyCtrlK:
		if m.panel.IsOpen() && len(m.parallelTasks) > 0 {
			// Move selection up through the task list.
			if m.panelSelected > 0 {
				m.panelSelected--
			} else if m.panelSelected == 0 {
				m.panelSelected = -1
			}
			m.showPipelinePanel()
			return m, nil
		}
		if !m.keysShown {
			m.stream.Append(KindNotification, keybindingSummary())
			m.keysShown = true
		}
		return m, nil

	case tea.KeyCtrlP:
		m.panel.Toggle()
		m.updateLayout()
		return m, nil

	case tea.KeyCtrlV:
		return m, postVoiceCycle(m.serverAddr)
	}

	// Any other key dismisses ghost text and resets completer
	m.ghost = ""
	m.completer.Reset()

	// Forward to textarea for typing
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateLayout() // recalculate if textarea height changed (no-op if dimensions unchanged)
	return m, cmd
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	value := m.input.Submit()
	if value == "" {
		return m, nil
	}

	// Check for colon commands
	if strings.HasPrefix(value, ":") {
		return m.handleCommand(value)
	}

	// Queue input if disconnected
	if !m.connected {
		m.inputQueue = append(m.inputQueue, value)
		m.stream.Append(KindNotification, "queued (reconnecting...)")
		return m, nil
	}

	return m.sendSignal(value)
}

type serverCmdMsg struct {
	output string
	err    error
}

func (m model) handleCommand(input string) (tea.Model, tea.Cmd) {
	m.keysShown = false
	name, _ := ParseCommand(input)

	// Server-fetching commands handled async
	switch name {
	case "pipes":
		return m, m.fetchPipes()
	case "status":
		return m, m.fetchStatus()
	}

	result, found := m.cmds.Execute(input)
	if !found {
		if name == "" {
			// Bare ":" — show command list
			allCmds := append(m.cmds.List(), "pipes", "status")
			result = CommandResult{Output: "commands: " + strings.Join(allCmds, ", ")}
		} else {
			m.stream.Append(KindError, "unknown command: :"+name)
			return m, nil
		}
	}

	if result.Quit {
		return m, tea.Quit
	}

	switch result.Output {
	case "panel":
		m.panel.Toggle()
		m.updateLayout()
	case "":
		// :clear — reset stream and session cost
		m.stream.Clear()
		m.sessionCost = 0
		if m.voiceStreamID != 0 {
			return m, postVoiceStop(m.serverAddr)
		}
		return m, nil
	default:
		m.stream.Append(KindNotification, result.Output)
	}

	return m, nil
}

func (m model) fetchPipes() tea.Cmd {
	return func() tea.Msg {
		defs := FetchPipes(m.serverAddr)
		if defs == nil {
			return serverCmdMsg{err: fmt.Errorf("failed to fetch pipes")}
		}
		var b strings.Builder
		b.WriteString("available pipes:\n")
		for _, d := range defs {
			b.WriteString(fmt.Sprintf("  %s — %s\n", d.Name, d.Description))
		}
		return serverCmdMsg{output: b.String()}
	}
}

func (m model) fetchStatus() tea.Cmd {
	return func() tea.Msg {
		resp, err := signalClient.Get(fmt.Sprintf("http://%s/status", m.serverAddr))
		if err != nil {
			return serverCmdMsg{err: err}
		}
		defer resp.Body.Close()
		var status map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			return serverCmdMsg{err: err}
		}
		var b strings.Builder
		b.WriteString(version.FullVersion() + "\n\n")
		b.WriteString("server status:\n")
		for k, v := range status {
			b.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
		return serverCmdMsg{output: b.String()}
	}
}

func (m model) sendSignal(text string) (tea.Model, tea.Cmd) {
	m.keysShown = false
	m.pipelineSteps = nil
	m.pipelineTools = nil
	m.pipelineDone = false
	m.clearParallelState()
	m.stream.Append(KindInput, SymPrompt+" "+text)
	m.waiting = true
	m.pending.Reset()
	m.ackBuf.Reset()
	m.dotPhase = 0
	m.activeStreamID++
	streamID := m.activeStreamID
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel
	cmds := []tea.Cmd{
		startStream(ctx, m.serverAddr, text, streamID, ""),
		tickCmd(),
	}
	if m.currentVoiceMode != "" && m.currentVoiceMode != config.VoiceModeSilent {
		cmds = append(cmds, postVoiceSpeak(m.serverAddr, voice.ThinkingPhrase(), envelope.VoicePriorityAnnouncement))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleStreamChunk(msg streamChunkMsg) (tea.Model, tea.Cmd) {
	if msg.streamID != m.activeStreamID {
		msg.reader.Close()
		return m, nil
	}
	if msg.isAck {
		m.ackBuf.WriteString(msg.text)
	} else {
		// First response chunk: commit ack to stream as its own message.
		if m.ackBuf.Len() > 0 {
			m.stream.Append(KindResponse, m.ackBuf.String())
			m.ackBuf.Reset()
		}
		m.pending.WriteString(msg.text)
		m.waiting = false
	}
	// Show ack while waiting for response, then show response.
	if m.pending.Len() > 0 {
		m.stream.SetPending(m.pending.String())
	} else {
		m.stream.SetPending(m.ackBuf.String())
	}
	return m, readNextEvent(msg.reader, msg.streamID)
}

func (m *model) formatPipelineSteps() string {
	var b strings.Builder
	for i, step := range m.pipelineSteps {
		isLast := i == len(m.pipelineSteps)-1
		if isLast && !m.pipelineDone {
			b.WriteString(SymActive + " " + step + "\n")
			// Render parallel tasks if active; otherwise render tool calls.
			if len(m.parallelTasks) > 0 {
				m.formatParallelTasks(&b)
			} else {
				for j, tool := range m.pipelineTools {
					if j == len(m.pipelineTools)-1 {
						b.WriteString("  " + SymActive + " " + tool + "\n")
					} else {
						b.WriteString("  " + SymCheck + " " + tool + "\n")
					}
				}
			}
		} else {
			b.WriteString(SymCheck + " " + step + "\n")
			if isLast {
				for _, tool := range m.pipelineTools {
					b.WriteString("  " + SymCheck + " " + tool + "\n")
				}
			}
		}
	}

	// When panelExpanded >= 0, append the expanded task's output below a rule.
	if m.panelExpanded >= 0 && m.panelExpanded < len(m.parallelTasks) {
		task := m.parallelTasks[m.panelExpanded]
		b.WriteString("\n" + SymSeparator + SymSeparator + " " + task.Name + " " + strings.Repeat(SymSeparator, 10) + "\n")
		b.WriteString(task.Output.String())
	}

	return b.String()
}

// formatParallelTasks writes the indented task tree with status symbols.
func (m *model) formatParallelTasks(b *strings.Builder) {
	n := len(m.parallelTasks)
	for i, task := range m.parallelTasks {
		connector := "├"
		if i == n-1 {
			connector = "└"
		}

		prefix := "  "
		if m.panelSelected == i {
			prefix = "▸ "
		}

		line := prefix + connector + " " + taskSymbol(task.Status) + " " + task.Name
		if task.Activity != "" {
			line += "   " + task.Activity
		} else if task.Duration != "" {
			line += "   " + task.Duration
		}

		b.WriteString(line + "\n")
	}
}

// taskSymbol maps a task status to the correct symbol from the TUI vocabulary.
func taskSymbol(status string) string {
	switch status {
	case "done":
		return SymCheck
	case "failed":
		return SymCross
	case "running":
		return SymActive
	default: // "waiting" and anything else
		return SymInactive
	}
}

// toolLabel builds a human-readable display string for a tool call.
func toolLabel(name, summary string) string {
	label := name
	switch name {
	case "read_file":
		label = "read"
	case "write_file":
		label = "write"
	case "edit_file":
		label = "edit"
	case "run_shell":
		label = "run"
	case "list_dir":
		label = "list"
	}
	if summary != "" {
		label += " " + summary
	}
	return label
}

func (m *model) showPipelinePanel() {
	title := "pipeline"
	if m.panelExpanded >= 0 && m.panelExpanded < len(m.parallelTasks) {
		title = m.parallelTasks[m.panelExpanded].Name
	}
	m.panel.SetContent(title, m.formatPipelineSteps())
	if !m.panel.IsOpen() && (len(m.pipelineSteps) > 1 || len(m.pipelineTools) > 0 || len(m.parallelTasks) > 0) {
		m.panel.Toggle()
		m.updateLayout()
	}
}

// findParallelTask returns the task with the given ID, or nil if not found.
func (m *model) findParallelTask(taskID string) *parallelTask {
	for _, t := range m.parallelTasks {
		if t.ID == taskID {
			return t
		}
	}
	return nil
}

// upsertParallelTask creates or updates a parallel task by ID.
func (m *model) upsertParallelTask(id, name, pipeName, status, activity string, dependsOn []string) {
	if t := m.findParallelTask(id); t != nil {
		if status != "" {
			t.Status = status
		}
		if activity != "" {
			t.Activity = activity
		}
		if name != "" {
			t.Name = name
		}
		if pipeName != "" {
			t.Pipe = pipeName
		}
		if dependsOn != nil {
			t.DependsOn = dependsOn
		}
		return
	}
	// New task — register it.
	m.parallelTasks = append(m.parallelTasks, &parallelTask{
		ID:        id,
		Name:      name,
		Pipe:      pipeName,
		Status:    status,
		Activity:  activity,
		DependsOn: dependsOn,
	})
}

// appendTaskOutput appends streaming text to a task's output buffer.
func (m *model) appendTaskOutput(taskID, text string) {
	if t := m.findParallelTask(taskID); t != nil {
		t.Output.WriteString(text)
	}
}

// completeParallelTask finalises a task with its terminal status and duration.
func (m *model) completeParallelTask(taskID, status, duration, errMsg string) {
	if t := m.findParallelTask(taskID); t != nil {
		t.Status = status
		t.Duration = duration
		t.Error = errMsg
		t.Activity = "" // clear live activity on completion
	}
}

// taskLabel returns a human-readable "name (pipe)" label for a task ID.
func (m *model) taskLabel(taskID string) string {
	if t := m.findParallelTask(taskID); t != nil {
		if t.Pipe != "" {
			return t.Name + " (" + t.Pipe + ")"
		}
		return t.Name
	}
	return taskID
}

// clearParallelState resets all parallel task tracking fields.
func (m *model) clearParallelState() {
	m.parallelTasks = nil
	m.panelSelected = -1
	m.panelExpanded = -1
}

func (m model) handleStreamDone(msg streamDoneMsg) (tea.Model, tea.Cmd) {
	if msg.streamID != m.activeStreamID {
		return m, nil
	}
	m.waiting = false
	m.cancelFn = nil
	m.lastEscTime = time.Time{}
	m.keysShown = false

	// Mark pipeline complete so the panel shows all checkmarks.
	if len(m.pipelineSteps) > 0 {
		m.pipelineDone = true
		m.showPipelinePanel()
	}
	// Clear parallel state — the pipeline is done.
	m.clearParallelState()

	if msg.err == nil && msg.env.Usage != nil {
		m.sessionCost += msg.env.Usage.Cost
	}

	pendingText := m.pending.String()
	ackText := m.ackBuf.String()
	var contentText string
	if msg.err == nil {
		contentText = envelope.ContentToText(msg.env.Content, msg.env.ContentType)
	}

	// Determine the response text: streamed chunks, or envelope content as fallback.
	responseText := pendingText
	if responseText == "" {
		responseText = contentText
	}

	var speakCmd tea.Cmd
	if msg.err == nil {
		speakCmd = m.speakResponse(responseText)
	}
	if msg.streamID == m.voiceStreamID {
		m.voiceStreamID = 0
	}

	m.stream.ClearPending()
	if msg.err != nil {
		m.stream.Append(KindError, fmt.Sprintf("error: %v", msg.err))
	} else {
		// Commit any remaining ack that wasn't flushed during streaming
		// (e.g. only ack chunks arrived, no response chunks).
		if ackText != "" {
			m.stream.Append(KindResponse, ackText)
		}
		if responseText != "" {
			m.stream.Append(KindResponse, responseText)
		} else if contentText != "" {
			m.stream.Append(KindResponse, contentText)
		}
	}
	m.pending.Reset()
	m.ackBuf.Reset()
	return m, speakCmd
}

const maxSpokenChars = 300

func (m model) speakResponse(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	var spoken string
	switch m.currentVoiceMode {
	case config.VoiceModeSilent, "":
		return nil
	case config.VoiceModeNotify, config.VoiceModeSteps:
		spoken = voice.NotifySummary(text, maxSpokenChars)
	case config.VoiceModeFull:
		spoken = voice.StripMarkdown(text)
	}
	if spoken == "" {
		return nil
	}
	return postVoiceSpeak(m.serverAddr, spoken, envelope.VoicePriorityResponse)
}

func (m model) handleReconnect(msg reconnectMsg) (tea.Model, tea.Cmd) {
	if msg.ok {
		m.connected = true
		m.reconnecting = false
		m.stream.Append(KindNotification, SymCheck+" Reconnected")

		// Flush queued inputs
		var cmds []tea.Cmd
		for _, queued := range m.inputQueue {
			m.stream.Append(KindInput, SymPrompt+" "+queued)
			m.activeStreamID++
			streamID := m.activeStreamID
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelFn = cancel
			m.waiting = true
			m.pending.Reset()
			cmds = append(cmds, startStream(ctx, m.serverAddr, queued, streamID, ""))
		}
		m.inputQueue = nil
		if len(cmds) > 0 {
			cmds = append(cmds, tickCmd())
		}
		return m, tea.Batch(cmds...)
	}

	// Still reconnecting
	return m, tryReconnect(m.serverAddr)
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}

	// Size warning
	if m.width < minWidthWarn || m.height < minHeightWarn {
		return m.theme.Error.Render(fmt.Sprintf(
			"Terminal too small (%dx%d). Minimum %dx%d.\nTry one-shot mode: virgil <signal>",
			m.width, m.height, minWidthWarn, minHeightWarn,
		))
	}

	// Build stream view with waiting indicator
	streamView := m.stream.View()

	sepWidth := m.mainColumnWidth()

	// Build separator — status indicators are embedded inline so height never changes.
	var statusParts []string
	if m.voiceRecording {
		statusParts = append(statusParts, m.theme.Active.Render(SymActive+" recording"))
	}
	if m.voiceMode != "" {
		statusParts = append(statusParts, m.theme.Notification.Render("voice: "+string(m.voiceMode)))
	}
	if m.waiting {
		dots := strings.Repeat(".", m.dotPhase)
		statusParts = append(statusParts, m.theme.Dim.Render("thinking"+dots))
	} else if !m.lastEscTime.IsZero() {
		statusParts = append(statusParts, m.theme.Notification.Render("press Esc again to cancel"))
	} else if !m.connected {
		statusParts = append(statusParts, m.theme.Error.Render(SymCross+" disconnected — reconnecting"+SymEllipsis))
	}

	titleStr := m.theme.Dim.Render("virgil")
	if m.sessionCost > 0 {
		titleStr += "  " + m.theme.Dim.Render(formatCost(m.sessionCost))
	}
	titleWidth := lipgloss.Width(titleStr)

	var sep string
	if len(statusParts) > 0 {
		statusText := strings.Join(statusParts, "  ")
		remaining := sepWidth - lipgloss.Width(statusText) - titleWidth - 2
		if remaining < 0 {
			remaining = 0
		}
		sep = statusText + " " + m.theme.Separator.Render(strings.Repeat(SymSeparator, remaining)) + " " + titleStr
	} else {
		remaining := sepWidth - titleWidth - 1
		if remaining < 0 {
			remaining = 0
		}
		sep = m.theme.Separator.Render(strings.Repeat(SymSeparator, remaining)) + " " + titleStr
	}

	inputView := m.input.View()
	if m.ghost != "" {
		inputView += m.theme.Dim.Render(m.ghost)
	}

	// Compose layout
	mainColumn := lipgloss.JoinVertical(lipgloss.Left, streamView, sep, inputView)

	// Panel
	if m.panel.IsOpen() {
		if m.layout == layoutNarrow {
			// Overlay: panel replaces stream
			return lipgloss.JoinVertical(lipgloss.Left, m.panel.View(), sep, inputView)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, mainColumn, m.panel.View())
	}

	return mainColumn
}

// formatCost formats a dollar amount for the separator line.
// Sub-cent amounts show as "<$0.01" to distinguish from free.
func formatCost(cost float64) string {
	if cost < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", cost)
}

func tickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

func startStream(ctx context.Context, addr, text string, streamID int, model string) tea.Cmd {
	return func() tea.Msg {
		reader, err := openSSEStream(ctx, addr, text, model)
		if err != nil {
			return streamDoneMsg{streamID: streamID, err: err}
		}
		return readNextEventSync(reader, streamID)
	}
}

func readNextEvent(reader *sse.Reader, streamID int) tea.Cmd {
	return func() tea.Msg {
		return readNextEventSync(reader, streamID)
	}
}

func readNextEventSync(reader *sse.Reader, streamID int) tea.Msg {
	for {
		event, err := reader.Next()
		if err != nil {
			reader.Close()
			return streamDoneMsg{streamID: streamID, err: err}
		}

		switch event.Type {
		case envelope.SSEEventChunk, envelope.SSEEventAck:
			var chunk struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				if event.Type == envelope.SSEEventAck {
					continue // skip malformed ack chunks
				}
				reader.Close()
				return streamDoneMsg{streamID: streamID, err: fmt.Errorf("invalid chunk: %w", err)}
			}
			return streamChunkMsg{text: chunk.Text, isAck: event.Type == envelope.SSEEventAck, streamID: streamID, reader: reader}

		case envelope.SSEEventDone:
			reader.Close()
			var env envelope.Envelope
			if err := json.Unmarshal([]byte(event.Data), &env); err != nil {
				return streamDoneMsg{streamID: streamID, err: fmt.Errorf("invalid done event: %w", err)}
			}
			if env.Error != nil {
				return streamDoneMsg{streamID: streamID, err: errors.New(env.Error.Message)}
			}
			return streamDoneMsg{env: env, streamID: streamID}

		case envelope.SSEEventRoute:
			var route struct {
				Pipe string `json:"pipe"`
			}
			_ = json.Unmarshal([]byte(event.Data), &route)
			return streamRouteMsg{pipe: route.Pipe, streamID: streamID, reader: reader}

		case envelope.SSEEventStep:
			var step struct {
				Pipe string `json:"pipe"`
			}
			if err := json.Unmarshal([]byte(event.Data), &step); err == nil {
				return streamStepMsg{pipe: step.Pipe, streamID: streamID, reader: reader}
			}
			continue

		case envelope.SSEEventTool:
			var tool struct {
				Name    string `json:"name"`
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal([]byte(event.Data), &tool); err == nil {
				return streamToolMsg{name: tool.Name, summary: tool.Summary, streamID: streamID, reader: reader}
			}
			continue

		case envelope.SSEEventTaskStatus:
			var msg taskStatusMsg
			if err := json.Unmarshal([]byte(event.Data), &msg.ts); err == nil {
				msg.streamID = streamID
				msg.reader = reader
				return msg
			}
			continue

		case envelope.SSEEventTaskChunk:
			var tc struct {
				TaskID string `json:"task_id"`
				Text   string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Data), &tc); err == nil {
				return taskChunkMsg{taskID: tc.TaskID, text: tc.Text, streamID: streamID, reader: reader}
			}
			continue

		case envelope.SSEEventTaskDone:
			var msg taskDoneMsg
			if err := json.Unmarshal([]byte(event.Data), &msg.td); err == nil {
				msg.streamID = streamID
				msg.reader = reader
				return msg
			}
			continue

		case envelope.SSEEventGoalProgress:
			var gp struct {
				Event   string `json:"event"`
				Status  string `json:"status"`
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal([]byte(event.Data), &gp); err == nil {
				return goalProgressMsg{
					event:    gp.Event,
					status:   gp.Status,
					summary:  gp.Summary,
					streamID: streamID,
					reader:   reader,
				}
			}
			continue

		default:
			continue
		}
	}
}

func keybindingSummary() string {
	return "" +
		"enter send   alt+enter newline   esc clear   ctrl+c clear/exit\n" +
		"↑/↓ history   tab complete   ctrl+p panel\n" +
		"ctrl+u half-page up   ctrl+d half-page down   pgup/pgdn scroll\n" +
		":help commands   :clear stream   :log server log   :quit exit\n" +
		"voice: right-option push-to-talk   ctrl+v cycle mode"
}

func tryReconnect(serverAddr string) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(time.Second)
		resp, err := signalClient.Get(fmt.Sprintf("http://%s/health", serverAddr))
		if err != nil {
			return reconnectMsg{ok: false, err: err}
		}
		resp.Body.Close()
		return reconnectMsg{ok: true}
	}
}
