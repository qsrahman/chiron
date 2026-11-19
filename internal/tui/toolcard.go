package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

const (
	// paramValueWidth cuts long argument values in the card header.
	paramValueWidth = 40
	// bodyIndent is how far a card's output sits to the right of its header.
	bodyIndent = 2
)

// mainParamKeys are the arguments worth showing first, in the order of preference.
var mainParamKeys = []string{"path", "file_path", "command", "pattern", "url", "query", "name"}

// toolParams splits a call's JSON arguments into the value to show prominently and the remaining
// "key=value" pairs. Arguments that are not a JSON object are returned as they are.
func toolParams(raw string) (main string, rest []string) {
	raw = strings.TrimSpace(raw)
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return singleLine(raw), nil
	}

	mainKey := ""
	for _, k := range mainParamKeys {
		if _, ok := args[k]; ok {
			mainKey = k
			break
		}
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k == mainKey {
			main = paramValue(args[k])
			continue
		}
		rest = append(rest, k+"="+ansi.Truncate(paramValue(args[k]), paramValueWidth, "…"))
	}
	return main, rest
}

// paramValue renders one argument value on a single line.
func paramValue(v any) string {
	if s, ok := v.(string); ok {
		return singleLine(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func singleLine(s string) string {
	line, rest, multi := strings.Cut(s, "\n")
	if multi && rest != "" {
		return line + " …"
	}
	return line
}

// cardIcon is the state marker at the start of a card header.
func (m *Model) cardIcon(e entry) string {
	switch {
	case !e.done:
		return m.sty.iconRunning.Render(m.spin.View())
	case e.outcome == agent.CallCancelled:
		return m.sty.iconOff.Render("●")
	case e.outcome == agent.CallDenied:
		return m.sty.iconOff.Render("⊘")
	case e.outcome == agent.CallFailed:
		return m.sty.iconErr.Render("×")
	default:
		return m.sty.iconOK.Render("✓")
	}
}

// renderTool draws a tool call. A card is closed by default: one header line (icon, name, arguments, and on the
// right what it returned and how long it took), plus one line for an error. Opened, it shows the whole output on a
// band. The card is indented by the shared gutter.
func (m *Model) renderTool(e entry) string {
	width := m.contentWidth() - gutter
	lines := []string{m.cardHeader(e, width)}
	if e.done {
		if body := m.cardBody(e, width-bodyIndent); len(body) > 0 {
			lines = append(lines, indent(strings.Join(body, "\n"), bodyIndent))
		}
	}
	card := indent(strings.Join(lines, "\n"), gutter)
	if e.selected { // the marker takes the place of the first gutter column
		card = m.sty.brand.Render("▸") + strings.TrimPrefix(card, " ")
	}
	return card
}

func (m *Model) cardHeader(e entry, width int) string {
	main, rest := toolParams(e.args)
	if main != "" && (e.name == "shell" || e.name == "bash") {
		main = highlight(main, "bash", m.th.dark)
	} else if main != "" {
		main = m.sty.toolParam.Render(main)
	}

	shown := e.name
	if e.title != "" {
		shown = e.title
	}
	left := m.cardIcon(e) + " " + m.cardTitle(shown)
	if main != "" {
		left += "  " + main
	}
	if len(rest) > 0 {
		left += " " + m.sty.toolMeta.Render("("+strings.Join(rest, ", ")+")")
	}

	meta := m.cardMeta(e)
	if meta == "" {
		return ansi.Truncate(left, width, "…")
	}
	meta = m.sty.toolMeta.Render(meta)
	// The meta keeps its place at the right end; a long command gives way to it.
	left = ansi.Truncate(left, width-ansi.StringWidth(meta)-2, "…")
	return left + strings.Repeat(" ", max(2, width-ansi.StringWidth(left)-ansi.StringWidth(meta))) + meta
}

// cardTitle is the name of the tool; for a tool of an MCP server the server is muted ("docs → search").
func (m *Model) cardTitle(shown string) string {
	if server, tool, ok := strings.Cut(shown, " → "); ok {
		return m.sty.toolMeta.Render(server+" → ") + m.sty.toolName.Render(tool)
	}
	return m.sty.toolName.Render(shown)
}

// cardMeta says how the call ended and what it came to: cancelled or denied, how many lines it returned, how long it
// took. A running call says only how long it has taken.
func (m *Model) cardMeta(e entry) string {
	var parts []string
	switch {
	case !e.done:
	case e.outcome == agent.CallCancelled:
		parts = append(parts, "cancelled")
	case e.outcome == agent.CallDenied:
		parts = append(parts, "denied by you")
	case e.outcome == agent.CallOK:
		if n := outputLines(e.output); n > 0 {
			parts = append(parts, plural(n, "line"))
		}
	}
	if d := m.cardElapsed(e); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " · ")
}

// outputLines counts the lines of a result; a result with nothing to show has none.
func outputLines(output string) int {
	text := strings.TrimSpace(output)
	if text == "" || text == "(no output)" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// cardElapsed is how long the call has taken, or "" while that is still too short to matter.
func (m *Model) cardElapsed(e entry) string {
	d := e.elapsed
	if !e.done {
		d = m.now().Sub(e.started)
	}
	if d < time.Second {
		return ""
	}
	return d.Round(time.Second).String()
}

// cardBody is what sits under the header: an error message (one line while the card is closed), or the whole output
// once it is opened. Calls that were cancelled or denied say so in the header and have nothing more to show.
func (m *Model) cardBody(e entry, width int) []string {
	switch e.outcome {
	case agent.CallCancelled, agent.CallDenied:
		return nil
	case agent.CallFailed:
		return m.failureLines(e, width)
	}
	if !e.expanded {
		return nil
	}
	return m.bandLines(e, width)
}

// failureLines shows an error as a badge and its first line, or all of it once the card is opened.
func (m *Model) failureLines(e entry, width int) []string {
	message := strings.TrimPrefix(strings.TrimSpace(e.output), "error: ")
	badge := m.sty.badge.Render("ERROR")
	lines := strings.Split(ansi.Strip(message), "\n")
	out := []string{badge + " " + m.sty.errorText.Render(ansi.Truncate(lines[0], width-ansi.StringWidth(badge)-1, "…"))}
	if !e.expanded {
		return out
	}
	for _, line := range lines[1:] {
		out = append(out, "  "+m.sty.errorText.Render(ansi.Truncate(line, width-2, "…")))
	}
	return out
}

// bandLines shows the whole output on a background band.
func (m *Model) bandLines(e entry, width int) []string {
	text := strings.TrimRight(strings.ReplaceAll(ansi.Strip(e.output), "\t", "    "), "\n ")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, m.sty.band.Width(width).Render(" "+ansi.Truncate(line, width-2, "…")))
	}
	return out
}
