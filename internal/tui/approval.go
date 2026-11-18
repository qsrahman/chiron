package tui

import (
	"fmt"
	"strings"
	"time"

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
const (
	choiceAllow = iota
	choiceSession
	choiceDeny
)

var choices = [...]struct {
	label    string
	decision agent.Decision
}{
	choiceAllow:   {"Allow", agent.Allow},
	choiceSession: {"Allow for session", agent.AllowSession},
	choiceDeny:    {"Deny", agent.Deny},
}

// answerGuard is how long after the modal opens its keys are ignored (Ctrl-C excepted). A prompt can appear while
// the user is typing, and the next keystroke, a "y" in the middle of a word, must not answer it.
const answerGuard = 500 * time.Millisecond

// Sizes of the modal, in cells.
const (
	modalMaxWidth    = 80
	modalChrome      = 11 // rows around the details: border 2, padding 2, title, summary, gap, size, gap, buttons, session note
	modalScreenGap   = 2  // rows left free above and below the modal when the window allows
	modalMaxDetails  = 15
	modalMinDetails  = 1
	modalSideMargins = 4
)

// approval is the modal that asks about a call. It is open while the agent waits for the answer.
type approval struct {
	open     bool
	req      agent.ApprovalRequested
	openedAt time.Time // when the modal appeared, for answerGuard
	choice   int
	details  viewport.Model
}

// openApproval shows the modal for a request.
func (m *Model) openApproval(req agent.ApprovalRequested) {
	req.Tool, req.Summary, req.Source = sanitize(req.Tool), sanitize(req.Summary), sanitize(req.Source)
	m.approval = approval{open: true, req: req, openedAt: m.now(), choice: choiceDeny}
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
	chrome := modalChrome
	if m.approval.req.Source != "" {
		chrome++ // the line that says where the tool comes from
	}
	m.approval.details = viewport.New(viewport.WithWidth(m.modalInnerWidth()), viewport.WithHeight(min(rows, max(modalMinDetails, min(modalMaxDetails, m.height-chrome-2*modalScreenGap)))))
	m.approval.details.SetContent(text)
}

func (m *Model) modalInnerWidth() int {
	return max(20, min(m.contentWidth()-modalSideMargins, modalMaxWidth)) - 4 // border and padding
}

// answer sends the decision to the agent and closes the modal. The reply has room, so this never blocks.
func (m *Model) answer(d agent.Decision) {
	m.approval.req.Reply <- d
	m.approval = approval{}
	m.attention = ""
}

// approvalKey handles the keys while the modal is open: every key belongs to it except Ctrl-C.
func (m Model) approvalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.now().Sub(m.approval.openedAt) < answerGuard {
		return m, nil
	}
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

// approvalBody draws the dialog and says on which row inside it the buttons are.
func (m *Model) approvalBody() (view string, buttonRow int) {
	a := m.approval
	inner := m.modalInnerWidth()

	size := fmt.Sprintf("%s bytes, %s", count(len(a.req.Details)), plural(strings.Count(a.req.Details, "\n")+1, "line"))
	if a.details.TotalLineCount() > a.details.Height() {
		size += " · ↑/↓ scroll"
	}

	covers := "every call to " + a.req.Tool
	if a.req.GrantKey != "" {
		covers = a.req.Tool + ": " + escape(a.req.GrantKey) // as exact as the details: it may be the command itself
	}

	buttons := make([]string, len(choices))
	for i := range choices {
		buttons[i] = m.button(i)
	}

	rows := []string{
		m.sty.brand.Render("Approval needed"),
		ansi.Truncate(a.req.Summary, inner, "…"),
	}
	if a.req.Source != "" {
		rows = append(rows, m.sty.toolMeta.Render(ansi.Truncate("Runs: "+a.req.Source, inner, "…")))
	}
	rows = append(rows,
		"",
		a.details.View(),
		m.sty.toolMeta.Render(ansi.Truncate(size, inner, "…")),
		"",
	)
	buttonRow = m.sty.modal.GetBorderTopSize() + m.sty.modal.GetPaddingTop() + lipgloss.Height(strings.Join(rows, "\n"))
	rows = append(rows,
		strings.Join(buttons, " "),
		m.sty.toolMeta.Render(ansi.Truncate("For the session: "+covers, inner, "…")),
	)
	return m.sty.modal.Width(inner + 4).Render(strings.Join(rows, "\n")), buttonRow
}

// button draws one of the answers; the chosen one is highlighted.
func (m *Model) button(i int) string {
	if i == m.approval.choice {
		return m.sty.buttonSelected.Render(choices[i].label)
	}
	return m.sty.button.Render(choices[i].label)
}

// buttonIDs name the layers of the buttons, in the order of choices.
var buttonIDs = [...]string{choiceAllow: "allow", choiceSession: "session", choiceDeny: "deny"}

// modalLayer is the dialog as a layer, with each button a layer of its own on top, so that the compositor can say which
// of them a click landed on.
func (m *Model) modalLayer() *lipgloss.Layer {
	view, buttonRow := m.approvalBody()
	buttons := make([]*lipgloss.Layer, len(choices))
	x := m.sty.modal.GetBorderLeftSize() + m.sty.modal.GetPaddingLeft()
	for i := range choices {
		button := m.button(i)
		buttons[i] = lipgloss.NewLayer(button).ID(buttonIDs[i]).X(x).Y(buttonRow).Z(2) // z is absolute, not the parent's plus
		x += lipgloss.Width(button) + 1                                                // the buttons are one space apart
	}
	return lipgloss.NewLayer(view, buttons...).ID("modal").
		X(max(0, (m.width-lipgloss.Width(view))/2)).Y(max(0, (m.height-lipgloss.Height(view))/2)).Z(1)
}

// clickModal answers the dialog if the click landed on one of its buttons.
func (m *Model) clickModal(x, y int) {
	if m.now().Sub(m.approval.openedAt) < answerGuard {
		return
	}
	id := lipgloss.NewCompositor(m.modalLayer()).Hit(x, y).ID()
	for i, name := range buttonIDs {
		if id == name {
			m.answer(choices[i].decision)
			m.refresh()
			return
		}
	}
}

// overlay draws the modal centered over the screen.
func (m *Model) overlay(screen string) string {
	out := lipgloss.NewCompositor(lipgloss.NewLayer(screen), m.modalLayer()).Render()
	// A window too small for the modal must not get a screen taller than itself.
	if rows := strings.Split(out, "\n"); len(rows) > m.height {
		out = strings.Join(rows[:m.height], "\n")
	}
	return out
}

// count formats a number with thousands separators.
func count(n int) string { return message.NewPrinter(language.English).Sprintf("%d", n) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return count(n) + " " + noun + "s"
}
