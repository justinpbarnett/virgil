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
	viewport    viewport.Model
	entries     []StreamEntry
	pending     string // live-updating text shown below entries while streaming
	entriesHTML string // cached render of entries (invalidated on Append/Clear/SetSize)
	theme       *Theme
	renderer    *glamour.TermRenderer
	maxBuffer   int
	atBottom    bool
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
	s.renderEntries()
	s.compose()
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
	s.renderEntries()
	s.compose()
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

// PageSize returns the number of visible lines in the stream viewport.
func (s *Stream) PageSize() int {
	return s.viewport.Height
}

// ScrollUp scrolls the viewport up by n lines.
func (s *Stream) ScrollUp(n int) {
	s.viewport.ScrollUp(n)
	s.atBottom = s.viewport.AtBottom()
}

// ScrollDown scrolls the viewport down by n lines.
func (s *Stream) ScrollDown(n int) {
	s.viewport.ScrollDown(n)
	s.atBottom = s.viewport.AtBottom()
}

// SetPending sets the live-updating text shown at the bottom of the stream
// while a response is being streamed. Only re-renders the pending suffix,
// not the full entry list.
func (s *Stream) SetPending(text string) {
	s.pending = text
	wasAtBottom := s.atBottom
	s.compose()
	if wasAtBottom {
		s.viewport.GotoBottom()
	}
}

// ClearPending removes the live-updating pending text.
func (s *Stream) ClearPending() {
	s.pending = ""
}

// Clear removes all entries and resets the viewport.
func (s *Stream) Clear() {
	s.entries = nil
	s.pending = ""
	s.entriesHTML = ""
	s.compose()
}

// renderEntries rebuilds the cached entry content. Called only when entries
// change (Append, Clear, SetSize), not on every pending text update.
func (s *Stream) renderEntries() {
	var b strings.Builder
	w := s.viewport.Width
	for i, e := range s.entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		var styled string
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
	s.entriesHTML = b.String()
}

// compose joins the cached entry content with the pending text and updates
// the viewport. This is cheap — just string concatenation.
func (s *Stream) compose() {
	if s.pending == "" {
		s.viewport.SetContent(s.entriesHTML)
		return
	}
	var b strings.Builder
	b.WriteString(s.entriesHTML)
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	w := s.viewport.Width
	b.WriteString(s.theme.Response.Render(wordwrap.String(s.pending, w)))
	s.viewport.SetContent(b.String())
}
