package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// scrollable returns a model whose transcript is longer than the window, scrolled to the bottom.
func scrollable(t *testing.T) Model {
	t.Helper()
	m := sized(&fakeAgent{}, 100, 20)
	for i := 0; i < 40; i++ {
		m.addEntry(entry{role: roleNotice, text: fmt.Sprintf("line %d", i)})
	}
	m.refresh()
	m.viewport.GotoBottom()
	return m
}

var (
	up   = tea.KeyPressMsg{Code: tea.KeyUp}
	down = tea.KeyPressMsg{Code: tea.KeyDown}
)

func wheel(button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{Button: button, X: 10, Y: 5}
}

func TestUpAndDownBelongToTheInputWhenThereIsText(t *testing.T) {
	m := scrollable(t)
	m.input.SetValue("first line\nsecond line")
	m.layout() // the input has two rows now, and the window is shorter
	bottom := m.viewport.YOffset()

	m, _ = update(m, up)

	if m.viewport.YOffset() != bottom {
		t.Error("Up scrolled the transcript although the input holds text")
	}
}

func TestUpTakesAQueuedPromptBackBeforeRecallingHistory(t *testing.T) {
	m := scrollable(t)
	m.hist.add("an older prompt")
	m.queue.prompts = []string{"queued"}

	m, _ = update(m, up)
	if m.input.Value() != "queued" || len(m.queue.prompts) != 0 {
		t.Errorf("input %q, queue %v: Up should take the queued prompt back", m.input.Value(), m.queue.prompts)
	}
}

// scrolledUp moves the transcript up by n lines, as the wheel or a page key would.
func scrolledUp(m Model, n int) Model {
	m.viewport.SetYOffset(m.viewport.YOffset() - n)
	return m
}

func TestScrollingKeepsTheSelectedCard(t *testing.T) {
	m := withCards(t, "a")
	m, _ = update(m, tab)

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyPgUp})

	if m.cards.selected < 0 {
		t.Error("scrolling a line cleared the card selection")
	}
}

func TestTheSuggestionListKeepsUpAndDown(t *testing.T) {
	m := scrollable(t)
	m.input.SetValue("/")
	m.layout() // the list is open and the window has its shorter height
	bottom := m.viewport.YOffset()

	m, _ = update(m, down)

	if m.suggest.sel != 1 || m.viewport.YOffset() != bottom {
		t.Errorf("selection %d, offset %d: Down belongs to the open list", m.suggest.sel, m.viewport.YOffset())
	}
}

func TestTheWheelScrollsTheTranscript(t *testing.T) {
	m := scrollable(t)
	bottom := m.viewport.YOffset()

	m, _ = update(m, wheel(tea.MouseWheelUp))
	if got := m.viewport.YOffset(); got >= bottom {
		t.Fatalf("the wheel did not scroll up: %d", got)
	}
	m, _ = update(m, wheel(tea.MouseWheelDown))
	if m.viewport.YOffset() != bottom {
		t.Errorf("up and down again left the transcript at %d, want %d", m.viewport.YOffset(), bottom)
	}
	m, _ = update(m, wheel(tea.MouseWheelDown))
	if m.viewport.YOffset() != bottom {
		t.Error("scrolling down at the bottom moved the transcript")
	}
}

func TestTheWheelScrollsTheDetailsWhileADialogIsOpen(t *testing.T) {
	m, _, _ := asking(t, strings.Repeat("line\n", 60))
	transcript := m.viewport.YOffset()

	m, _ = update(m, wheel(tea.MouseWheelDown))

	if m.approval.details.YOffset() == 0 {
		t.Error("the wheel did not scroll the details of the dialog")
	}
	if m.viewport.YOffset() != transcript {
		t.Error("the wheel scrolled the transcript behind the dialog")
	}
	if !m.approval.open {
		t.Error("the wheel closed the dialog")
	}
}

func TestUpAndDownScrollTheDetailsWhileADialogIsOpen(t *testing.T) {
	m, reply, _ := asking(t, strings.Repeat("line\n", 60))

	m, _ = update(m, down)
	m, _ = update(m, down)
	if m.approval.details.YOffset() != 2 {
		t.Fatalf("Down, Down moved the details to %d, want 2", m.approval.details.YOffset())
	}
	m, _ = update(m, up)
	if m.approval.details.YOffset() != 1 || len(reply) != 0 {
		t.Errorf("offset %d, answered %v", m.approval.details.YOffset(), len(reply) != 0)
	}
}

func TestClicksAndMovementDoNothing(t *testing.T) {
	m := scrollable(t)
	m.input.SetValue("typed")
	bottom := m.viewport.YOffset()

	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: 3},
		tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 3, Y: 3},
		tea.MouseMotionMsg{X: 4, Y: 4},
	} {
		m, _ = update(m, msg)
	}

	if m.input.Value() != "typed" || m.viewport.YOffset() != bottom {
		t.Errorf("input %q, offset %d", m.input.Value(), m.viewport.YOffset())
	}
}

func TestTheViewAsksTheTerminalForTheWheel(t *testing.T) {
	if got := newTestModel(&fakeAgent{}).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("mouse mode = %v, want the wheel to be reported", got)
	}
}

// A window at the bottom stays at the bottom when its height changes, so that what a command says next is in view. (The
// suggestion list shortens the window while a command is typed; with the position lost, the output of the command
// ended up below the fold.)
func TestTheTranscriptKeepsFollowingAcrossAHeightChange(t *testing.T) {
	m := scrollable(t)

	m, _ = update(m, tea.KeyPressMsg{Code: '/', Text: "/"}) // the suggestion list opens and the window gets shorter
	if !m.viewport.AtBottom() {
		t.Fatal("the window left the bottom when the suggestion list opened")
	}
	m, _ = typeAndEnter(t, m, "/help")

	if !m.viewport.AtBottom() || m.linesBelow() != 0 {
		t.Errorf("after the command the window is %d lines above the bottom", m.linesBelow())
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "mouse wheel") {
		t.Errorf("the last line of the output is not on screen:\n%s", view)
	}
}

func TestAWindowThatWasScrolledUpStaysWhereItWasWhenItsHeightChanges(t *testing.T) {
	m := scrolledUp(scrollable(t), 2)
	at := m.viewport.YOffset()

	m, _ = update(m, tea.KeyPressMsg{Code: '/', Text: "/"})

	if m.viewport.YOffset() != at {
		t.Errorf("the offset went from %d to %d: someone who scrolled up wants to stay there", at, m.viewport.YOffset())
	}
}
