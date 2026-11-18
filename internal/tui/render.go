package tui

import (
	"slices"
	"strings"
	"time"

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
	roleTool // a tool call card: running, then succeeded, failed or cancelled
	roleInfo // the answer to a slash command; never part of the conversation the model sees
)

// Layout, in terminal cells.
const (
	margin       = 1   // empty columns on the left of everything
	gutter       = 2   // indent shared by assistant text, tool cards and notices
	maxTextWidth = 120 // wider terminals do not get longer lines: they are harder to read
)

// entry is one finished item in the transcript. rendered caches the styled text for the current width
// and theme, because markdown rendering is too slow to redo on every streamed token.
type entry struct {
	role     role
	text     string
	rows     [][2]string // roleInfo: label and text columns, instead of text
	rendered string

	// Tool cards only.
	callID    string
	name      string
	args      string // raw JSON arguments
	started   time.Time
	elapsed   time.Duration
	done      bool
	failed    bool
	cancelled bool
	output    string
}

// contentWidth is the width of the transcript and the other full-width parts, excluding the left margin.
func (m *Model) contentWidth() int {
	return max(20, min(m.width-margin, maxTextWidth))
}

// indent shifts every line of s to the right.
func indent(s string, n int) string {
	return lipgloss.NewStyle().PaddingLeft(n).Render(s)
}

// wrap word-wraps plain text to width.
func wrap(text string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(text)
}

// newMarkdownRenderer returns nil when glamour cannot be set up; callers then show plain text.
func newMarkdownRenderer(width int, t theme) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle(t)), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	return r
}

// render styles an entry for the current width and theme. Whatever the entry holds, no line is wider
// than the content area: wide code or unbreakable words are cut with an ellipsis, since a line that is
// too wide would wrap in the terminal and break the layout.
func (m *Model) render(e entry) string {
	// Everything shown passes through here, so this is where text from the model, tools and servers is cleaned.
	e.text, e.name, e.args, e.output = sanitize(e.text), sanitize(e.name), sanitize(e.args), sanitize(e.output)
	e.rows = slices.Clone(e.rows)
	for i, row := range e.rows {
		e.rows[i] = [2]string{sanitize(row[0]), sanitize(row[1])}
	}
	width := m.contentWidth()
	lines := strings.Split(m.renderEntry(e, width), "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines, "\n")
}

// renderEntry styles an entry. Assistant text is markdown; the rest is plain.
func (m *Model) renderEntry(e entry, width int) string {
	switch e.role {
	case roleUser:
		return m.sty.user.Width(width).Render(e.text)
	case roleNotice:
		return indent(m.sty.notice.Render(e.text), gutter)
	case roleError:
		return indent(m.sty.badge.Render("ERROR")+" "+m.sty.errorText.Render(ansi.Truncate(e.text, width-gutter-8, "…")), gutter)
	case roleTool:
		return m.renderTool(e)
	case roleInfo:
		return m.renderInfo(e, width)
	}
	return indent(m.markdownText(e.text, width-gutter), gutter)
}

// markdownText renders assistant text, or wraps it as plain text if markdown rendering is unavailable.
func (m *Model) markdownText(text string, width int) string {
	// Glamour pads lines as if a tab were zero cells wide, which makes code lines too wide. Expand tabs first.
	text = strings.ReplaceAll(text, "\t", "    ")
	if m.markdown != nil {
		if out, err := m.markdown.Render(text); err == nil {
			return trimBlankLines(out)
		}
	}
	return wrap(text, width)
}

// trimBlankLines drops empty or whitespace-only lines at both ends. Glamour pads its output with them, which
// would make the gap after some answers larger than after others.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(ansi.Strip(lines[start])) == "" {
		start++
	}
	for end > start && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// renderInfo draws a dim line, or rows in two aligned columns with long text wrapped under its column.
func (m *Model) renderInfo(e entry, width int) string {
	if len(e.rows) == 0 {
		return indent(m.sty.meta.Width(width-gutter).Render(e.text), gutter)
	}
	labelWidth := 0
	for _, row := range e.rows {
		labelWidth = max(labelWidth, lipgloss.Width(row[0]))
	}
	labelWidth = min(labelWidth, (width-gutter)/2)
	textWidth := width - gutter - labelWidth - 2
	lines := make([]string, len(e.rows))
	for i, row := range e.rows {
		lines[i] = lipgloss.JoinHorizontal(lipgloss.Top,
			m.sty.toolName.Width(labelWidth+2).Render(row[0]),
			m.sty.meta.Width(textWidth).Render(row[1]))
	}
	return indent(strings.Join(lines, "\n"), gutter)
}
