package tui

import "github.com/charmbracelet/lipgloss"

// Symbol vocabulary used throughout the TUI.
const (
	SymPrompt     = "❯"
	SymArrow      = "▸"
	SymCheck      = "✓"
	SymCross      = "✗"
	SymActive     = "◉"
	SymInactive   = "○"
	SymReload     = "⟳"
	SymUndo       = "↺"
	SymEllipsis   = "…"
	SymSeparator  = "─"
	SymBullet     = "·"
)

// Theme holds all lipgloss styles for the TUI.
type Theme struct {
	// Text styles
	UserInput    lipgloss.Style
	Response     lipgloss.Style
	Notification lipgloss.Style
	Error        lipgloss.Style
	Success      lipgloss.Style
	Active       lipgloss.Style

	// UI chrome
	Prompt    lipgloss.Style
	Separator lipgloss.Style
	Dim       lipgloss.Style

	// Panel
	PanelBorder lipgloss.Style
	PanelTitle  lipgloss.Style
}

// NewTheme returns a themed style set. Supported modes: "dark", "light".
// Any other value (including "auto") defaults to dark.
func NewTheme(mode string) Theme {
	if mode == "light" {
		return lightTheme()
	}
	return darkTheme()
}

func darkTheme() Theme {
	return Theme{
		UserInput:    lipgloss.NewStyle().Foreground(lipgloss.Color("15")),  // white
		Response:     lipgloss.NewStyle().Foreground(lipgloss.Color("250")), // soft gray
		Notification: lipgloss.NewStyle().Foreground(lipgloss.Color("242")), // dim gray
		Error:        lipgloss.NewStyle().Foreground(lipgloss.Color("167")), // muted red
		Success:      lipgloss.NewStyle().Foreground(lipgloss.Color("108")), // muted green
		Active:       lipgloss.NewStyle().Foreground(lipgloss.Color("110")), // muted blue
		Prompt:       lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true),
		Separator:    lipgloss.NewStyle().Foreground(lipgloss.Color("236")),
		Dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		PanelBorder:  lipgloss.NewStyle().Foreground(lipgloss.Color("236")),
		PanelTitle:   lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true),
	}
}

func lightTheme() Theme {
	return Theme{
		UserInput:    lipgloss.NewStyle().Foreground(lipgloss.Color("0")),   // black
		Response:     lipgloss.NewStyle().Foreground(lipgloss.Color("238")), // dark gray
		Notification: lipgloss.NewStyle().Foreground(lipgloss.Color("245")), // mid gray
		Error:        lipgloss.NewStyle().Foreground(lipgloss.Color("124")), // dark red
		Success:      lipgloss.NewStyle().Foreground(lipgloss.Color("28")),  // dark green
		Active:       lipgloss.NewStyle().Foreground(lipgloss.Color("25")),  // dark blue
		Prompt:       lipgloss.NewStyle().Foreground(lipgloss.Color("25")).Bold(true),
		Separator:    lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("248")),
		PanelBorder:  lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		PanelTitle:   lipgloss.NewStyle().Foreground(lipgloss.Color("25")).Bold(true),
	}
}
