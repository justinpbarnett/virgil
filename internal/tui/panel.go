package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Panel is a bubbletea sub-model for a collapsible side panel.
type Panel struct {
	viewport viewport.Model
	open     bool
	theme    *Theme
	title    string
	content  string
}

// NewPanel creates a Panel with the given theme. The panel starts closed.
func NewPanel(theme *Theme) Panel {
	return Panel{
		viewport: viewport.New(0, 0),
		theme:    theme,
	}
}

// Toggle flips the panel between open and closed.
func (p *Panel) Toggle() {
	p.open = !p.open
}

// IsOpen reports whether the panel is open.
func (p *Panel) IsOpen() bool {
	return p.open
}

// SetContent sets the panel title and body, updating the viewport.
func (p *Panel) SetContent(title, content string) {
	p.title = title
	p.content = content
	p.viewport.SetContent(p.renderContent())
}

// SetSize updates the viewport dimensions.
func (p *Panel) SetSize(width, height int) {
	p.viewport.Width = width
	p.viewport.Height = height
	// Re-render content so it wraps to the new width.
	p.viewport.SetContent(p.renderContent())
}

// ScrollDown scrolls the viewport down by n lines.
func (p *Panel) ScrollDown(n int) {
	p.viewport.LineDown(n)
}

// ScrollUp scrolls the viewport up by n lines.
func (p *Panel) ScrollUp(n int) {
	p.viewport.LineUp(n)
}

// Update processes messages when the panel is open. If the panel is closed
// no update is performed.
func (p Panel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if !p.open {
		return p, nil
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

// View renders the panel. Returns an empty string when closed.
func (p Panel) View() string {
	if !p.open {
		return ""
	}

	titleLine := p.theme.PanelTitle.Render(p.title)
	separator := p.theme.Separator.Render(strings.Repeat(SymSeparator, p.viewport.Width))
	body := p.viewport.View()

	inner := lipgloss.JoinVertical(lipgloss.Left, titleLine, separator, body)

	bordered := p.theme.PanelBorder.
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Render(inner)

	return bordered
}

// renderContent produces the styled text placed inside the viewport.
func (p Panel) renderContent() string {
	if p.content == "" {
		return p.theme.Dim.Render("ctrl+p to close")
	}
	return p.content
}
