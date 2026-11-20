package tui

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

// runLine types line and presses Enter, then returns the model and its command.
func runLine(t *testing.T, a *fakeAgent, line string) (Model, tea.Cmd) {
	t.Helper()
	return typeAndEnter(t, newTestModel(a), line)
}

// said is the plain text of the newest entry.
func said(m Model) string { return strings.TrimSpace(ansi.Strip(lastEntry(m).rendered)) }

func TestCommandListIsWellFormed(t *testing.T) {
	name := regexp.MustCompile(`^/[a-z][a-z0-9-]*$`)
	seen := map[string]bool{}
	list := commandList()
	for _, c := range list {
		if !name.MatchString(c.name) || seen[c.name] {
			t.Errorf("name %q is malformed or repeated", c.name)
		}
		seen[c.name] = true
		if c.description == "" || len(c.description) >= 90 || strings.Contains(c.description, "\n") || c.run == nil {
			t.Errorf("%s needs a one-line description under 90 characters and a handler", c.name)
		}
	}
	if !slices.IsSortedFunc(list, func(a, b command) int { return strings.Compare(a.name, b.name) }) {
		t.Error("commands are not sorted")
	}
}

func TestHelpListsEveryCommandAndKey(t *testing.T) {
	m, _ := runLine(t, &fakeAgent{}, "/help")

	var all string
	for _, e := range m.entries {
		all += ansi.Strip(e.rendered) + "\n"
	}
	all = strings.Join(strings.Fields(all), " ") // long descriptions wrap
	for _, c := range commandList() {
		if !strings.Contains(all, c.name) || !strings.Contains(all, c.description) {
			t.Errorf("/help does not list %s", c.name)
		}
	}
	for _, want := range []string{"enter", "ctrl+j", "pgup/pgdn", "↑/↓", "mouse wheel", "ctrl+c", "ctrl+d"} {
		if !strings.Contains(all, want) {
			t.Errorf("/help does not list the key %s", want)
		}
	}
}

func TestDispatchRules(t *testing.T) {
	tests := []struct {
		line     string
		sent     bool   // goes to the model as a prompt
		wantText string // what the transcript says otherwise
	}{
		{"/unknown", false, "Unknown command: /unknown. Type /help to list commands."},
		{"/usr/bin is missing", true, ""},
		{"/UNDO", false, "Nothing to undo"},
		{"  /undo  ", false, "Nothing to undo"},
		{"/undo now please", false, "Nothing to undo"}, // a known command takes the rest as its argument
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			a := &fakeAgent{}
			m, _ := runLine(t, a, tt.line)
			if got := len(a.texts) == 1; got != tt.sent {
				t.Fatalf("sent = %v, want %v (%v)", got, tt.sent, a.texts)
			}
			if !tt.sent && said(m) != tt.wantText {
				t.Errorf("said %q, want %q", said(m), tt.wantText)
			}
		})
	}
}

func TestCommandIsRefusedWhileATurnRuns(t *testing.T) {
	a := &fakeAgent{block: true, undoable: true}
	m, _ := typeAndEnter(t, newTestModel(a), "slow")

	m, cmd := typeAndEnter(t, m, "/undo")

	if cmd != nil || len(m.entries) != 1 {
		t.Errorf("the command ran: %d entries", len(m.entries))
	}
	if !strings.Contains(m.hint, "ctrl+c") || !strings.Contains(m.hint, "wait") {
		t.Errorf("hint %q should say to wait or press ctrl+c", m.hint)
	}
	if m.input.Value() != "/undo" {
		t.Error("the typed command should stay in the input")
	}
	m.cancel()
}

func TestHandlerErrorIsShownAndTheSessionGoesOn(t *testing.T) {
	m, _ := runLine(t, &fakeAgent{}, "/effort extreme")

	e := lastEntry(m)
	if e.role != roleError || !strings.Contains(e.text, "Command error:") || !strings.Contains(e.text, "none, minimal, low, medium, high, xhigh, max, default") {
		t.Errorf("entry = %+v", e)
	}
}

func TestUndoAndRetry(t *testing.T) {
	t.Run("undo", func(t *testing.T) {
		m, _ := runLine(t, &fakeAgent{undoable: true}, "/undo")
		if said(m) != "Last exchange removed" {
			t.Errorf("said %q", said(m))
		}
	})
	t.Run("retry sends the prompt again", func(t *testing.T) {
		a := &fakeAgent{rewindPrompt: "fix the bug"}
		m, cmd := runLine(t, a, "/retry")
		if len(a.texts) != 1 || a.texts[0] != "fix the bug" || m.events == nil || cmd == nil {
			t.Errorf("sent %v, busy %v", a.texts, m.events != nil)
		}
	})
	t.Run("nothing to retry", func(t *testing.T) {
		a := &fakeAgent{}
		m, _ := runLine(t, a, "/retry")
		if said(m) != "Nothing to retry" || len(a.texts) != 0 {
			t.Errorf("said %q, sent %v", said(m), a.texts)
		}
	})
}

func TestModel(t *testing.T) {
	a := &fakeAgent{settings: agent.Settings{Model: "gpt-x"}}
	m, _ := runLine(t, a, "/model")
	if said(m) != "Model: gpt-x" {
		t.Errorf("said %q", said(m))
	}

	m, _ = typeAndEnter(t, m, "/model Qwen3:8B-Instruct")
	if a.settings.Model != "Qwen3:8B-Instruct" {
		t.Errorf("the name's case must be kept: got %q", a.settings.Model)
	}

	m, _ = typeAndEnter(t, m, "/model two words")
	if lastEntry(m).role != roleError || a.settings.Model != "Qwen3:8B-Instruct" {
		t.Error("a model name with a space must be rejected")
	}
}

func TestEffort(t *testing.T) {
	a := &fakeAgent{}
	m, _ := runLine(t, a, "/effort")
	if said(m) != "Reasoning effort: model default" {
		t.Errorf("said %q", said(m))
	}
	m, _ = typeAndEnter(t, m, "/effort HIGH")
	if a.settings.ReasoningEffort != "high" {
		t.Errorf("effort = %q, want high", a.settings.ReasoningEffort)
	}
	m, _ = typeAndEnter(t, m, "/effort default")
	if a.settings.ReasoningEffort != "" || said(m) != "Reasoning effort: model default" {
		t.Errorf("default must leave the setting out: %q, said %q", a.settings.ReasoningEffort, said(m))
	}
	typeAndEnter(t, m, "/effort extreme")
	if a.settings.ReasoningEffort != "" {
		t.Error("an unknown level must not be set")
	}
}

func TestResetAndClear(t *testing.T) {
	a := &fakeAgent{events: []agent.Event{agent.TextDelta{Text: "hi"}, agent.Done{}}}
	m, cmd := runLine(t, a, "hello")
	m = pump(t, m, cmd)

	m, _ = typeAndEnter(t, m, "/clear")
	if len(m.entries) != 0 || a.resets != 0 {
		t.Errorf("/clear: %d entries, %d resets; it clears the view only", len(m.entries), a.resets)
	}

	m, _ = typeAndEnter(t, m, "/reset")
	if a.resets != 1 || said(m) != "Conversation reset" {
		t.Errorf("/reset: resets %d, said %q", a.resets, said(m))
	}
}

func TestExitAndQuitLeaveAtOnce(t *testing.T) {
	for _, line := range []string{"/exit", "/quit"} {
		a := &fakeAgent{conversation: true}
		m := newTestModel(a)
		m.addEntry(entry{role: roleUser, text: "earlier"}) // a conversation would make Ctrl-C ask first
		if _, cmd := typeAndEnter(t, m, line); !isQuit(cmd) {
			t.Errorf("%s did not quit", line)
		}
	}
}

func TestContextRows(t *testing.T) {
	a := &fakeAgent{estimate: agent.ContextEstimate{Instructions: 1200, Tools: 3400, ToolCount: 4, History: 56789, LastInput: 61000, Budget: 65536}}
	m, _ := typeAndEnter(t, newTestModel(a), "/context")

	got := said(m)
	for _, want := range []string{"instructions", "~1,200 tokens", "tools (4)", "~3,400", "history", "~56,789", "total", "~61,389", "61,000 tokens, counted by the server", "budget", "65,536 tokens (93% used)"} {
		if !strings.Contains(got, want) {
			t.Errorf("/context is missing %q:\n%s", want, got)
		}
	}
}

func TestContextRowsWithASummary(t *testing.T) {
	a := &fakeAgent{estimate: agent.ContextEstimate{History: 1000, Summary: 700, Budget: 65536}}
	m, _ := typeAndEnter(t, newTestModel(a), "/context")

	got := said(m)
	for _, want := range []string{"summary", "~700 tokens", "replaces the oldest turns", "history after it"} {
		if !strings.Contains(got, want) {
			t.Errorf("/context is missing %q:\n%s", want, got)
		}
	}
}

func TestCompactRunsInTheBackgroundAndHoldsPrompts(t *testing.T) {
	a := &fakeAgent{conversation: true, compaction: agent.Compaction{Turns: 3, Before: 9000, After: 800}}
	m := newTestModel(a)

	m, cmd := typeAndEnter(t, m, "/compact the file names")
	if !m.starting || cmd == nil {
		t.Fatal("the command should run in the background and hold prompts")
	}
	msg := cmd()
	if a.compactFocus != "the file names" {
		t.Errorf("focus = %q", a.compactFocus)
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if m.starting || !strings.Contains(said(m), "Summarized 3 earlier turn(s)") {
		t.Errorf("starting = %v, said:\n%s", m.starting, said(m))
	}
}

func TestCompactSaysWhyItFailed(t *testing.T) {
	a := &fakeAgent{conversation: true, compactErr: errors.New("the summary is empty")}
	m, cmd := typeAndEnter(t, newTestModel(a), "/compact")
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(said(m), "Could not summarize: the summary is empty") {
		t.Errorf("said:\n%s", said(m))
	}
}

func TestCompactWithoutAConversation(t *testing.T) {
	m, cmd := typeAndEnter(t, newTestModel(&fakeAgent{}), "/compact")
	if cmd != nil || m.starting || !strings.Contains(said(m), "Nothing to summarize") {
		t.Errorf("starting = %v, said:\n%s", m.starting, said(m))
	}
}

func TestSummaryShowsTheNotes(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{summary: "## Goal\nbuild it"}), "/summary")
	if got := said(m); !strings.Contains(got, "## Goal") || !strings.Contains(got, "build it") {
		t.Errorf("said:\n%s", got)
	}
	m, _ = typeAndEnter(t, newTestModel(&fakeAgent{}), "/summary")
	if !strings.Contains(said(m), "No summary yet") {
		t.Errorf("said:\n%s", said(m))
	}
}

func TestNoticesAreShown(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.handleEvent(agent.Notice{Text: "Context full: older tool results dropped"})
	if !strings.Contains(said(m), "Context full") {
		t.Errorf("said:\n%s", said(m))
	}
}

func TestSuggestionsFollowTheInput(t *testing.T) {
	labels := func(m Model) []string {
		list, _ := m.suggestions()
		var out []string
		for _, s := range list {
			out = append(out, s.label)
		}
		return out
	}
	m := newTestModel(&fakeAgent{})

	m.input.SetValue("hello")
	if len(labels(m)) != 0 {
		t.Error("plain text must not open the list")
	}
	m.input.SetValue("/")
	if len(labels(m)) != len(commandList()) {
		t.Errorf("a lone slash lists every command: %v", labels(m))
	}
	m.input.SetValue("/re")
	if got := labels(m); !slices.Equal(got, []string{"/reconnect [NAME]", "/reset", "/retry"}) {
		t.Errorf("/re lists %v", got)
	}
	m.input.SetValue("/effort h")
	if got := labels(m); !slices.Equal(got, []string{"high"}) {
		t.Errorf("/effort h lists %v", got)
	}
	m.input.SetValue("/model ")
	if len(labels(m)) != 0 {
		t.Error("a command with free text has nothing to suggest")
	}
	m.input.SetValue("/effort high x")
	if len(labels(m)) != 0 {
		t.Error("nothing is suggested once the value has more words")
	}
}

func TestSuggestionKeys(t *testing.T) {
	press := func(m Model, code rune) Model {
		next, _ := update(m, tea.KeyPressMsg{Code: code})
		return next
	}
	m := newTestModel(&fakeAgent{})
	m.input.SetValue("/re")

	m = press(m, tea.KeyDown)
	m = press(m, tea.KeyDown)
	m = press(m, tea.KeyTab)
	if m.input.Value() != "/retry" {
		t.Errorf("down, down, tab gave %q, want /retry", m.input.Value())
	}

	m.input.SetValue("/ef")
	m = press(m, tea.KeyEnter)
	if m.input.Value() != "/effort " {
		t.Errorf("enter on a partly typed command gave %q, want it completed", m.input.Value())
	}

	m.input.SetValue("/effort")
	m = press(m, tea.KeyEnter)
	if said(m) != "Reasoning effort: model default" {
		t.Errorf("enter on a whole command must run it, said %q", said(m))
	}

	m.input.SetValue("/re")
	m = press(m, tea.KeyEscape)
	if list, _ := m.suggestions(); len(list) != 0 {
		t.Error("esc must close the list")
	}
	m.input.SetValue("/res")
	if list, _ := m.suggestions(); len(list) == 0 {
		t.Error("typing again must open the list again")
	}
}

func TestListShrinksTheTranscript(t *testing.T) {
	m := sized(&fakeAgent{}, 80, 24)
	before := m.viewport.Height()
	m.input.SetValue("/")
	m.layout()
	if got := m.viewport.Height(); got != before-maxSuggestions {
		t.Errorf("viewport height %d, want %d", got, before-maxSuggestions)
	}
}

func TestNoSuggestionsDuringATurn(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{block: true}), "slow")
	m.input.SetValue("/")

	if list, _ := m.suggestions(); len(list) != 0 {
		t.Error("commands are refused during a turn, so none should be suggested")
	}
	m.cancel()
}

// With nothing typed after the command, Enter runs it: the list is only showing what could follow.
func TestEnterRunsACommandWhoseValueIsNotTypedYet(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{}), "/effort ")

	if said(m) != "Reasoning effort: model default" {
		t.Errorf("said %q", said(m))
	}
}

// The entries are not the screen: what a command says must also be in the transcript the user sees. (A command that
// said something without a refresh passed every test that looked at the entries, and showed nothing in the real UI.)
func TestWhatACommandSaysIsInTheTranscriptOnScreen(t *testing.T) {
	for _, line := range []string{"/nosuch", "/undo", "/retry", "/model", "/model other", "/effort", "/effort bogus", "/context", "/tools", "/servers", "/reset", "/help"} {
		t.Run(line, func(t *testing.T) {
			m := sized(&fakeAgent{}, 100, 40)

			m, _ = typeAndEnter(t, m, line)

			last := ansi.Strip(lastEntry(m).rendered)
			shown := ansi.Strip(m.viewport.GetContent())
			if first := strings.Fields(last)[0]; !strings.Contains(shown, first) || !strings.Contains(shown, strings.TrimSpace(strings.Split(last, "\n")[0])) {
				t.Errorf("the transcript on screen does not show what %s said (%q):\n%s", line, last, shown)
			}
		})
	}
}

// sessionList is a small list of sessions, the first of which is the current one.
func sessionList() []agent.SessionInfo {
	return []agent.SessionInfo{
		{ID: "20261118-100000-abcd", Turns: 3, Prompt: "fix the\nbuild", Current: true},
		{ID: "20261117-090000-wxyz", Turns: 1, Prompt: "hello"},
	}
}

func TestSessionsOpensAPickerOfThisDirectory(t *testing.T) {
	a := &fakeAgent{sessions: sessionList()}
	m, _ := typeAndEnter(t, newTestModel(a), "/sessions")
	if !m.picker.open {
		t.Fatal("the picker did not open")
	}
	got := screen(m)
	for _, want := range []string{"Resume a session of this directory", "20261118-100000-abcd", "(this one)", "3 turns", "fix the …", "20261117-090000-wxyz", "1 turn"} {
		if !strings.Contains(got, want) {
			t.Errorf("the picker is missing %q:\n%s", want, got)
		}
	}
	m, _ = typeAndEnter(t, newTestModel(a), "/sessions all")
	if m.picker.open || !strings.Contains(said(m), "takes no argument") {
		t.Errorf("an argument was accepted:\n%s", said(m))
	}
}

func TestThePickerResumesTheChosenSessionInPlace(t *testing.T) {
	a := &fakeAgent{sessions: sessionList(), resumedMsgs: []agent.Message{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi there"},
	}}
	m, cmd := typeAndEnter(t, newTestModel(a), "say something")
	m = pump(t, m, cmd)
	m, _ = typeAndEnter(t, m, "/sessions")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.resumed != "20261117-090000-wxyz" || m.picker.open {
		t.Fatalf("resumed %q, picker open %v", a.resumed, m.picker.open)
	}
	got := screen(m)
	for _, want := range []string{"hello", "hi there", "Resumed session 20261117-090000-wxyz"} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "say something") {
		t.Errorf("the earlier conversation is still shown:\n%s", got)
	}
}

func TestThePickerCanBeClosedAndNeverResumesTheCurrentSession(t *testing.T) {
	a := &fakeAgent{sessions: sessionList()}
	m, _ := typeAndEnter(t, newTestModel(a), "/sessions")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.picker.open || a.resumed != "" {
		t.Errorf("Esc: open %v, resumed %q", m.picker.open, a.resumed)
	}

	m, _ = typeAndEnter(t, m, "/sessions") // the current session is the first row
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.resumed != "" || !strings.Contains(said(m), "Already in session 20261118-100000-abcd") {
		t.Errorf("resumed %q, said:\n%s", a.resumed, said(m))
	}
}

func TestAFailedResumeShowsTheErrorAndKeepsTheConversation(t *testing.T) {
	a := &fakeAgent{sessions: sessionList(), resumeErr: errors.New("session is damaged")}
	m, cmd := typeAndEnter(t, newTestModel(a), "keep me")
	m = pump(t, m, cmd)
	m, _ = typeAndEnter(t, m, "/sessions")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	got := screen(m)
	if !strings.Contains(got, "Could not resume: session is damaged") || !strings.Contains(got, "keep me") {
		t.Errorf("screen:\n%s", got)
	}
}

func TestSessionsWhenOffOrEmpty(t *testing.T) {
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{sessionsErr: agent.ErrSessionsOff}), "/sessions")
	if !strings.Contains(said(m), "sessions are off") {
		t.Errorf("said:\n%s", said(m))
	}
	m, _ = typeAndEnter(t, newTestModel(&fakeAgent{}), "/sessions")
	if m.picker.open || !strings.Contains(said(m), "No saved sessions yet") {
		t.Errorf("said:\n%s", said(m))
	}
}

func TestAResumedConversationIsShownAgain(t *testing.T) {
	a := &fakeAgent{sessionID: "20261118-100000-abcd", summary: "## Goal", messages: []agent.Message{
		{Role: "user", Text: "list the files"},
		{Role: "tool", CallID: "c1", Name: "shell", Arguments: `{"command":"ls"}`, Output: "main.go"},
		{Role: "assistant", Text: "There is one file."},
	}}
	m := newTestModel(a)

	var roles []role
	for _, e := range m.entries {
		roles = append(roles, e.role)
	}
	if !slices.Equal(roles, []role{roleUser, roleTool, roleAssistant, roleNotice}) {
		t.Fatalf("entries = %v", roles)
	}
	if card := m.entries[1]; !card.done || card.output != "main.go" || card.outcome != agent.CallOK {
		t.Errorf("card = %+v", card)
	}
	if note := m.entries[3].text; !strings.Contains(note, "Resumed session 20261118-100000-abcd") || !strings.Contains(note, "/summary") {
		t.Errorf("note = %q", note)
	}
}

func TestANewConversationShowsNothingExtra(t *testing.T) {
	if m := newTestModel(&fakeAgent{}); len(m.entries) != 0 {
		t.Errorf("entries = %d", len(m.entries))
	}
}
