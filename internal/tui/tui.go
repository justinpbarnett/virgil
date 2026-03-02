package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

type model struct {
	textInput      textinput.Model
	messages       []string
	serverAddr     string
	err            error
	pending        strings.Builder
	waiting        bool
	dotPhase       int
	activeStreamID int
	cancelFn       context.CancelFunc
	lastEscTime    time.Time
}

const (
	maxMessages  = 200
	escapeWindow = 400 * time.Millisecond
)

type tickMsg struct{}

type streamChunkMsg struct {
	text     string
	streamID int
	reader   *sseReader
}

type streamDoneMsg struct {
	env      envelope.Envelope
	streamID int
	err      error
}

func (m *model) appendMessage(msg string) {
	m.messages = append(m.messages, msg)
	if len(m.messages) > maxMessages {
		m.messages = m.messages[len(m.messages)-maxMessages:]
	}
}

func RunSession(serverAddr string) error {
	ti := textinput.New()
	ti.Placeholder = "Ask Virgil something..."
	ti.Focus()

	m := model{
		textInput:  ti,
		serverAddr: serverAddr,
	}

	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type != tea.KeyEsc {
			m.lastEscTime = time.Time{}
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if m.cancelFn == nil {
				break
			}
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
			m.appendMessage("virgil > [cancelled]")
			m.appendMessage("")
			return m, nil
		case tea.KeyEnter:
			input := m.textInput.Value()
			if strings.TrimSpace(input) == "" {
				return m, nil
			}
			m.appendMessage("you > " + input)
			m.textInput.SetValue("")
			m.waiting = true
			m.pending.Reset()
			m.dotPhase = 0
			m.activeStreamID++
			streamID := m.activeStreamID
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelFn = cancel
			return m, tea.Batch(
				startStream(ctx, m.serverAddr, input, streamID),
				tickCmd(),
			)
		}

	case tickMsg:
		if m.waiting {
			m.dotPhase = (m.dotPhase + 1) % 4
			return m, tickCmd()
		}
		return m, nil

	case streamChunkMsg:
		if msg.streamID != m.activeStreamID {
			msg.reader.Close()
			return m, nil
		}
		m.pending.WriteString(msg.text)
		m.waiting = false
		return m, readNextEvent(msg.reader, msg.streamID)

	case streamDoneMsg:
		if msg.streamID != m.activeStreamID {
			return m, nil
		}
		m.waiting = false
		m.cancelFn = nil
		m.lastEscTime = time.Time{}
		if msg.err != nil {
			m.appendMessage(fmt.Sprintf("error: %v", msg.err))
		} else if m.pending.Len() > 0 {
			m.appendMessage("virgil > " + m.pending.String())
		} else {
			content := envelope.ContentToText(msg.env.Content, msg.env.ContentType)
			if content != "" {
				m.appendMessage("virgil > " + content)
			}
		}
		m.appendMessage("")
		m.pending.Reset()
		return m, nil

	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) View() string {
	var b strings.Builder

	for _, msg := range m.messages {
		b.WriteString(msg)
		b.WriteString("\n")
	}

	if m.waiting {
		dots := strings.Repeat(".", m.dotPhase)
		b.WriteString("virgil > " + dots)
		b.WriteString("\n")
	} else if m.pending.Len() > 0 {
		b.WriteString("virgil > " + m.pending.String())
		b.WriteString("\n")
	}

	if !m.lastEscTime.IsZero() {
		b.WriteString("press Esc again to cancel\n")
	}

	b.WriteString(m.textInput.View())
	b.WriteString("\n")

	return b.String()
}

func tickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

func startStream(ctx context.Context, addr, text string, streamID int) tea.Cmd {
	return func() tea.Msg {
		reader, err := openSSEStream(ctx, addr, text)
		if err != nil {
			return streamDoneMsg{streamID: streamID, err: err}
		}
		return readNextEventSync(reader, streamID)
	}
}

func readNextEvent(reader *sseReader, streamID int) tea.Cmd {
	return func() tea.Msg {
		return readNextEventSync(reader, streamID)
	}
}

func readNextEventSync(reader *sseReader, streamID int) tea.Msg {
	event, err := reader.Next()
	if err != nil {
		reader.Close()
		return streamDoneMsg{streamID: streamID, err: err}
	}

	switch event.Type {
	case envelope.SSEEventChunk:
		var chunk struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
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

	default:
		reader.Close()
		return streamDoneMsg{streamID: streamID, err: fmt.Errorf("unknown event type: %s", event.Type)}
	}
}

