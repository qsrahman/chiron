package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// searchState is Ctrl-R: a search through the prompt history. The input is not touched while it is open; Enter
// puts the match there.
type searchState struct {
	open  bool
	query string
	match int // index in the history of the prompt shown, -1 when none matches
}

// startSearch opens the search on the newest prompt.
func (m *Model) startSearch() {
	if len(m.hist.items) == 0 {
		m.hint = "No prompt history yet"
		return
	}
	m.search = searchState{open: true, match: len(m.hist.items) - 1}
}

// findMatch looks for the query in the prompts older than `before`, newest first.
func (m *Model) findMatch(before int) int {
	query := strings.ToLower(m.search.query)
	for i := min(before, len(m.hist.items)) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(m.hist.items[i]), query) {
			return i
		}
	}
	return -1
}

// searchKey handles every key while the search is open.
func (m *Model) searchKey(msg tea.KeyPressMsg) {
	switch {
	case key.Matches(msg, m.keys.search): // again: the next older match
		if m.search.match > 0 {
			if i := m.findMatch(m.search.match); i >= 0 {
				m.search.match = i
			}
		}
	case key.Matches(msg, m.keys.send):
		if m.search.match >= 0 {
			text := m.hist.items[m.search.match]
			m.input.SetValue(text)
			m.input.CursorEnd()
			m.recall = recallState{pos: m.search.match, shown: text} // Up and Down go on from there
			m.suggest.dismissed = text
		}
		m.search = searchState{}
	case key.Matches(msg, m.keys.dismiss), key.Matches(msg, m.keys.interrupt):
		m.search = searchState{}
	case msg.Key().Code == tea.KeyBackspace:
		if runes := []rune(m.search.query); len(runes) > 0 {
			m.search.query = string(runes[:len(runes)-1])
			m.search.match = m.findMatch(len(m.hist.items))
		}
	case msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0:
		m.search.query += msg.Text
		m.search.match = m.findMatch(len(m.hist.items))
	}
}

// searchView is the row above the input while the search is open.
func (m *Model) searchView() string {
	if !m.search.open {
		return ""
	}
	shown := m.sty.errorText.Render("no match")
	if m.search.match >= 0 {
		line, _, _ := strings.Cut(strings.TrimSpace(sanitize(m.hist.items[m.search.match])), "\n")
		shown = line
	}
	row := m.sty.toolMeta.Render("search ") + m.sty.toolName.Render(sanitize(m.search.query)+"▏") + m.sty.toolMeta.Render("  ") + shown
	return indent(ansi.Truncate(row, m.contentWidth()-gutter, "…"), gutter)
}

func (m *Model) searchHeight() int {
	if m.search.open {
		return 1
	}
	return 0
}

// searchHelp is what the help bar shows while the search is open.
func (k keyMap) searchHelp() []key.Binding {
	older := k.search
	older.SetHelp("ctrl+r", "older match")
	use := k.send
	use.SetHelp("enter", "use it")
	cancel := k.dismiss
	cancel.SetHelp("esc", "cancel")
	return []key.Binding{older, use, cancel}
}
