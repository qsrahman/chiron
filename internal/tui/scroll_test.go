package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestUpAndDownScrollTheTranscriptOneLine(t *testing.T) {
	m := scrollable(t)
	bottom := m.viewport.YOffset()

	m, _ = update(m, up)
	if got := m.viewport.YOffset(); got != bottom-1 {
		t.Fatalf("Up moved the transcript to %d, want %d (one line up)", got, bottom-1)
	}
	m, _ = update(m, up)
	m, _ = update(m, down)
	if got := m.viewport.YOffset(); got != bottom-1 {
		t.Errorf("Up, Up, Down left the transcript at %d, want %d", got, bottom-1)
	}
	if m.input.Value() != "" {
		t.Errorf("the keys reached the input: %q", m.input.Value())
	}
}

func TestUpAndDownBelongToTheInputWhenThereIsText(t *testing.T) {
	m := scrollable(t)
	m.input.SetValue("first line\nsecond line")
	bottom := m.viewport.YOffset()

	m, _ = update(m, up)

	if m.viewport.YOffset() != bottom {
		t.Error("Up scrolled the transcript although the input holds text")
	}
}

func TestDownScrollsEvenWhenAQueuedPromptCanBeTakenBack(t *testing.T) {
	m := scrollable(t)
	m.queue.prompts = []string{"queued"}
	m.viewport.SetYOffset(m.viewport.YOffset() - 5)
	at := m.viewport.YOffset()

	m, _ = update(m, down)
	if m.viewport.YOffset() != at+1 {
		t.Errorf("Down moved to %d, want %d", m.viewport.YOffset(), at+1)
	}
	m, _ = update(m, up) // Up takes the queued prompt back, as before
	if m.input.Value() != "queued" || m.viewport.YOffset() != at+1 {
		t.Errorf("input %q, offset %d: Up should take the prompt back and not scroll", m.input.Value(), m.viewport.YOffset())
	}
}

func TestScrollingKeepsTheSelectedCard(t *testing.T) {
	m := withCards(t, "a")
	m, _ = update(m, tab)

	m, _ = update(m, up)

	if m.cards.selected < 0 {
		t.Error("scrolling a line cleared the card selection")
	}
}

func TestTheSuggestionListKeepsUpAndDown(t *testing.T) {
	m := scrollable(t)
	m.input.SetValue("/")
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
