package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// maxQueueRows is how many queued prompts are listed above the input; the rest are counted.
const maxQueueRows = 3

// enqueue keeps a prompt typed during a turn, to be sent when the turn ends.
func (m *Model) enqueue(text string) {
	m.queue = append(m.queue, text)
	m.input.Reset()
}

// unqueue takes the last queued prompt back into the input, to edit or delete.
func (m *Model) unqueue() {
	last := len(m.queue) - 1
	m.input.SetValue(m.queue[last])
	m.input.CursorEnd()
	m.queue = m.queue[:last]
}

// dropQueue forgets the queued prompts, because the user cancelled the turn they were waiting for. The count is
// kept to be reported once the cancellation shows.
func (m *Model) dropQueue() {
	m.dropped = len(m.queue)
	m.queue = nil
}

// nextQueued runs when a turn has ended and sends the next queued prompt. After a turn that failed on its own it
// holds the queue instead, so a broken server is not hit once per prompt; the user decides what happens next.
func (m *Model) nextQueued() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	if m.failed {
		m.say(fmt.Sprintf("%s waiting after the error: Enter on an empty line sends the first, ↑ takes the last back", plural(len(m.queue), "queued prompt")))
		return nil
	}
	return m.sendQueued()
}

func (m *Model) sendQueued() tea.Cmd {
	prompt := m.queue[0]
	m.queue = m.queue[1:]
	return m.startTurn(prompt)
}

func (m *Model) queueHeight() int {
	return min(len(m.queue), maxQueueRows) + min(1, max(0, len(m.queue)-maxQueueRows))
}

// queueView lists the next prompts above the input, one line each.
func (m *Model) queueView() string {
	if len(m.queue) == 0 {
		return ""
	}
	rows := make([]string, 0, maxQueueRows+1)
	for _, text := range m.queue[:min(len(m.queue), maxQueueRows)] {
		line, _, _ := strings.Cut(strings.TrimSpace(sanitize(text)), "\n")
		rows = append(rows, ansi.Truncate(m.sty.toolMeta.Render("› queued  "+line), m.contentWidth()-gutter, "…"))
	}
	if more := len(m.queue) - maxQueueRows; more > 0 {
		rows = append(rows, m.sty.toolMeta.Render(fmt.Sprintf("  + %d more", more)))
	}
	return indent(strings.Join(rows, "\n"), gutter)
}
