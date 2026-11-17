package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

type role int

const (
	roleUser role = iota
	roleAssistant
	roleNotice
	roleError
)

// entry is one finished item in the transcript. rendered caches the styled text for the
// current width and theme, because markdown rendering is too slow to redo on every streamed token.
type entry struct {
	role     role
	text     string
	rendered string
}

var (
	userStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	noticeStyle = lipgloss.NewStyle().Faint(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// wrap word-wraps plain text to width.
func wrap(text string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(text)
}

// newMarkdownRenderer returns nil when glamour cannot be set up; callers then show plain text.
func newMarkdownRenderer(width int, dark bool) *glamour.TermRenderer {
	style := "light"
	if dark {
		style = "dark"
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	return r
}

// render styles an entry for the given width. Assistant text is markdown; everything else is plain.
func (m *Model) render(e entry) string {
	switch e.role {
	case roleUser:
		return userStyle.Width(m.width).Render("› " + e.text)
	case roleNotice:
		return noticeStyle.Width(m.width).Render(e.text)
	case roleError:
		return errorStyle.Width(m.width).Render(e.text)
	}
	if m.markdown != nil {
		if out, err := m.markdown.Render(e.text); err == nil {
			return strings.Trim(out, "\n")
		}
	}
	return wrap(e.text, m.width)
}
