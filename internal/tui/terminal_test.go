package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

// messages runs a command and returns what it produced within a moment, looking inside batches. A command that waits
// for an event (the agent's channel) produces nothing in time and is left behind.
func messages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	out := make(chan tea.Msg, 16)
	go func() { out <- cmd() }()
	var got []tea.Msg
	select {
	case msg := <-out:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				got = append(got, messages(c)...)
			}
		} else if msg != nil {
			got = append(got, msg)
		}
	case <-time.After(150 * time.Millisecond):
	}
	return got
}

func rings(cmd tea.Cmd) bool {
	for _, msg := range messages(cmd) {
		if raw, ok := msg.(tea.RawMsg); ok && raw.Msg == "\a" {
			return true
		}
	}
	return false
}

func TestTheViewAsksForFocusReports(t *testing.T) {
	if !newTestModel(&fakeAgent{}).View().ReportFocus {
		t.Error("the view does not ask the terminal to report focus")
	}
}

func TestWindowTitleSaysWhatTheProgramIsDoing(t *testing.T) {
	m := newTestModel(&fakeAgent{block: true}) // the working directory is /work/demo
	if got := m.View().WindowTitle; got != "chiron · demo" {
		t.Errorf("idle title = %q", got)
	}

	m, _ = typeAndEnter(t, m, "go")
	t.Cleanup(func() { m.cancel() })
	if got := m.View().WindowTitle; got != "● chiron · demo" {
		t.Errorf("working title = %q", got)
	}

	m, _ = update(m, eventMsg{agent.ApprovalRequested{CallID: "c", Tool: "t", Summary: "s", Reply: make(chan agent.Decision, 1)}})
	if got := m.View().WindowTitle; got != "▲ chiron · demo" {
		t.Errorf("title while it waits for an answer = %q", got)
	}
}

func TestTheProgressBarFollowsTheWork(t *testing.T) {
	m := newTestModel(&fakeAgent{block: true})
	if bar := m.View().ProgressBar; bar != nil {
		t.Errorf("an idle program shows a progress bar: %+v", bar)
	}

	m, _ = typeAndEnter(t, m, "go")
	t.Cleanup(func() { m.cancel() })
	if bar := m.View().ProgressBar; bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Errorf("working: %+v", bar)
	}

	m, _ = update(m, eventMsg{agent.ApprovalRequested{CallID: "c", Tool: "t", Summary: "s", Reply: make(chan agent.Decision, 1)}})
	if bar := m.View().ProgressBar; bar == nil || bar.State != tea.ProgressBarWarning {
		t.Errorf("waiting for an answer: %+v", bar)
	}
}

func TestTheServersStartingShowAsWork(t *testing.T) {
	m, _ := starting(t)
	if bar := m.View().ProgressBar; bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Errorf("starting: %+v", bar)
	}
}

// A window that is not in front gets a bell and a mark in its title when the run is over, so that a long turn can be left
// alone.
func TestARunThatEndsWhileTheWindowIsNotInFrontRingsAndMarksTheTitle(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "go")
	m, _ = update(m, tea.BlurMsg{})

	m, cmd := update(m, turnClosedMsg{})

	if !rings(cmd) {
		t.Error("no bell although the window was not in front")
	}
	if got := m.View().WindowTitle; got != "✓ chiron · demo" {
		t.Errorf("title = %q", got)
	}

	m, _ = update(m, tea.FocusMsg{})
	if got := m.View().WindowTitle; got != "chiron · demo" {
		t.Errorf("coming back should clear the mark, got %q", got)
	}
}

func TestAFailedRunIsMarkedAsFailed(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "go")
	m, _ = update(m, tea.BlurMsg{})
	m, _ = update(m, eventMsg{agent.Error{Err: errors.New("connection refused")}})

	m, cmd := update(m, turnClosedMsg{})

	if !rings(cmd) || m.View().WindowTitle != "✗ chiron · demo" {
		t.Errorf("title %q", m.View().WindowTitle)
	}
}

func TestNothingRingsWhileTheWindowIsInFront(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "go")

	m, cmd := update(m, turnClosedMsg{})

	if rings(cmd) || m.attention != "" {
		t.Error("a bell although the user is looking at the window")
	}
}

func TestAQueuedPromptStartingNextDoesNotRing(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "first")
	m, _ = typeAndEnter(t, m, "second")
	t.Cleanup(func() { m.cancel() })
	m, _ = update(m, tea.BlurMsg{})

	m, cmd := update(m, turnClosedMsg{})

	if rings(cmd) || m.attention != "" {
		t.Error("the run goes on with the queued prompt: it is not over")
	}
}

func TestAnApprovalAsksForAttentionWhenTheWindowIsNotInFront(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "go")
	t.Cleanup(func() { m.cancel() })
	m, _ = update(m, tea.BlurMsg{})

	_, cmd := update(m, eventMsg{agent.ApprovalRequested{CallID: "c", Tool: "t", Summary: "s", Reply: make(chan agent.Decision, 1)}})

	if !rings(cmd) {
		t.Error("no bell for an approval that waits for an answer")
	}
}

func TestAKeyPressClearsTheMark(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.attention = "✓"

	m, _ = update(m, letter('x'))

	if m.attention != "" {
		t.Errorf("attention = %q after a key press", m.attention)
	}
}

// --- the terminal's own cursor ---

// inputRow finds the row and column where the text of a row of the input box starts, on the screen.
func inputRow(t *testing.T, m Model, wantText string, prompt ...string) (row, col int) {
	t.Helper()
	lead := "❯ " // only the first row of the input has the prompt
	if len(prompt) > 0 {
		lead = prompt[0]
	}
	for y, line := range strings.Split(screen(m), "\n") {
		if i := strings.Index(line, "│ "+lead+wantText); i >= 0 {
			return y, ansi.StringWidth(line[:i]) + 2 + ansi.StringWidth(lead)
		}
	}
	t.Fatalf("no input row shows %q:\n%s", wantText, screen(m))
	return 0, 0
}

func TestTheCursorIsTheTerminalsAndSitsInTheInput(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)

	c := m.View().Cursor
	row, col := inputRow(t, m, "Ask anything")
	if c == nil || c.X != col || c.Y != row {
		t.Fatalf("cursor %+v, want the start of the input at column %d, row %d", c, col, row)
	}
	if c.Shape != tea.CursorBar {
		t.Errorf("shape = %v, want a bar", c.Shape)
	}

	m.input.SetValue("abc")
	m.input.CursorEnd()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	row, col = inputRow(t, m, "abc")
	if c := m.View().Cursor; c == nil || c.X != col+3 || c.Y != row {
		t.Errorf("after typing abc: cursor %+v, want column %d on row %d", c, col+3, row)
	}
}

func TestTheCursorFollowsTheInputWhenItGrowsAndWhenAListOpensAboveIt(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m.input.SetValue("one\ntwo\nthree")
	m.input.CursorEnd()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	row, col := inputRow(t, m, "three", "  ")
	if c := m.View().Cursor; c == nil || c.Y != row || c.X != col+len("three") {
		t.Errorf("a three-line input: cursor %+v, want row %d column %d", c, row, col+5)
	}

	m = sized(&fakeAgent{}, 100, 24)
	m.input.SetValue("/")
	m.input.CursorEnd()
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	row, col = inputRow(t, m, "/")
	if c := m.View().Cursor; c == nil || c.Y != row || c.X != col+1 {
		t.Errorf("with the suggestion list open: cursor %+v, want row %d column %d", c, row, col+1)
	}
}

func TestTheCursorIsHiddenWhileTheDialogHasTheScreen(t *testing.T) {
	m, _, _ := asking(t, "contents")

	if c := m.View().Cursor; c != nil {
		t.Errorf("the cursor %+v shows through the dialog", c)
	}
}

func TestNoVirtualCursorIsDrawn(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)

	if m.input.VirtualCursor() {
		t.Error("the input draws its own cursor as well as the terminal's")
	}
}

// --- the key for a new line ---

func TestTheHelpNamesTheNewLineKeyThatWorksInThisTerminal(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "ctrl+j new line") || strings.Contains(view, "shift+enter") {
		t.Errorf("before the terminal has said anything the help names ctrl+j:\n%s", view)
	}

	m, _ = update(m, tea.KeyboardEnhancementsMsg{Flags: 1})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "shift+enter new line") {
		t.Errorf("a terminal that can tell Shift+Enter from Enter gets it named:\n%s", view)
	}
	m, _ = typeAndEnter(t, m, "/help")
	if !strings.Contains(said(m)+allText(m), "shift+enter") {
		t.Error("/help does not name Shift+Enter either")
	}
}

func TestATerminalWithoutKeyDisambiguationKeepsCtrlJ(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)

	m, _ = update(m, tea.KeyboardEnhancementsMsg{Flags: 0})

	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "ctrl+j new line") {
		t.Errorf("view:\n%s", view)
	}
}

func allText(m Model) string {
	var all string
	for _, e := range m.entries {
		all += ansi.Strip(e.rendered) + "\n"
	}
	return all
}

// --- the clipboard ---

func TestCopyPutsTheLastAnswerOnTheClipboardThroughTheTerminal(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.addEntry(entry{role: roleAssistant, text: "an older answer"})
	m.addEntry(entry{role: roleAssistant, text: "the **last** answer\x1b]52;c;ZXZpbA==\x07"})
	m.addEntry(entry{role: roleNotice, text: "Cancelled"})

	m, cmd := typeAndEnter(t, m, "/copy")

	want := tea.SetClipboard("the **last** answer]52;c;ZXZpbA==")() // control characters removed: it will be pasted somewhere
	if got := cmd(); got != want {
		t.Errorf("clipboard message = %#v, want %#v", got, want)
	}
	if !strings.Contains(said(m), "Copied the last answer (") {
		t.Errorf("said %q", said(m))
	}
}

func TestCopyCodePutsTheLastCodeBlockOnTheClipboard(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.addEntry(entry{role: roleAssistant, text: "Run:\n\n```bash\nls -la\n```\n\nor:\n\n```bash\ngo test ./...\ngo vet ./...\n```\n\nDone."})

	m, cmd := typeAndEnter(t, m, "/copy code")

	if got, want := cmd(), tea.SetClipboard("go test ./...\ngo vet ./...")(); got != want {
		t.Errorf("clipboard message = %#v, want %#v", got, want)
	}
	if !strings.Contains(said(m), "the last code block") {
		t.Errorf("said %q", said(m))
	}
}

func TestCopyWithNothingToCopyOrNoCodeSaysSo(t *testing.T) {
	m, cmd := typeAndEnter(t, newTestModel(&fakeAgent{}), "/copy")
	if cmd != nil || said(m) != "Nothing to copy" {
		t.Errorf("no answer yet: cmd %v, said %q", cmd != nil, said(m))
	}

	m.addEntry(entry{role: roleAssistant, text: "plain words"})
	m, cmd = typeAndEnter(t, m, "/copy code")
	if cmd != nil || said(m) != "The last answer has no code block" {
		t.Errorf("no code: cmd %v, said %q", cmd != nil, said(m))
	}

	m, _ = typeAndEnter(t, m, "/copy everything")
	if lastEntry(m).role != roleError {
		t.Error("an unknown argument should be an error")
	}
}

func TestCopyIsListedAndSuggestsItsArgument(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.input.SetValue("/copy c")

	list, _ := m.suggestions()

	if len(list) != 1 || list[0].label != "code" {
		t.Errorf("suggestions = %+v", list)
	}
}

// --- the usage figure ---

func TestTheUsageFigureTurnsAmberAndRedAsTheContextFills(t *testing.T) {
	const amber, red = "38;2;251;191;36", "38;2;248;113;113"
	for _, tt := range []struct {
		tokens int64
		want   string
	}{{50000, ""}, {74999, ""}, {75000, amber}, {89999, amber}, {90000, red}, {120000, red}} {
		a := &fakeAgent{}
		a.settings.MaxContextTokens = 100000
		m := sized(a, 120, 24)
		m.contextTokens = tt.tokens

		usage := m.helpView()
		usage = usage[strings.Index(usage, "tokens")-40:]

		for _, colour := range []string{amber, red} {
			if has := strings.Contains(usage, colour); has != (colour == tt.want) {
				t.Errorf("%d tokens: has %s = %v, want %v", tt.tokens, colour, has, colour == tt.want)
			}
		}
	}
}

func TestTheUsageFigureStaysMutedWithoutAKnownWindow(t *testing.T) {
	m := sized(&fakeAgent{}, 120, 24)
	m.contextTokens = 1_000_000

	if bottom := m.helpView(); strings.Contains(bottom, "38;2;251;191;36") || strings.Contains(bottom, "38;2;248;113;113") {
		t.Error("the figure is coloured although there is no window to compare it with")
	}
}
