package tui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// fakeSender replays scripted events, or with block set waits for cancellation like a slow model.
type fakeSender struct {
	events []agent.Event
	block  bool

	texts []string
	ctx   context.Context
}

func (f *fakeSender) Send(ctx context.Context, text string) <-chan agent.Event {
	f.texts = append(f.texts, text)
	f.ctx = ctx
	ch := make(chan agent.Event)
	go func() {
		defer close(ch)
		if f.block {
			<-ctx.Done()
			ch <- agent.Error{Err: ctx.Err()}
			return
		}
		for _, e := range f.events {
			ch <- e
		}
	}()
	return ch
}

func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

var enter = tea.KeyPressMsg{Code: tea.KeyEnter}

// update applies msg and returns the concrete model, which is all these tests need.
func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// pump runs commands and feeds their messages back until none are left, as the Bubble Tea runtime would.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		m, cmd = update(m, msg)
	}
	return m
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func typeAndEnter(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	m.input.SetValue(text)
	return update(m, enter)
}

func lastEntry(m Model) entry { return m.entries[len(m.entries)-1] }

func TestSubmitStreamsThenRendersMarkdown(t *testing.T) {
	sender := &fakeSender{events: []agent.Event{
		agent.TextDelta{Text: "Here is **bold"},
		agent.TextDelta{Text: "** text"},
		agent.Done{},
	}}
	m, cmd := typeAndEnter(t, New(sender, "test-model"), "hello")

	if m.input.Value() != "" {
		t.Error("input was not cleared after submit")
	}
	if m.events == nil {
		t.Error("model should be busy while the turn runs")
	}
	m = pump(t, m, cmd)

	if len(sender.texts) != 1 || sender.texts[0] != "hello" {
		t.Errorf("sender got %v, want [hello]", sender.texts)
	}
	if len(m.entries) != 2 || m.entries[0].role != roleUser || lastEntry(m).role != roleAssistant {
		t.Fatalf("entries = %+v, want a user then an assistant entry", m.entries)
	}
	rendered := lastEntry(m).rendered
	if !strings.Contains(rendered, "bold") || strings.Contains(rendered, "**") {
		t.Errorf("assistant entry not rendered as markdown: %q", rendered)
	}
	if m.events != nil || m.streaming != "" {
		t.Error("model should be idle with nothing streaming after the turn")
	}
}

func TestPartialTextIsVisibleWhileStreaming(t *testing.T) {
	sender := &fakeSender{block: true}
	m, _ := typeAndEnter(t, New(sender, "m"), "hi")
	t.Cleanup(func() { m.cancel() })

	m, _ = update(m, eventMsg{agent.TextDelta{Text: "partial answer"}})

	if m.streaming != "partial answer" {
		t.Errorf("streaming = %q", m.streaming)
	}
	if !strings.Contains(m.View().Content, "partial answer") {
		t.Error("streamed text is not in the view")
	}
}

func TestEnterIsIgnoredWhenEmptyOrBusy(t *testing.T) {
	sender := &fakeSender{block: true}
	m := New(sender, "m")

	m, cmd := update(m, enter)
	if cmd != nil || len(sender.texts) != 0 {
		t.Fatal("empty input must not start a turn")
	}

	m, _ = typeAndEnter(t, m, "first")
	t.Cleanup(func() { m.cancel() })
	m, cmd = typeAndEnter(t, m, "second while busy")
	if cmd != nil || len(sender.texts) != 1 {
		t.Errorf("a prompt was sent while busy: %v", sender.texts)
	}
	if m.input.Value() != "second while busy" {
		t.Error("the draft should stay in the input while busy")
	}
}

func TestCtrlCCancelsTurnThenQuits(t *testing.T) {
	sender := &fakeSender{block: true}
	m, wait := typeAndEnter(t, New(sender, "m"), "slow")

	m, cmd := update(m, ctrl('c'))
	if isQuit(cmd) {
		t.Fatal("first Ctrl-C while busy must cancel the turn, not quit")
	}
	if sender.ctx.Err() == nil {
		t.Error("the turn's context was not cancelled")
	}
	if !strings.Contains(m.statusLine(), "cancelling") {
		t.Errorf("status = %q, want it to say cancelling", m.statusLine())
	}

	// A second Ctrl-C while the turn is still winding down quits.
	if _, cmd := update(m, ctrl('c')); !isQuit(cmd) {
		t.Error("second Ctrl-C should quit")
	}

	// Otherwise the agent reports the cancellation and the model goes idle.
	m = pump(t, m, wait)
	if lastEntry(m).role != roleNotice || lastEntry(m).text != "cancelled" {
		t.Errorf("last entry = %+v, want a cancelled notice", lastEntry(m))
	}
	if m.events != nil || m.cancelling {
		t.Error("model should be idle after the cancelled turn ends")
	}
}

func TestCtrlCQuitsWhenIdle(t *testing.T) {
	if _, cmd := update(New(&fakeSender{}, "m"), ctrl('c')); !isQuit(cmd) {
		t.Error("Ctrl-C while idle should quit")
	}
}

func TestCtrlDQuitsOnlyWithEmptyInput(t *testing.T) {
	m := New(&fakeSender{}, "m")
	if _, cmd := update(m, ctrl('d')); !isQuit(cmd) {
		t.Error("Ctrl-D with an empty input should quit")
	}
	m.input.SetValue("draft")
	if _, cmd := update(m, ctrl('d')); isQuit(cmd) {
		t.Error("Ctrl-D with a draft should not quit")
	}
}

func TestErrorIsShownAndPartialTextKept(t *testing.T) {
	sender := &fakeSender{events: []agent.Event{
		agent.TextDelta{Text: "half an ans"},
		agent.Error{Err: errors.New("boom")},
	}}
	m, cmd := typeAndEnter(t, New(sender, "m"), "hi")
	m = pump(t, m, cmd)

	if got := lastEntry(m); got.role != roleError || got.text != "error: boom" {
		t.Errorf("last entry = %+v, want the error", got)
	}
	if got := m.entries[len(m.entries)-2]; got.role != roleAssistant || got.text != "half an ans" {
		t.Errorf("entry before the error = %+v, want the partial answer", got)
	}
}

func TestResizeRelayoutsAndRerenders(t *testing.T) {
	sender := &fakeSender{events: []agent.Event{agent.TextDelta{Text: "# Title"}, agent.Done{}}}
	m, cmd := typeAndEnter(t, New(sender, "m"), "hi")
	m = pump(t, m, cmd)

	m, _ = update(m, tea.WindowSizeMsg{Width: 40, Height: 12})

	if m.width != 40 || m.viewport.Height() != 12-inputHeight-statusHeight {
		t.Errorf("layout: width=%d viewport height=%d", m.width, m.viewport.Height())
	}
	if !strings.Contains(lastEntry(m).rendered, "Title") {
		t.Errorf("entry lost its content after resize: %q", lastEntry(m).rendered)
	}
}

func TestLightBackgroundSwitchesTheme(t *testing.T) {
	m := New(&fakeSender{}, "m")
	if !m.dark {
		t.Fatal("dark should be the default")
	}
	m, _ = update(m, tea.BackgroundColorMsg{Color: color.White})
	if m.dark {
		t.Error("a light background should switch to the light theme")
	}
}
