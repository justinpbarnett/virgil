package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// Input is a bubbletea sub-model for text input with history.
type Input struct {
	textarea      textarea.Model
	history       []string
	historyCursor int
	maxHistory    int
	theme         *Theme
}

// NewInput creates an Input wired to the given theme.
func NewInput(theme *Theme) Input {
	ta := textarea.New()
	ta.Prompt = SymPrompt + " "
	ta.CharLimit = 0
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.FocusedStyle.Prompt = theme.Prompt
	ta.Focus()

	return Input{
		textarea:      ta,
		historyCursor: -1,
		maxHistory:    100,
		theme:         theme,
	}
}

// Submit returns the trimmed textarea value, adds it to history, and resets
// the input. Returns "" if the value is blank.
func (i *Input) Submit() string {
	v := strings.TrimSpace(i.textarea.Value())
	if v == "" {
		return ""
	}

	i.history = append(i.history, v)
	if len(i.history) > i.maxHistory {
		i.history = i.history[len(i.history)-i.maxHistory:]
	}

	i.historyCursor = -1
	i.textarea.SetValue("")
	i.textarea.SetHeight(1)
	return v
}

// HistoryUp moves the history cursor toward older entries.
func (i *Input) HistoryUp() {
	if len(i.history) == 0 {
		return
	}
	i.historyCursor++
	if i.historyCursor > len(i.history)-1 {
		i.historyCursor = len(i.history) - 1
	}
	i.textarea.SetValue(i.history[len(i.history)-1-i.historyCursor])
}

// HistoryDown moves the history cursor toward newer entries.
func (i *Input) HistoryDown() {
	i.historyCursor--
	if i.historyCursor < 0 {
		i.historyCursor = -1
		i.textarea.SetValue("")
		return
	}
	i.textarea.SetValue(i.history[len(i.history)-1-i.historyCursor])
}

// Value returns the current textarea content.
func (i *Input) Value() string {
	return i.textarea.Value()
}

// SetWidth sets the textarea width.
func (i *Input) SetWidth(w int) {
	i.textarea.SetWidth(w)
}

// Update delegates to the underlying textarea and returns the updated Input.
func (i Input) Update(msg tea.Msg) (Input, tea.Cmd) {
	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	return i, cmd
}

// View renders the textarea.
func (i *Input) View() string {
	return i.textarea.View()
}

// Focus sets focus on the textarea.
func (i *Input) Focus() tea.Cmd {
	return i.textarea.Focus()
}

// Blur removes focus from the textarea.
func (i *Input) Blur() {
	i.textarea.Blur()
}
