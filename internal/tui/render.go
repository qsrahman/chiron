package tui

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
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
	margin       = 1   // empty columns on the left and on the right of everything
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
	callID   string
	name     string
	title    string // shown instead of name when set
	args     string // raw JSON arguments
	started  time.Time
	elapsed  time.Duration
	done     bool
	outcome  agent.Outcome
	selected bool // the card chosen with Tab
	expanded bool // shows the whole output instead of the preview
	output   string
}

// contentWidth is the width of the transcript and the other full-width parts, excluding the left margin.
func (m *Model) contentWidth() int {
	return max(20, min(m.width-2*margin, maxTextWidth))
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

// markdownText renders assistant text: fenced code blocks as shaded bands, the rest as markdown, or as wrapped plain
// text if markdown rendering is unavailable.
func (m *Model) markdownText(text string, width int) string {
	// Glamour pads lines as if a tab were zero cells wide, which makes code lines too wide. Expand tabs first.
	text = strings.ReplaceAll(text, "\t", "    ")
	var parts []string
	for _, seg := range splitFences(text) {
		switch {
		case seg.code:
			parts = append(parts, strings.Join(m.codeBand(seg, width), "\n"))
		case strings.TrimSpace(seg.text) != "":
			parts = append(parts, m.prose(seg.text, width))
		}
	}
	return strings.Join(parts, "\n\n")
}

// prose renders markdown without fenced code blocks.
func (m *Model) prose(text string, width int) string {
	if m.markdown == nil {
		return wrap(text, width)
	}
	out, err := m.markdown.Render(keepHyphenated(text))
	if err != nil {
		return wrap(text, width)
	}
	// The hyphens go back last: the re-wrapping of list items must not break at them either.
	return trimBlankLines(strings.ReplaceAll(hangListItems(out, width), unbreakable, "-"))
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

// listMarker is the start of a list item as glamour draws it: indentation, then a bullet or a number.
var listMarker = regexp.MustCompile(`^( *)(• |[0-9]+\. )`)

// hangListItems moves the wrapped lines of a list item under its text. Glamour wraps an item at the full width
// and starts the continuation under the bullet, which reads as a new paragraph. A line continues the item above
// only if it is indented like it and its first word would not have fitted on that line: exactly the condition
// under which the wrapper broke it, so a code block or a line break inside an item is left alone.
func hangListItems(text string, width int) string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		plain := ansi.Strip(lines[i])
		marker := listMarker.FindString(plain)
		if marker == "" {
			out = append(out, lines[i])
			continue
		}
		lead, hang := len(marker)-len(strings.TrimLeft(marker, " ")), ansi.StringWidth(marker)
		item, last, merged := strings.TrimRight(lines[i], " "), plain, false
		for i+1 < len(lines) && continuesItem(last, ansi.Strip(lines[i+1]), lead, width) {
			i++
			item += " " + strings.TrimSpace(lines[i])
			last, merged = ansi.Strip(lines[i]), true
		}
		if !merged {
			out = append(out, lines[i])
			continue
		}
		head, body := ansi.Truncate(item, hang, ""), ansi.TruncateLeft(item, hang, "")
		for n, line := range strings.Split(ansi.Wrap(body, width-hang, ""), "\n") {
			if n == 0 {
				out = append(out, head+line)
			} else {
				out = append(out, strings.Repeat(" ", hang)+line)
			}
		}
	}
	return strings.Join(out, "\n")
}

// continuesItem reports whether next is the soft-wrapped rest of the list line prev (both without styling).
func continuesItem(prev, next string, lead, width int) bool {
	text := strings.TrimLeft(next, " ")
	if text == "" || len(next)-len(text) != lead || listMarker.MatchString(next) {
		return false
	}
	word, _, _ := strings.Cut(text, " ")
	return ansi.StringWidth(strings.TrimRight(prev, " "))+1+ansi.StringWidth(word) > width
}

// separator goes between two neighbouring entries: a blank line, except between tool cards, so that a run of calls
// reads as a list.
func separator(prev, next entry) string {
	if prev.role == roleTool && next.role == roleTool {
		return "\n"
	}
	return "\n\n"
}

// unbreakable stands in for a hyphen inside a word while the text is wrapped: the wrapper breaks lines after a hyphen,
// which splits a flag such as --config or a name such as read-only. It is a private-use character of the width of a
// hyphen, and it is turned back after wrapping, so the text on screen and the text copied are the original.
const unbreakable = "\ue000"

// keepHyphenated makes the hyphens inside words unbreakable. A word with no letter or digit (a list marker, a rule,
// the separator row of a table) is left alone because they mean something to markdown, and so are fenced code blocks,
// where a hyphen is an operator the highlighter needs to see.
func keepHyphenated(markdown string) string {
	if strings.Contains(markdown, unbreakable) {
		return markdown
	}
	lines := strings.Split(markdown, "\n")
	inFence := false
	for i, line := range lines {
		if fence := strings.TrimLeft(line, " "); strings.HasPrefix(fence, "```") || strings.HasPrefix(fence, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence {
			lines[i] = wordPattern.ReplaceAllStringFunc(line, func(word string) string {
				if strings.ContainsFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
					return strings.ReplaceAll(word, "-", unbreakable)
				}
				return word
			})
		}
	}
	return strings.Join(lines, "\n")
}

var wordPattern = regexp.MustCompile(`\S+`)

// fenceSegment is a stretch of markdown: prose, or the inside of a fenced code block.
type fenceSegment struct {
	code bool
	lang string // of a code block: the word after the opening fence
	text string
}

var fencePattern = regexp.MustCompile("^(`{3,}|~{3,})[ \\t]*([^ \\t`]*)")

// splitFences cuts the markdown at fenced code blocks that start at the left edge. A fence inside a list item is
// indented, and taking it out would end the item, so those stay with glamour. A block that is not closed yet (the
// reply is still streaming) runs to the end.
func splitFences(markdown string) []fenceSegment {
	var segments []fenceSegment
	var prose, code []string
	var fence, lang string
	flush := func() {
		if len(prose) > 0 {
			segments = append(segments, fenceSegment{text: strings.Join(prose, "\n")})
			prose = nil
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		switch {
		case fence == "":
			if m := fencePattern.FindStringSubmatch(line); m != nil {
				flush()
				fence, lang = m[1], m[2]
				continue
			}
			prose = append(prose, line)
		case strings.HasPrefix(line, fence[:1]) && strings.TrimRight(line, " ") == strings.Repeat(fence[:1], len(strings.TrimRight(line, " "))) && len(strings.TrimRight(line, " ")) >= len(fence):
			segments = append(segments, fenceSegment{code: true, lang: lang, text: strings.Join(code, "\n")})
			code, fence = nil, ""
		default:
			code = append(code, line)
		}
	}
	if fence != "" {
		segments = append(segments, fenceSegment{code: true, lang: lang, text: strings.Join(code, "\n")})
	}
	flush()
	return segments
}

// codeBand draws a code block on a shaded band, as wide as the text, with a space of padding on each side. Lines are
// not wrapped: a line that is too long is cut with an ellipsis. The highlighter resets its colours after every token,
// which would end the band, so the band's colour is put back after each reset.
func (m *Model) codeBand(seg fenceSegment, width int) []string {
	bg, _, _ := strings.Cut(lipgloss.NewStyle().Background(m.th.band).Render("x"), "x")
	var rows []string
	for _, line := range strings.Split(highlight(strings.TrimRight(seg.text, "\n"), seg.lang, m.th.dark), "\n") {
		line = ansi.Truncate(line, width-2, "…")
		line = strings.ReplaceAll(strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg), "\x1b[m", "\x1b[m"+bg)
		pad := strings.Repeat(" ", max(0, width-2-ansi.StringWidth(line)))
		rows = append(rows, bg+" "+line+pad+" \x1b[m")
	}
	return rows
}
