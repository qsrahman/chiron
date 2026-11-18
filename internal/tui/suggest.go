package tui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// suggestState is the list's own state: the selected row, and the input text it was closed with Esc for.
type suggestState struct {
	sel       int
	dismissed string
}

// maxSuggestions is the most rows the list shows at once; it scrolls with the selection.
const maxSuggestions = 6

// suggestion is one row of the list. accept is what the input becomes when the row is taken.
type suggestion struct {
	accept string
	label  string
	desc   string
}

// suggestions lists what could complete the input: commands while the first word is typed, then the command's
// own values. typed reports that the user has typed part of what is being completed, which Enter takes as a
// request to complete rather than to run. Nothing is offered during a turn, because commands are refused then.
func (m *Model) suggestions() (list []suggestion, typed bool) {
	v := m.input.Value()
	if m.events != nil || m.starting || !strings.HasPrefix(v, "/") || v == m.suggest.dismissed {
		return nil, false
	}
	i := strings.IndexFunc(v, unicode.IsSpace)
	if i < 0 {
		prefix := strings.ToLower(v)
		for _, c := range m.commands {
			if !strings.HasPrefix(c.name, prefix) {
				continue
			}
			accept := c.name
			if c.usage != "" {
				accept += " "
			}
			list = append(list, suggestion{accept, strings.TrimSpace(c.name + " " + c.usage), c.description})
		}
		return list, len(v) > 1
	}

	c, ok := m.command(strings.ToLower(v[:i]))
	rest := strings.TrimLeftFunc(v[i:], unicode.IsSpace)
	if !ok || c.args == nil || strings.ContainsFunc(rest, unicode.IsSpace) {
		return nil, false
	}
	for _, a := range c.args(m) {
		if strings.HasPrefix(a, strings.ToLower(rest)) {
			list = append(list, suggestion{accept: c.name + " " + a, label: a})
		}
	}
	return list, rest != ""
}

// suggestKey handles the keys of an open list and reports whether it used the key.
func (m *Model) suggestKey(msg tea.KeyPressMsg, list []suggestion, typed bool) bool {
	m.suggest.sel = min(m.suggest.sel, len(list)-1)
	switch {
	case key.Matches(msg, m.keys.up):
		m.suggest.sel = (m.suggest.sel + len(list) - 1) % len(list)
	case key.Matches(msg, m.keys.down):
		m.suggest.sel = (m.suggest.sel + 1) % len(list)
	case key.Matches(msg, m.keys.dismiss):
		m.suggest.dismissed = m.input.Value()
	case key.Matches(msg, m.keys.complete):
		m.acceptSuggestion(list[m.suggest.sel])
	case key.Matches(msg, m.keys.send):
		chosen := list[m.suggest.sel]
		// Complete a partly typed word; run what is typed when it is already whole (or nothing was typed).
		if !typed || strings.EqualFold(strings.TrimSpace(chosen.accept), strings.TrimSpace(m.input.Value())) {
			return false
		}
		m.acceptSuggestion(chosen)
	default:
		return false
	}
	return true
}

func (m *Model) acceptSuggestion(s suggestion) {
	m.input.SetValue(s.accept)
	m.input.CursorEnd()
	m.suggest.sel = 0
}

// suggestionHeight is the number of rows the list takes, 0 when it is closed.
func (m *Model) suggestionHeight() int {
	list, _ := m.suggestions()
	return min(len(list), maxSuggestions)
}

func (m *Model) suggestionView() string {
	list, _ := m.suggestions()
	if len(list) == 0 {
		return ""
	}
	sel := min(m.suggest.sel, len(list)-1)
	start := max(0, min(sel-maxSuggestions+1, len(list)-maxSuggestions))
	if sel < start {
		start = sel
	}
	shown := list[start:min(len(list), start+maxSuggestions)]

	labelWidth := 0
	for _, s := range list {
		labelWidth = max(labelWidth, lipgloss.Width(s.label))
	}
	rows := make([]string, len(shown))
	for i, s := range shown {
		label, marker := m.sty.toolName, "  "
		if start+i == sel {
			label, marker = m.sty.brand, "▸ "
		}
		rows[i] = ansi.Truncate(marker+label.Width(labelWidth).Render(s.label)+"  "+m.sty.toolMeta.Render(s.desc), m.contentWidth()-gutter, "…")
	}
	return indent(strings.Join(rows, "\n"), gutter)
}

// suggestionHelp is what the help bar shows while the list is open.
func (k keyMap) suggestionHelp() []key.Binding {
	return []key.Binding{k.up, k.down, k.complete, k.dismiss}
}
