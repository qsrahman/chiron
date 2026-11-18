package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

const (
	// cardPreviewLines is how many lines of output a collapsed card shows.
	cardPreviewLines = 5
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

// renderTool draws a tool call: a header (icon, name, arguments, duration) and, once finished, the output
// on a band. The card is indented by the shared gutter.
func (m *Model) renderTool(e entry) string {
	width := m.contentWidth() - gutter
	lines := []string{m.cardHeader(e, width)}
	if e.done {
		body := m.cardBody(e, width-bodyIndent)
		lines = append(lines, indent(strings.Join(body, "\n"), bodyIndent))
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

	header := m.cardIcon(e) + " " + m.sty.toolName.Render(e.name)
	if main != "" {
		header += "  " + main
	}
	if len(rest) > 0 {
		header += " " + m.sty.toolMeta.Render("("+strings.Join(rest, ", ")+")")
	}
	if d := m.cardElapsed(e); d != "" {
		header += " " + m.sty.toolMeta.Render("· "+d)
	}
	return ansi.Truncate(header, width, "…")
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

func (m *Model) cardBody(e entry, width int) []string {
	switch {
	case e.outcome == agent.CallCancelled:
		return []string{m.sty.toolMeta.Render("cancelled")}
	case e.outcome == agent.CallDenied:
		return []string{m.sty.toolMeta.Render("denied by you")}
	case e.outcome == agent.CallFailed:
		return m.failureLines(e, width)
	}
	return m.bandLines(e, width)
}

// failureLines shows an error as a badge followed by its first lines.
func (m *Model) failureLines(e entry, width int) []string {
	message := strings.TrimPrefix(strings.TrimSpace(e.output), "error: ")
	badge := m.sty.badge.Render("ERROR")
	lines := strings.Split(ansi.Strip(message), "\n")
	out := []string{badge + " " + m.sty.errorText.Render(ansi.Truncate(lines[0], width-ansi.StringWidth(badge)-1, "…"))}
	shown := len(lines)
	if !e.expanded {
		shown = min(shown, cardPreviewLines)
	}
	for _, line := range lines[1:shown] {
		out = append(out, "  "+m.sty.errorText.Render(ansi.Truncate(line, width-2, "…")))
	}
	return out
}

// bandLines shows output on a background band, cut to the preview size. If only one line would be hidden
// it is shown instead, since "1 more line" saves nothing.
func (m *Model) bandLines(e entry, width int) []string {
	text := strings.TrimRight(strings.ReplaceAll(ansi.Strip(e.output), "\t", "    "), "\n ")
	all := strings.Split(text, "\n")
	shown := all
	if !e.expanded && len(all) > cardPreviewLines+1 {
		shown = all[:cardPreviewLines]
	}

	out := make([]string, 0, len(shown)+1)
	for _, line := range shown {
		out = append(out, m.sty.band.Width(width).Render(" "+ansi.Truncate(line, width-2, "…")))
	}
	if hidden := len(all) - len(shown); hidden > 0 {
		hint := fmt.Sprintf(" … (%d more lines)", hidden)
		if e.selected {
			hint += " · enter to expand"
		}
		out = append(out, m.sty.bandHint.Width(width).Render(hint))
	}
	return out
}
