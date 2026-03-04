package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/reflow/wordwrap"
)

// StreamEntryKind classifies an entry in the message stream.
type StreamEntryKind string

const (
	KindInput        StreamEntryKind = "input"
	KindResponse     StreamEntryKind = "response"
	KindNotification StreamEntryKind = "notification"
	KindError        StreamEntryKind = "error"
)

// StreamEntry is a single styled block in the scrollable stream.
type StreamEntry struct {
	Kind StreamEntryKind
	Text string
}

// Stream is a bubbletea sub-model that renders a scrollable viewport of
// styled message entries.
type Stream struct {
	viewport  viewport.Model
	entries   []StreamEntry
	theme     *Theme
	renderer  *glamour.TermRenderer
	maxBuffer int
	atBottom  bool
}

// NewStream returns an initialised Stream bound to the given theme.
func NewStream(theme *Theme) Stream {
	vp := viewport.New(0, 0)
	return Stream{
		viewport:  vp,
		theme:     theme,
		maxBuffer: 5000,
		atBottom:  true,
	}
}

// Append adds an entry to the stream. If the buffer exceeds maxBuffer the
// oldest entries are discarded. When the user was already scrolled to the
// bottom the viewport auto-scrolls to show the new content.
func (s *Stream) Append(kind StreamEntryKind, text string) {
	s.entries = append(s.entries, StreamEntry{Kind: kind, Text: text})
	if len(s.entries) > s.maxBuffer {
		s.entries = s.entries[len(s.entries)-s.maxBuffer:]
	}
	wasAtBottom := s.atBottom
	s.render()
	if wasAtBottom {
		s.viewport.GotoBottom()
	}
}

// SetSize updates the viewport dimensions.
func (s *Stream) SetSize(width, height int) {
	s.viewport.Width = width
	s.viewport.Height = height
	if width > 0 {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err == nil {
			s.renderer = r
		}
	}
	s.render()
}

// Update delegates input handling to the embedded viewport and tracks whether
// the user is scrolled to the bottom.
func (s Stream) Update(msg tea.Msg) (Stream, tea.Cmd) {
	var cmd tea.Cmd
	s.viewport, cmd = s.viewport.Update(msg)
	s.atBottom = s.viewport.AtBottom()
	return s, cmd
}

// View returns the rendered viewport content.
func (s Stream) View() string {
	return s.viewport.View()
}

// Clear removes all entries and resets the viewport.
func (s *Stream) Clear() {
	s.entries = nil
	s.render()
}

// render rebuilds the full viewport content from entries, styling each one
// according to its kind.
func (s *Stream) render() {
	var b strings.Builder
	for i, e := range s.entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		var styled string
		w := s.viewport.Width
		switch e.Kind {
		case KindInput:
			styled = s.theme.UserInput.Render(wordwrap.String(e.Text, w))
		case KindResponse:
			if s.renderer != nil {
				if rendered, err := s.renderer.Render(e.Text); err == nil {
					styled = strings.TrimSpace(rendered)
				} else {
					styled = s.theme.Response.Render(wordwrap.String(e.Text, w))
				}
			} else {
				styled = s.theme.Response.Render(wordwrap.String(e.Text, w))
			}
		case KindNotification:
			styled = s.theme.Notification.Render(wordwrap.String(e.Text, w))
		case KindError:
			styled = s.theme.Error.Render(wordwrap.String(e.Text, w))
		default:
			styled = wordwrap.String(e.Text, w)
		}
		b.WriteString(styled)
	}
	s.viewport.SetContent(b.String())
}
