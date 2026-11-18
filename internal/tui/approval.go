package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// The answers the modal offers, left to right. Deny is the one selected when it opens, so an answer by reflex
// refuses.
var choices = []struct {
	label    string
	decision agent.Decision
}{
	{"Allow", agent.Allow},
	{"Allow for session", agent.AllowSession},
	{"Deny", agent.Deny},
}

const denyChoice = 2

// Sizes of the modal, in cells.
const (
	modalMaxWidth    = 80
	modalChrome      = 14 // rows around the details: border, padding, title, summary, size, buttons, session note
	modalMaxDetails  = 15
	modalMinDetails  = 3
	modalSideMargins = 4
)

// approval is the modal that asks about a call. It is open while the agent waits for the answer.
type approval struct {
	open    bool
	req     agent.ApprovalRequested
	choice  int
	details viewport.Model
}

// openApproval shows the modal for a request.
func (m *Model) openApproval(req agent.ApprovalRequested) {
	req.Tool, req.Summary, req.GrantKey = sanitize(req.Tool), sanitize(req.Summary), sanitize(req.GrantKey)
	m.approval = approval{open: true, req: req, choice: denyChoice}
	m.layoutApproval()
}

// layoutApproval sizes the details to the window. The details are shown as they are, with control characters as
// visible escapes and nothing cut: what the user allows must be what runs. Long text scrolls.
func (m *Model) layoutApproval() {
	if !m.approval.open {
		return
	}
	text := ansi.Hardwrap(strings.ReplaceAll(escape(m.approval.req.Details), "\t", "    "), m.modalInnerWidth(), true)
	rows := strings.Count(text, "\n") + 1
	m.approval.details = viewport.New(viewport.WithWidth(m.modalInnerWidth()), viewport.WithHeight(min(rows, max(modalMinDetails, min(modalMaxDetails, m.height-modalChrome)))))
	m.approval.details.SetContent(text)
}

func (m *Model) modalInnerWidth() int {
	return max(20, min(m.contentWidth()-modalSideMargins, modalMaxWidth)) - 4 // border and padding
}

// answer sends the decision to the agent and closes the modal. The reply has room, so this never blocks.
func (m *Model) answer(d agent.Decision) {
	m.approval.req.Reply <- d
	m.approval = approval{}
}

// approvalKey handles the keys while the modal is open: every key belongs to it except Ctrl-C.
func (m Model) approvalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	n := len(choices)
	switch {
	case key.Matches(msg, m.keys.yes):
		m.answer(agent.Allow)
	case key.Matches(msg, m.keys.always):
		m.answer(agent.AllowSession)
	case key.Matches(msg, m.keys.no):
		m.answer(agent.Deny)
	case key.Matches(msg, m.keys.send):
		m.answer(choices[m.approval.choice].decision)
	case key.Matches(msg, m.keys.next):
		m.approval.choice = (m.approval.choice + 1) % n
	case key.Matches(msg, m.keys.prev):
		m.approval.choice = (m.approval.choice + n - 1) % n
	default:
		var cmd tea.Cmd
		m.approval.details, cmd = m.approval.details.Update(msg) // up, down, pgup, pgdn scroll the details
		return m, cmd
	}
	m.refresh()
	return m, nil
}

// approvalView draws the dialog.
func (m *Model) approvalView() string {
	a := m.approval
	inner := m.modalInnerWidth()

	size := fmt.Sprintf("%s bytes, %s", count(len(a.req.Details)), plural(strings.Count(a.req.Details, "\n")+1, "line"))
	if a.details.TotalLineCount() > a.details.Height() {
		size += " · ↑/↓ scroll"
	}

	covers := "every call to " + a.req.Tool
	if a.req.GrantKey != "" {
		covers = a.req.Tool + ": " + a.req.GrantKey
	}

	buttons := make([]string, len(choices))
	for i, c := range choices {
		style := m.sty.button
		if i == a.choice {
			style = m.sty.buttonSelected
		}
		buttons[i] = style.Render(c.label)
	}

	body := strings.Join([]string{
		m.sty.brand.Render("Approval needed"),
		ansi.Truncate(a.req.Summary, inner, "…"),
		"",
		a.details.View(),
		m.sty.toolMeta.Render(ansi.Truncate(size, inner, "…")),
		"",
		strings.Join(buttons, " "),
		m.sty.toolMeta.Render(ansi.Truncate("For the session: "+covers, inner, "…")),
	}, "\n")
	return m.sty.modal.Width(inner + 4).Render(body)
}

// overlay draws the modal centered over the screen.
func (m *Model) overlay(screen string) string {
	modal := m.approvalView()
	x := (m.width - lipgloss.Width(modal)) / 2
	y := (m.height - lipgloss.Height(modal)) / 2
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(modal).X(max(0, x)).Y(max(0, y)).Z(1),
	).Render()
}

// count formats a number with thousands separators.
func count(n int) string { return message.NewPrinter(language.English).Sprintf("%d", n) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return count(n) + " " + noun + "s"
}
