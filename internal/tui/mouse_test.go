package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

func clickAt(m Model, x, y int) Model {
	next, _ := update(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	return next
}

// rowOf is the row of the screen that holds text.
func rowOf(t *testing.T, m Model, text string) int {
	t.Helper()
	for y, line := range strings.Split(screen(m), "\n") {
		if strings.Contains(line, text) {
			return y
		}
	}
	t.Fatalf("no row of the screen holds %q:\n%s", text, screen(m))
	return 0
}

func TestClickingAToolCardOpensAndClosesIt(t *testing.T) {
	m := withCards(t, lines(20))
	y := rowOf(t, m, "✓ read_file")

	m = clickAt(m, 20, y)
	if opened := screenText(m); !strings.Contains(opened, "row 20") || !strings.Contains(opened, "▸") {
		t.Fatalf("a click on the header did not open and select the card:\n%s", opened)
	}

	m = clickAt(m, 20, y+3) // any row of the card does the same
	if closed := screenText(m); strings.Contains(closed, "row 1 ") {
		t.Errorf("a click on its output did not close it:\n%s", closed)
	}
}

func TestClickingElsewhereInTheTranscriptEndsTheSelection(t *testing.T) {
	m := withCards(t, "a")
	m.addEntry(entry{role: roleAssistant, text: "an answer"})
	m.refresh()
	m, _ = update(m, tab)
	if m.cards.selected < 0 {
		t.Fatal("no card selected to begin with")
	}

	m = clickAt(m, 20, rowOf(t, m, "an answer"))

	if m.cards.selected != -1 {
		t.Error("a click on text that is no card should leave the selection")
	}
}

func TestOnlyTheLeftButtonClicks(t *testing.T) {
	m := withCards(t, lines(20))
	y := rowOf(t, m, "✓ read_file")

	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{Button: tea.MouseRight, X: 20, Y: y},
		tea.MouseClickMsg{Button: tea.MouseMiddle, X: 20, Y: y},
		tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 20, Y: y},
		tea.MouseMotionMsg{X: 20, Y: y},
	} {
		m, _ = update(m, msg)
		if strings.Contains(screenText(m), "row 20") {
			t.Fatalf("%T opened the card: only the press of the left button should", msg)
		}
	}
}

func TestClickingTheHintJumpsToTheBottom(t *testing.T) {
	m := scrolledUp(scrollable(t), 2)
	if m.linesBelow() == 0 {
		t.Fatal("not scrolled up")
	}

	m = clickAt(m, m.width-5, m.height-1)

	if m.linesBelow() != 0 {
		t.Errorf("%d lines are still below", m.linesBelow())
	}
}

func TestClickingTheHelpBarDoesNothingAtTheBottom(t *testing.T) {
	m := scrollable(t)
	offset := m.viewport.YOffset()

	m = clickAt(m, 10, m.height-1)

	if m.viewport.YOffset() != offset {
		t.Error("a click on the help bar moved the transcript")
	}
}

// --- the dialog's buttons ---

// buttonAt is a point on the screen inside the button with this label.
func buttonAt(t *testing.T, m Model, label string) (x, y int) {
	t.Helper()
	for row, line := range strings.Split(screen(m), "\n") {
		if strings.Contains(line, "Allow for session") && strings.Contains(line, "Deny") {
			col := strings.Index(line, label)
			if label == "Allow" {
				col = strings.Index(line, "Allow ")
			}
			return ansi.StringWidth(line[:col]) + 1, row
		}
	}
	t.Fatalf("no row has the buttons:\n%s", screen(m))
	return 0, 0
}

func TestClickingAButtonAnswersTheDialog(t *testing.T) {
	for label, want := range map[string]agent.Decision{"Allow": agent.Allow, "Allow for session": agent.AllowSession, "Deny": agent.Deny} {
		t.Run(label, func(t *testing.T) {
			m, reply, _ := asking(t, "contents")
			x, y := buttonAt(t, m, label)

			m = clickAt(m, x, y)

			if m.approval.open {
				t.Fatal("the dialog is still open")
			}
			if d := <-reply; d != want {
				t.Errorf("answered %v, want %v", d, want)
			}
		})
	}
}

func TestClickingOutsideTheButtonsDoesNotAnswer(t *testing.T) {
	m, reply, _ := asking(t, "contents")
	x, y := buttonAt(t, m, "Deny")

	for _, p := range [][2]int{{x, y - 3}, {2, 2}, {m.width - 3, m.height - 3}, {x, y + 1}} {
		m = clickAt(m, p[0], p[1])
	}

	if !m.approval.open || len(reply) != 0 {
		t.Error("a click away from the buttons answered the dialog")
	}
}

// A click is no more allowed to answer a dialog that has only just opened than a key is.
func TestAClickRightAfterTheDialogOpensIsIgnored(t *testing.T) {
	m, reply, _ := askingNow(t, "contents")
	x, y := buttonAt(t, m, "Allow")

	m = clickAt(m, x, y)

	if !m.approval.open || len(reply) != 0 {
		t.Error("a click inside the guard time answered the dialog")
	}
}

func TestACardBehindTheDialogCannotBeClicked(t *testing.T) {
	m, _, _ := asking(t, "contents")
	m.addEntry(entry{role: roleTool, callID: "x", name: "read_file", args: `{}`, done: true, output: lines(5)})
	m.refresh()

	for y := headerHeight; y < headerHeight+m.viewport.Height(); y++ {
		m = clickAt(m, 3, y)
	}

	for _, e := range m.entries {
		if e.expanded {
			t.Fatal("a click went through the dialog to a card")
		}
	}
}
