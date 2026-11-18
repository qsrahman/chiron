package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// busyWith returns a model whose first turn is running, with the given prompts queued behind it.
func busyWith(t *testing.T, queued ...string) (Model, *fakeAgent) {
	t.Helper()
	a := &fakeAgent{block: true}
	m, _ := typeAndEnter(t, sized(a, 100, 24), "first")
	for _, q := range queued {
		m, _ = typeAndEnter(t, m, q)
	}
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	return m, a
}

// endTurn plays what the runtime does when the agent closes its channel; the next queued prompt may start.
func endTurn(m Model) Model {
	m, _ = update(m, turnClosedMsg{})
	return m
}

func TestEnterWhileBusyQueuesThePrompt(t *testing.T) {
	m, a := busyWith(t, "second", "/usr/bin is missing")

	if len(a.texts) != 1 {
		t.Errorf("a prompt was sent while busy: %v", a.texts)
	}
	if got := strings.Join(m.queue.prompts, "|"); got != "second|/usr/bin is missing" {
		t.Errorf("queue = %q", got)
	}
	if m.input.Value() != "" {
		t.Errorf("the input still holds %q", m.input.Value())
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "queued  second") {
		t.Errorf("the queue is not shown:\n%s", view)
	}
}

func TestACommandIsNeverQueued(t *testing.T) {
	m, _ := busyWith(t)

	m, _ = typeAndEnter(t, m, "/undo")

	if len(m.queue.prompts) != 0 || m.input.Value() != "/undo" || !strings.Contains(m.hint, "ctrl+c") {
		t.Errorf("queue %v, input %q, hint %q", m.queue.prompts, m.input.Value(), m.hint)
	}
}

func TestQueuedPromptsRunInOrder(t *testing.T) {
	m, a := busyWith(t, "a", "b")

	m = endTurn(m)
	if got := strings.Join(a.texts, "|"); got != "first|a" || m.events == nil {
		t.Fatalf("after the first turn: sent %q, busy %v", got, m.events != nil)
	}
	m = endTurn(m)
	if got := strings.Join(a.texts, "|"); got != "first|a|b" {
		t.Errorf("after the second turn: sent %q", got)
	}
	m = endTurn(m)
	if m.events != nil || len(m.queue.prompts) != 0 {
		t.Error("the queue should be empty and the UI idle")
	}
}

// A prompt taken from the queue must not wipe what the user is typing meanwhile.
func TestAutoSendKeepsTheDraft(t *testing.T) {
	m, _ := busyWith(t, "a")
	m.input.SetValue("half a thought")

	m = endTurn(m)

	if m.input.Value() != "half a thought" {
		t.Errorf("the draft became %q", m.input.Value())
	}
}

func TestCtrlCCancelsAndDropsTheQueue(t *testing.T) {
	m, a := busyWith(t, "a", "b")

	m, _ = update(m, ctrl('c'))
	m, _ = update(m, eventMsg{agent.Error{Err: context.Canceled}})
	m = endTurn(m)

	if len(m.queue.prompts) != 0 || len(a.texts) != 1 {
		t.Errorf("queue %v, sent %v: nothing queued may run after a cancel", m.queue.prompts, a.texts)
	}
	if got := said(m); got != "Dropped 2 queued prompts" {
		t.Errorf("said %q", got)
	}
}

func TestAnErrorHoldsTheQueue(t *testing.T) {
	m, a := busyWith(t, "a", "b")

	m, _ = update(m, eventMsg{agent.Error{Err: errors.New("connection refused")}})
	m = endTurn(m)

	if len(a.texts) != 1 || len(m.queue.prompts) != 2 || m.events != nil {
		t.Fatalf("sent %v, queue %v: after an error nothing is sent", a.texts, m.queue.prompts)
	}
	if got := said(m); !strings.Contains(got, "2 queued prompts waiting") {
		t.Errorf("said %q", got)
	}

	// Enter on an empty line sends the first.
	m, _ = update(m, enter)
	if got := strings.Join(a.texts, "|"); got != "first|a" || len(m.queue.prompts) != 1 {
		t.Errorf("sent %q, queue %v", got, m.queue.prompts)
	}
}

func TestTypedPromptAfterAnErrorKeepsTheQueueForLater(t *testing.T) {
	m, a := busyWith(t, "a")
	m, _ = update(m, eventMsg{agent.Error{Err: errors.New("boom")}})
	m = endTurn(m)

	m, _ = typeAndEnter(t, m, "use the other file")
	if got := strings.Join(a.texts, "|"); got != "first|use the other file" || len(m.queue.prompts) != 1 {
		t.Fatalf("sent %q, queue %v", got, m.queue.prompts)
	}

	m = endTurn(m) // that turn succeeded, so the held prompt goes now
	if got := strings.Join(a.texts, "|"); got != "first|use the other file|a" {
		t.Errorf("sent %q", got)
	}
}

func TestUpTakesTheLastQueuedPromptBack(t *testing.T) {
	m, _ := busyWith(t, "a", "b")

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.input.Value() != "b" || len(m.queue.prompts) != 1 {
		t.Fatalf("input %q, queue %v", m.input.Value(), m.queue.prompts)
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyUp}) // the input is not empty now: Up belongs to it
	if len(m.queue.prompts) != 1 {
		t.Errorf("queue %v: Up with text typed must not take another prompt", m.queue.prompts)
	}
}

func TestQueueShowsThreeAndCountsTheRest(t *testing.T) {
	m, _ := busyWith(t, "a", "b", "c", "d", "e")

	view := ansi.Strip(m.View().Content)

	if !strings.Contains(view, "queued  c") || strings.Contains(view, "queued  d") || !strings.Contains(view, "+ 2 more") {
		t.Errorf("view:\n%s", view)
	}
	if m.queueHeight() != 4 {
		t.Errorf("queue height = %d, want 3 rows and the count", m.queueHeight())
	}
}

func TestGoldenQueue(t *testing.T) {
	m, _ := busyWith(t, "also add a test for it", "and update the docs\nsecond line is not shown")
	checkGolden(t, "queue_100x24", screen(m))
}
