package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

// pickerState is /sessions: the saved sessions of this directory, one of which is resumed in place of the
// conversation on screen. It takes every key until it closes.
type pickerState struct {
	open bool
	list []agent.SessionInfo
	sel  int
}

// maxPickerRows is the most sessions shown at once; the list scrolls with the selection.
const maxPickerRows = 8

// pickerKey handles every key while the picker is open.
func (m *Model) pickerKey(msg tea.KeyPressMsg) {
	n := len(m.picker.list)
	switch {
	case key.Matches(msg, m.keys.up):
		m.picker.sel = (m.picker.sel + n - 1) % n
	case key.Matches(msg, m.keys.down):
		m.picker.sel = (m.picker.sel + 1) % n
	case key.Matches(msg, m.keys.send):
		chosen := m.picker.list[m.picker.sel]
		m.picker = pickerState{}
		m.resume(chosen)
	case key.Matches(msg, m.keys.dismiss), key.Matches(msg, m.keys.interrupt):
		m.picker = pickerState{}
	}
	m.refresh() // what the key said (an error, a notice) must reach the screen
}

// resume replaces the conversation by the chosen session and shows it again. The one on screen was saved after its
// last turn, so nothing is lost; if the chosen one cannot be loaded, nothing changes.
func (m *Model) resume(chosen agent.SessionInfo) {
	if chosen.Current {
		m.say("Already in session " + chosen.ID)
		return
	}
	if err := m.agent.ResumeSession(chosen.ID); err != nil {
		m.addEntry(entry{role: roleError, text: "Could not resume: " + err.Error()})
		return
	}
	m.entries, m.cards.selected, m.contextTokens = nil, -1, 0
	m.restore()
	if len(m.entries) == 0 {
		m.say("Resumed session " + chosen.ID)
	}
	m.refresh()
	m.viewport.GotoBottom()
}

// pickerView is the title and the rows above the input while the picker is open.
func (m *Model) pickerView() string {
	if !m.picker.open {
		return ""
	}
	title := "Resume a session of this directory"
	if len(m.picker.list) == agent.SessionListLimit {
		title += fmt.Sprintf(" (the newest %d)", agent.SessionListLimit)
	}
	sel := m.picker.sel
	start := max(0, min(sel-maxPickerRows+1, len(m.picker.list)-maxPickerRows))
	if sel < start {
		start = sel
	}
	shown := m.picker.list[start:min(len(m.picker.list), start+maxPickerRows)]

	rows := []string{m.sty.toolMeta.Render(title)}
	for i, s := range shown {
		label, marker := m.sty.toolName, "  "
		if start+i == sel {
			label, marker = m.sty.brand, "▸ "
		}
		detail := fmt.Sprintf("%s · %s · %s", s.Updated.Local().Format("2006-01-02 15:04"), plural(s.Turns, "turn"), sanitize(singleLine(s.Prompt)))
		if s.Current {
			detail += " (this one)"
		}
		rows = append(rows, ansi.Truncate(marker+label.Render(sanitize(s.ID))+"  "+m.sty.toolMeta.Render(detail), m.contentWidth()-gutter, "…"))
	}
	return indent(lipgloss.JoinVertical(lipgloss.Left, rows...), gutter)
}

func (m *Model) pickerHeight() int {
	if !m.picker.open {
		return 0
	}
	return 1 + min(len(m.picker.list), maxPickerRows)
}

// pickerHelp is what the help bar shows while the picker is open.
func (k keyMap) pickerHelp() []key.Binding {
	use := k.send
	use.SetHelp("enter", "resume")
	cancel := k.dismiss
	cancel.SetHelp("esc", "cancel")
	return []key.Binding{k.up, k.down, use, cancel}
}
