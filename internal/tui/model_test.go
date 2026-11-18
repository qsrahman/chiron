package tui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// fakeAgent replays scripted events, or with block set waits for cancellation like a slow model.
type fakeAgent struct {
	events []agent.Event
	block  bool

	texts []string
	ctx   context.Context

	// What the slash commands act on.
	settings     agent.Settings
	estimate     agent.ContextEstimate
	resets       int
	undoable     bool // whether Undo finds an exchange
	rewindPrompt string
	conversation bool
}

func (f *fakeAgent) Reset()                { f.resets++ }
func (f *fakeAgent) Undo() bool            { return f.undoable }
func (f *fakeAgent) HasConversation() bool { return f.conversation }
func (f *fakeAgent) Settings() agent.Settings {
	s := f.settings
	if s.Model == "" {
		s.Model = "test-model"
	}
	return s
}
func (f *fakeAgent) SetModel(model string)                  { f.settings.Model = model }
func (f *fakeAgent) SetReasoningEffort(e string)            { f.settings.ReasoningEffort = e }
func (f *fakeAgent) ContextEstimate() agent.ContextEstimate { return f.estimate }
func (f *fakeAgent) Rewind() (string, bool)                 { return f.rewindPrompt, f.rewindPrompt != "" }

func (f *fakeAgent) Send(ctx context.Context, text string) <-chan agent.Event {
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

func newTestModel(s Agent) Model {
	return New(s, Info{Version: "v0.0.0-test", WorkDir: "/work/demo", Branch: "main"})
}

func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

var enter = tea.KeyPressMsg{Code: tea.KeyEnter}

// update applies msg and returns the concrete model, which is all these tests need.
func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// pump runs commands and feeds their messages back until none are left, as the Bubble Tea runtime would.
// Batches are unpacked. A spinner tick is delivered but its follow-up is dropped, since it would tick for as
// long as the turn runs.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinner.TickMsg:
			m, _ = update(m, msg)
		default:
			var follow tea.Cmd
			m, follow = update(m, msg)
			queue = append(queue, follow)
		}
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
	sender := &fakeAgent{events: []agent.Event{
		agent.TextDelta{Text: "Here is **bold"},
		agent.TextDelta{Text: "** text"},
		agent.Done{},
	}}
	m, cmd := typeAndEnter(t, newTestModel(sender), "hello")

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
	sender := &fakeAgent{block: true}
	m, _ := typeAndEnter(t, newTestModel(sender), "hi")
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
	sender := &fakeAgent{block: true}
	m := newTestModel(sender)

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
	sender := &fakeAgent{block: true}
	m, wait := typeAndEnter(t, newTestModel(sender), "slow")

	m, cmd := update(m, ctrl('c'))
	if isQuit(cmd) {
		t.Fatal("first Ctrl-C while busy must cancel the turn, not quit")
	}
	if sender.ctx.Err() == nil {
		t.Error("the turn's context was not cancelled")
	}
	if !strings.Contains(m.activityLine(), "Cancelling") || !strings.Contains(ansi.Strip(m.helpView()), "quit now") {
		t.Errorf("activity %q / help %q should say the turn is being cancelled", m.activityLine(), m.helpView())
	}

	// A second Ctrl-C while the turn is still winding down quits.
	if _, cmd := update(m, ctrl('c')); !isQuit(cmd) {
		t.Error("second Ctrl-C should quit")
	}

	// Otherwise the agent reports the cancellation and the model goes idle.
	m = pump(t, m, wait)
	if lastEntry(m).role != roleNotice || lastEntry(m).text != "Cancelled" {
		t.Errorf("last entry = %+v, want a cancelled notice", lastEntry(m))
	}
	if m.events != nil || m.cancelling {
		t.Error("model should be idle after the cancelled turn ends")
	}
}

func TestCtrlCQuitsWhenIdle(t *testing.T) {
	if _, cmd := update(newTestModel(&fakeAgent{}), ctrl('c')); !isQuit(cmd) {
		t.Error("Ctrl-C while idle should quit")
	}
}

func TestCtrlDQuitsOnlyWithEmptyInput(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	if _, cmd := update(m, ctrl('d')); !isQuit(cmd) {
		t.Error("Ctrl-D with an empty input should quit")
	}
	m.input.SetValue("draft")
	if _, cmd := update(m, ctrl('d')); isQuit(cmd) {
		t.Error("Ctrl-D with a draft should not quit")
	}
}

func TestErrorIsShownAndPartialTextKept(t *testing.T) {
	sender := &fakeAgent{events: []agent.Event{
		agent.TextDelta{Text: "half an ans"},
		agent.Error{Err: errors.New("boom")},
	}}
	m, cmd := typeAndEnter(t, newTestModel(sender), "hi")
	m = pump(t, m, cmd)

	if got := lastEntry(m); got.role != roleError || got.text != "boom" {
		t.Errorf("last entry = %+v, want the error", got)
	}
	if got := m.entries[len(m.entries)-2]; got.role != roleAssistant || got.text != "half an ans" {
		t.Errorf("entry before the error = %+v, want the partial answer", got)
	}
}

func TestResizeRelayoutsAndRerenders(t *testing.T) {
	sender := &fakeAgent{events: []agent.Event{agent.TextDelta{Text: "# Title"}, agent.Done{}}}
	m, cmd := typeAndEnter(t, newTestModel(sender), "hi")
	m = pump(t, m, cmd)

	m, _ = update(m, tea.WindowSizeMsg{Width: 40, Height: 12})

	if m.width != 40 || m.viewport.Height() != 12-headerHeight-(m.input.Height()+2)-helpHeight {
		t.Errorf("layout: width=%d viewport height=%d", m.width, m.viewport.Height())
	}
	if !strings.Contains(lastEntry(m).rendered, "Title") {
		t.Errorf("entry lost its content after resize: %q", lastEntry(m).rendered)
	}
}

func TestLightBackgroundSwitchesTheme(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	if !m.th.dark {
		t.Fatal("dark should be the default")
	}
	m, _ = update(m, tea.BackgroundColorMsg{Color: color.White})
	if m.th.dark {
		t.Error("a light background should switch to the light theme")
	}
}

func TestToolCardsShowProgressAndResult(t *testing.T) {
	sender := &fakeAgent{events: []agent.Event{
		agent.TextDelta{Text: "Let me look."},
		agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		agent.ToolStarted{CallID: "c2", Name: "list_dir", Arguments: `{"path":"."}`},
		// Parallel calls can finish in any order; each result must land on its own card.
		agent.ToolFinished{CallID: "c2", Name: "list_dir", Output: "a.go\nb.go"},
		agent.ToolFinished{CallID: "c1", Name: "read_file", Output: "error: no such file", Failed: true},
		agent.TextDelta{Text: "Done."},
		agent.Done{},
	}}
	m, cmd := typeAndEnter(t, newTestModel(sender), "look around")
	m = pump(t, m, cmd)

	// user, the text written before the calls, two cards, the final answer
	roles := []role{roleUser, roleAssistant, roleTool, roleTool, roleAssistant}
	if len(m.entries) != len(roles) {
		t.Fatalf("got %d entries, want %d: %+v", len(m.entries), len(roles), m.entries)
	}
	for i, want := range roles {
		if m.entries[i].role != want {
			t.Errorf("entry %d role = %v, want %v", i, m.entries[i].role, want)
		}
	}

	failed, ok := m.entries[2], m.entries[3]
	if !failed.done || !failed.failed || !strings.Contains(failed.rendered, "×") || !strings.Contains(failed.rendered, "ERROR") || !strings.Contains(failed.rendered, "no such file") {
		t.Errorf("failed card not rendered as failed: %q", failed.rendered)
	}
	if !ok.done || ok.failed || !strings.Contains(ok.rendered, "✓") || !strings.Contains(ok.rendered, "b.go") {
		t.Errorf("successful card not rendered as done: %q", ok.rendered)
	}
}

func TestRunningToolCardHasNoResultMarker(t *testing.T) {
	m := newTestModel(&fakeAgent{block: true})
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"sleep 5"}`}})

	card := lastEntry(m)
	plain := ansi.Strip(card.rendered)
	if card.done || !strings.Contains(plain, "shell") || strings.ContainsAny(plain, "✓×") {
		t.Errorf("a running card shows its name but no result marker: %q", plain)
	}
}

func TestToolPreviewIsShortened(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 30, Height: 20})
	lines := []string{"line1", "line2", "line3 " + strings.Repeat("x", 100), "line4", "line5", "line6", "line7", "line8", "line9"}

	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: "{}"}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "read_file", Output: strings.Join(lines, "\n")}})

	card := ansi.Strip(lastEntry(m).rendered)
	if strings.Contains(card, "line6") || !strings.Contains(card, "line5") || !strings.Contains(card, "(4 more lines)") {
		t.Errorf("a collapsed card shows %d lines and counts the rest:\n%s", cardPreviewLines, card)
	}
	for _, line := range strings.Split(card, "\n") {
		if w := ansi.StringWidth(line); w > m.contentWidth() {
			t.Errorf("line %q is %d cells wide, want at most %d", line, w, m.contentWidth())
		}
	}
}

func TestOneHiddenLineIsShownInstead(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	six := "a\nb\nc\nd\ne\nf" // one more than the preview: "1 more line" would save nothing
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: "{}"}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "read_file", Output: six}})

	if card := ansi.Strip(lastEntry(m).rendered); !strings.Contains(card, "f") || strings.Contains(card, "more line") {
		t.Errorf("all six lines should be shown:\n%s", card)
	}
}

func TestHeaderShowsContextUsage(t *testing.T) {
	tests := []struct {
		name   string
		window int
		usage  agent.Usage
		want   string // substring of the status line
		not    string
	}{
		{"nothing before the first response", 128000, agent.Usage{}, "", "tokens"},
		{"tokens only when the window is unknown", 0, agent.Usage{InputTokens: 900, OutputTokens: 50}, "950 tokens", "%"},
		{"thousands and a percentage", 200000, agent.Usage{InputTokens: 24000, OutputTokens: 1500}, "25.5k tokens (12%)", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(&fakeAgent{}, Info{ContextWindow: tt.window})
			m, _ = update(m, eventMsg{tt.usage})

			status := ansi.Strip(m.headerView())
			if !strings.Contains(status, tt.want) {
				t.Errorf("status %q does not contain %q", status, tt.want)
			}
			if tt.not != "" && strings.Contains(status, tt.not) {
				t.Errorf("status %q should not contain %q", status, tt.not)
			}
		})
	}
}

func TestUsageIsShownWhileBusyAndLatestWins(t *testing.T) {
	m := newTestModel(&fakeAgent{block: true})
	m, _ = typeAndEnter(t, m, "hi")
	t.Cleanup(func() { m.cancel() })

	m, _ = update(m, eventMsg{agent.Usage{InputTokens: 1000, OutputTokens: 0}})
	m, _ = update(m, eventMsg{agent.Usage{InputTokens: 2000, OutputTokens: 500}})

	if header := ansi.Strip(m.headerView()); !strings.Contains(header, "2.5k tokens") {
		t.Errorf("header = %q, want the latest context size", header)
	}
	if activity := ansi.Strip(m.activityLine()); !strings.Contains(activity, "Working") {
		t.Errorf("activity = %q, want a working indicator", activity)
	}
}
