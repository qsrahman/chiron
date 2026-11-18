package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// cardState is the selected tool card: an index into the entries, -1 for none. reveal asks the next refresh to
// scroll to it.
type cardState struct {
	selected int
	reveal   bool
}

// toolCards returns the indexes of the entries that are tool cards.
func (m *Model) toolCards() []int {
	var cards []int
	for i, e := range m.entries {
		if e.role == roleTool {
			cards = append(cards, i)
		}
	}
	return cards
}

// moveCard selects the next or previous card; with none selected it starts at the newest.
func (m *Model) moveCard(delta int) {
	cards := m.toolCards()
	if len(cards) == 0 {
		return
	}
	pos := len(cards) - 1
	for i, c := range cards {
		if c == m.cards.selected {
			pos = (i + delta + len(cards)) % len(cards)
		}
	}
	m.selectCard(cards[pos])
}

// selectCard marks one card as selected, or none with -1, and asks the next refresh to bring it into view.
func (m *Model) selectCard(i int) {
	if m.cards.selected >= 0 && m.cards.selected < len(m.entries) {
		m.entries[m.cards.selected].selected = false
		m.entries[m.cards.selected].rendered = m.render(m.entries[m.cards.selected])
	}
	m.cards.selected = i
	if i >= 0 {
		m.entries[i].selected = true
		m.entries[i].rendered = m.render(m.entries[i])
		m.cards.reveal = true
	}
	m.refresh()
}

// toggleCard switches the selected card between its preview and its whole output.
func (m *Model) toggleCard() {
	card := &m.entries[m.cards.selected]
	card.expanded = !card.expanded
	card.rendered = m.render(*card)
	m.cards.reveal = true
	m.refresh()
}

// emptyInputKey handles the keys that mean something only while the input is empty: choosing a card, taking a
// queued prompt back, and scrolling the transcript a line. It reports whether it used the key.
func (m *Model) emptyInputKey(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, m.keys.cardNext) && len(m.toolCards()) > 0:
		m.moveCard(1)
	case key.Matches(msg, m.keys.cardPrev) && len(m.toolCards()) > 0:
		m.moveCard(-1)
	case m.cards.selected >= 0 && key.Matches(msg, m.keys.toggle):
		m.toggleCard()
	case m.cards.selected >= 0 && key.Matches(msg, m.keys.dismiss):
		m.selectCard(-1)
	case len(m.queue.prompts) > 0 && key.Matches(msg, m.keys.unqueue):
		m.unqueue()
	case key.Matches(msg, m.keys.line):
		m.viewport, _ = m.viewport.Update(msg) // the viewport's own keys move it one line
	default:
		if m.cards.selected >= 0 && !key.Matches(msg, m.keys.scroll) {
			m.selectCard(-1) // typing goes back to the input
		}
		return false
	}
	return true
}

// cardOffset returns the first line of an entry in the transcript and how many lines it takes.
func (m *Model) cardOffset(index int) (top, height int) {
	for i, e := range m.entries {
		height = strings.Count(e.rendered, "\n") + 1
		if i == index {
			return top, height
		}
		top += height + 1 // the blank line between entries
	}
	return 0, 0
}

// showCard scrolls the transcript just enough to bring the selected card into view, or its top if it is taller
// than the window.
func (m *Model) showCard() {
	top, height := m.cardOffset(m.cards.selected)
	y, window := m.viewport.YOffset(), m.viewport.Height()
	switch {
	case top < y:
		m.viewport.SetYOffset(top)
	case top+height > y+window:
		m.viewport.SetYOffset(min(top, top+height-window))
	}
}
