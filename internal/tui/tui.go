package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

type model struct {
	textInput  textinput.Model
	messages   []string
	serverAddr string
	err        error
}

type responseMsg struct {
	content string
	err     error
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
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			input := m.textInput.Value()
			if strings.TrimSpace(input) == "" {
				return m, nil
			}
			m.messages = append(m.messages, "you > "+input)
			m.textInput.SetValue("")
			return m, sendSignal(m.serverAddr, input)
		}

	case responseMsg:
		if msg.err != nil {
			m.messages = append(m.messages, fmt.Sprintf("error: %v", msg.err))
		} else {
			m.messages = append(m.messages, msg.content)
		}
		m.messages = append(m.messages, "")
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

	b.WriteString(m.textInput.View())
	b.WriteString("\n")

	return b.String()
}

func sendSignal(addr, text string) tea.Cmd {
	return func() tea.Msg {
		env, err := postSignal(addr, text)
		if err != nil {
			return responseMsg{err: err}
		}

		if env.Error != nil {
			return responseMsg{err: fmt.Errorf("%s: %s", env.Error.Severity, env.Error.Message)}
		}

		content := envelope.ContentToText(env.Content, env.ContentType)
		return responseMsg{content: content}
	}
}
