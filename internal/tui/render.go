package tui

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type role int

const (
	roleUser role = iota
	roleAssistant
	roleNotice
	roleError
	roleTool // a tool call card: running, then succeeded or failed
)

// previewLines is how many lines of a tool's output its card shows.
const previewLines = 3

// entry is one finished item in the transcript. rendered caches the styled text for the
// current width and theme, because markdown rendering is too slow to redo on every streamed token.
type entry struct {
	role     role
	text     string
	rendered string

	// Tool cards only: text holds "name arguments"; the rest is filled in when the call finishes.
	callID string
	done   bool
	failed bool
	output string
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
	case roleTool:
		return m.renderTool(e)
	}
	if m.markdown != nil {
		if out, err := m.markdown.Render(e.text); err == nil {
			return strings.Trim(out, "\n")
		}
	}
	return wrap(e.text, m.width)
}

// renderTool draws a one-line header and, once finished, a short preview of the output.
// Long lines are cut instead of wrapped so a card never takes more space than a few rows.
func (m *Model) renderTool(e entry) string {
	glyph, style := "…", noticeStyle
	switch {
	case e.done && e.failed:
		glyph, style = "✗", errorStyle
	case e.done:
		glyph = "✓"
	}
	lines := []string{style.Render(ansi.Truncate(glyph+" "+e.text, m.width, "…"))}

	if e.done {
		output := strings.Split(strings.TrimSpace(e.output), "\n")
		for _, line := range output[:min(len(output), previewLines)] {
			lines = append(lines, noticeStyle.Render(ansi.Truncate("  "+line, m.width, "…")))
		}
		if extra := len(output) - previewLines; extra > 0 {
			lines = append(lines, noticeStyle.Render(fmt.Sprintf("  … %d more lines", extra)))
		}
	}
	return strings.Join(lines, "\n")
}
