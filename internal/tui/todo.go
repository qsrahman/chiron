package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/tools"
)

// maxTodoRows is how many steps the panel above the input lists; the rest are counted.
const maxTodoRows = 5

// syncTodos takes the list from the agent, after something other than the model changed it: a session was resumed, or
// the conversation was reset.
func (m *Model) syncTodos() { m.todos = m.agent.Todos() }

// todoRows lists the model's todo list above the input while a step of it is open: how much is done, then the open
// steps from the first one on, with the finished steps before it only counted.
func (m *Model) todoRows() []string {
	if !tools.TodosOpen(m.todos) {
		return nil
	}
	done, first := 0, -1
	for i, item := range m.todos {
		if item.Status == tools.TodoCompleted {
			done++
		}
		if first < 0 && (item.Status == tools.TodoPending || item.Status == tools.TodoInProgress) {
			first = i
		}
	}
	rows := []string{m.sty.toolMeta.Render(fmt.Sprintf("todo · %d/%d done", done, len(m.todos)))}
	shown := m.todos[first:min(len(m.todos), first+maxTodoRows)]
	for _, item := range shown {
		icon, text := m.sty.toolMeta.Render("○"), m.sty.toolMeta
		switch item.Status {
		case tools.TodoInProgress:
			icon, text = m.sty.brand.Render("▸"), m.sty.toolName
		case tools.TodoCompleted:
			icon = m.sty.iconOK.Render("✓")
		case tools.TodoCancelled:
			icon = m.sty.toolMeta.Render("–")
		}
		rows = append(rows, ansi.Truncate(icon+" "+text.Render(sanitize(item.Content)), m.contentWidth()-gutter, "…"))
	}
	if more := len(m.todos) - first - len(shown); more > 0 {
		rows = append(rows, m.sty.toolMeta.Render(fmt.Sprintf("  + %d more", more)))
	}
	return rows
}

func (m *Model) todoHeight() int { return len(m.todoRows()) }

func (m *Model) todoView() string {
	rows := m.todoRows()
	if len(rows) == 0 {
		return ""
	}
	return indent(lipgloss.JoinVertical(lipgloss.Left, rows...), gutter)
}

// todoCommand prints the whole list, finished steps included, into the transcript.
func (m *Model) todoCommand(string) (tea.Cmd, error) {
	items := m.agent.Todos()
	if len(items) == 0 {
		m.say("No todos")
		return nil, nil
	}
	rows := make([][2]string, len(items))
	for i, item := range items {
		rows[i] = [2]string{tools.TodoMark(item.Status), item.Content}
	}
	m.sayTable(rows)
	return nil, nil
}
