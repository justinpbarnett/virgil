package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/sse"
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
	escapeWindow   = 400 * time.Millisecond
	minPanelWidth  = 30
	panelFraction  = 3 // panel gets 1/panelFraction of width
	minWidthWarn   = 60
	minHeightWarn  = 15
)

type model struct {
	stream    Stream
	input     Input
	panel     Panel
	theme     Theme
	cmds      *CommandRegistry
	completer *Completer

	serverAddr     string
	width          int
	height         int
	layout         layoutMode
	ghost          string // autocomplete ghost text
	keysShown      bool   // ctrl+k debounce

	// Streaming state
	pending        strings.Builder
	waiting        bool
	dotPhase       int
	activeStreamID int
	cancelFn       context.CancelFunc
	lastEscTime    time.Time

	// Reconnection state
	connected      bool
	reconnecting   bool
	inputQueue     []string

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

type streamRouteMsg struct {
	streamID int
	reader   *sse.Reader
}

type pipesLoadedMsg struct{}

func RunSession(serverAddr string) error {
	theme := NewTheme("dark")
	cmds := NewCommandRegistry()
	comp := NewCompleter(cmds.List())
	m := model{
		stream:     NewStream(&theme),
		input:      NewInput(&theme),
		panel:      NewPanel(&theme),
		theme:      theme,
		cmds:       cmds,
		completer:  comp,
		serverAddr: serverAddr,
		connected:  true,
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
		m.completer.LoadPipes(m.serverAddr)
		return pipesLoadedMsg{}
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
		return m, readNextEvent(msg.reader, msg.streamID)

	case streamStepMsg:
		if msg.streamID != m.activeStreamID {
			return m, readNextEvent(msg.reader, msg.streamID)
		}
		var cmds []tea.Cmd
		if m.currentVoiceMode == config.VoiceModeSteps || m.currentVoiceMode == config.VoiceModeFull {
			ann := voice.StepAnnouncement(msg.pipe)
			cmds = append(cmds, postVoiceSpeak(m.serverAddr, ann, envelope.VoicePriorityAnnouncement))
		}
		cmds = append(cmds, readNextEvent(msg.reader, msg.streamID))
		return m, tea.Batch(cmds...)

	case pipesLoadedMsg:
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

func (m *model) updateLayout() {
	inputHeight := 3 // textarea height + some padding
	streamHeight := m.height - inputHeight
	if streamHeight < 1 {
		streamHeight = 1
	}

	if m.panel.IsOpen() && m.layout != layoutNarrow {
		panelWidth := m.width / panelFraction
		if panelWidth < minPanelWidth {
			panelWidth = minPanelWidth
		}
		streamWidth := m.width - panelWidth - 1 // -1 for border
		if streamWidth < 20 {
			streamWidth = 20
		}
		m.stream.SetSize(streamWidth, streamHeight)
		m.panel.SetSize(panelWidth, streamHeight)
		m.input.SetWidth(streamWidth)
	} else {
		m.stream.SetSize(m.width, streamHeight)
		m.panel.SetSize(m.width, streamHeight)
		m.input.SetWidth(m.width)
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
		return m, tea.Quit

	case tea.KeyEsc:
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
			if m.voiceStreamID != 0 {
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

	case tea.KeyEnter:
		if msg.Alt {
			// Alt+Enter (or Shift+Enter in kitty-protocol terminals) → newline
			m.input.textarea.InsertRune('\n')
			return m, nil
		}
		return m.handleSubmit()

	case tea.KeyCtrlJ:
		if m.panel.IsOpen() {
			m.panel.ScrollDown(3)
			return m, nil
		}

	case tea.KeyCtrlK:
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
		resp, err := signalClient.Get(fmt.Sprintf("http://%s/pipes", m.serverAddr))
		if err != nil {
			return serverCmdMsg{err: err}
		}
		defer resp.Body.Close()
		var defs []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Category    string `json:"category"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&defs); err != nil {
			return serverCmdMsg{err: err}
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
		b.WriteString("server status:\n")
		for k, v := range status {
			b.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
		return serverCmdMsg{output: b.String()}
	}
}

func (m model) sendSignal(text string) (tea.Model, tea.Cmd) {
	m.keysShown = false
	m.stream.Append(KindInput, SymPrompt+" "+text)
	m.waiting = true
	m.pending.Reset()
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
	m.pending.WriteString(msg.text)
	m.waiting = false
	return m, readNextEvent(msg.reader, msg.streamID)
}

func (m model) handleStreamDone(msg streamDoneMsg) (tea.Model, tea.Cmd) {
	if msg.streamID != m.activeStreamID {
		return m, nil
	}
	m.waiting = false
	m.cancelFn = nil
	m.lastEscTime = time.Time{}
	m.keysShown = false

	if msg.err == nil && msg.env.Usage != nil {
		m.sessionCost += msg.env.Usage.Cost
	}

	pendingText := m.pending.String()
	var contentText string
	if pendingText == "" && msg.err == nil {
		contentText = envelope.ContentToText(msg.env.Content, msg.env.ContentType)
	}

	var speakCmd tea.Cmd
	if msg.err == nil {
		responseText := pendingText
		if responseText == "" {
			responseText = contentText
		}
		speakCmd = m.speakResponse(responseText)
	}
	if msg.streamID == m.voiceStreamID {
		m.voiceStreamID = 0
	}

	if msg.err != nil {
		m.stream.Append(KindError, fmt.Sprintf("error: %v", msg.err))
	} else if pendingText != "" {
		m.stream.Append(KindResponse, pendingText)
	} else if contentText != "" {
		m.stream.Append(KindResponse, contentText)
	}
	m.pending.Reset()
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
		remaining := m.width - lipgloss.Width(statusText) - titleWidth - 2
		if remaining < 0 {
			remaining = 0
		}
		sep = statusText + " " + m.theme.Separator.Render(strings.Repeat(SymSeparator, remaining)) + " " + titleStr
	} else {
		remaining := m.width - titleWidth - 1
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
			return streamChunkMsg{text: chunk.Text, streamID: streamID, reader: reader}

		case envelope.SSEEventDone:
			reader.Close()
			var env envelope.Envelope
			if err := json.Unmarshal([]byte(event.Data), &env); err != nil {
				return streamDoneMsg{streamID: streamID, err: fmt.Errorf("invalid done event: %w", err)}
			}
			if env.Error != nil {
				return streamDoneMsg{streamID: streamID, err: fmt.Errorf("%s: %s", env.Error.Severity, env.Error.Message)}
			}
			return streamDoneMsg{env: env, streamID: streamID}

		case envelope.SSEEventRoute:
			return streamRouteMsg{streamID: streamID, reader: reader}

		case envelope.SSEEventStep:
			var step struct {
				Pipe string `json:"pipe"`
			}
			if err := json.Unmarshal([]byte(event.Data), &step); err == nil {
				return streamStepMsg{pipe: step.Pipe, streamID: streamID, reader: reader}
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
		"↑/↓ history   tab complete   ctrl+p panel   ctrl+d exit\n" +
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
