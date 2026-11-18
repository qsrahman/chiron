package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain text is unchanged", "héllo, wörld ✓ 你好 مرحبا", "héllo, wörld ✓ 你好 مرحبا"},
		{"newline and tab are kept", "a\n\tb", "a\n\tb"},
		{"escape sequences lose their escape", "x\x1b[31mred\x1b[0m", "x[31mred[0m"},
		{"a clipboard write is defused", "a\x1b]52;c;ZXZpbA==\x07b", "a]52;c;ZXZpbA==b"},
		{"carriage return cannot overwrite a line", "safe\rEVIL", "safeEVIL"},
		{"bell and backspace", "a\x07b\x08c", "abc"},
		{"C1 controls", "a\u009bb", "ab"},
		{"bidi overrides and isolates", "a\u202eb\u2066c\u200fd", "abcd"},
		{"invisible tag characters", "a\U000e0041b", "ab"},
		{"zero-width joiners stay", "👨‍👩", "👨‍👩"},
		{"delete", "a\x7fb", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitize(tt.in); got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Whatever comes from the model, a tool or the environment, no escape sequence or spoofing character may reach
// the terminal through the view.
func TestNothingUnsafeReachesTheScreen(t *testing.T) {
	const evil = "\x1b]52;c;ZXZpbA==\x07 and \x1b[2J and \u202e and \r end"
	sender := &fakeAgent{events: []agent.Event{
		agent.TextDelta{Text: "reply " + evil},
		agent.ToolStarted{CallID: "c1", Name: "tool" + evil, Arguments: `{"path":"` + evil + `"}`},
		agent.ToolFinished{CallID: "c1", Name: "tool", Output: "output " + evil},
		agent.Error{Err: errors.New("failed " + evil)},
	}}
	sender.settings.Model = "model" + evil
	m := New(sender, Info{WorkDir: "/dir" + evil, Branch: "main" + evil})
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, cmd := typeAndEnter(t, m, "user pasted "+evil)
	m = pump(t, m, cmd)

	view := m.View().Content
	for name, bad := range map[string]string{"clipboard escape": "\x1b]52", "clear screen": "\x1b[2J", "bidi override": "\u202e", "carriage return": "\r", "bell": "\x07"} {
		if strings.Contains(view, bad) {
			t.Errorf("the view contains %s", name)
		}
	}
	// Only the sequences the UI itself draws may remain: every escape that is left must be a colour or style one.
	for i, r := range view {
		if r == 0x1b && !strings.HasPrefix(view[i:], "\x1b[") {
			t.Errorf("a non-CSI escape at byte %d: %q", i, view[i:min(len(view), i+12)])
			break
		}
	}
}

func TestRetryClearsTheStreamedTextAndShowsANotice(t *testing.T) {
	sender := &fakeAgent{block: true}
	m, _ := typeAndEnter(t, newTestModel(sender), "hi")
	t.Cleanup(func() { m.cancel() })

	m, _ = update(m, eventMsg{agent.TextDelta{Text: "half of a reply that will start over"}})
	m, _ = update(m, eventMsg{agent.Retry{Reason: "Stream cut off: EOF; trying again (1 of 2)"}})

	if m.streaming != "" {
		t.Errorf("streamed text %q should have been dropped", m.streaming)
	}
	if got := lastEntry(m); got.role != roleNotice || !strings.Contains(got.text, "trying again (1 of 2)") {
		t.Errorf("last entry = %+v, want a notice about the retry", got)
	}
	if strings.Contains(m.View().Content, "half of a reply") {
		t.Error("the discarded text is still on screen")
	}
}

// --- Ctrl-C and Ctrl-D protect a conversation that lives only in memory ---

func withConversation(t *testing.T) Model {
	t.Helper()
	sender := &fakeAgent{conversation: true, events: []agent.Event{agent.TextDelta{Text: "answer"}, agent.Done{}}}
	m, cmd := typeAndEnter(t, newTestModel(sender), "hello")
	return pump(t, m, cmd)
}

func TestCtrlCClearsTypedTextFirst(t *testing.T) {
	m := withConversation(t)
	m.input.SetValue("a long prompt I do not want to lose to a stray key")

	m, cmd := update(m, ctrl('c'))

	if isQuit(cmd) || m.input.Value() != "" {
		t.Errorf("Ctrl-C with typed text should clear it and stay (quit=%v, input=%q)", isQuit(cmd), m.input.Value())
	}
	if m.hint != "" {
		t.Error("clearing the line needs no warning")
	}
}

func TestQuitNeedsASecondPressOnceThereIsAConversation(t *testing.T) {
	for _, press := range []rune{'c', 'd'} {
		t.Run(string(press), func(t *testing.T) {
			m := withConversation(t)

			m, cmd := update(m, ctrl(press))
			if isQuit(cmd) {
				t.Fatal("the first press must not quit: the conversation would be lost")
			}
			if want := "Press ctrl+" + string(press) + " again to quit"; !strings.Contains(ansi.Strip(m.helpView()), want) {
				t.Errorf("help bar = %q, want it to say %q", ansi.Strip(m.helpView()), want)
			}

			if _, cmd := update(m, ctrl(press)); !isQuit(cmd) {
				t.Error("the second press should quit")
			}
		})
	}
}

func TestCtrlCThenCtrlDCountTogether(t *testing.T) {
	m, _ := update(withConversation(t), ctrl('c'))
	if _, cmd := update(m, ctrl('d')); !isQuit(cmd) {
		t.Error("Ctrl-C followed by Ctrl-D should quit")
	}
}

func TestAnyOtherKeyTakesTheQuitWarningBack(t *testing.T) {
	m, _ := update(withConversation(t), ctrl('c'))

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyPgUp}) // a key that types nothing
	if m.quitArmed || m.hint != "" {
		t.Error("the warning should be gone after another key")
	}
	m, cmd := update(m, ctrl('c'))

	if isQuit(cmd) {
		t.Error("a key in between starts the count again, so this press only asks again")
	}
	if !m.quitArmed {
		t.Error("it should be armed again")
	}
}

func TestNothingToLoseQuitsAtOnce(t *testing.T) {
	for _, press := range []rune{'c', 'd'} {
		if _, cmd := update(newTestModel(&fakeAgent{}), ctrl(press)); !isQuit(cmd) {
			t.Errorf("ctrl+%c with no conversation should quit at once", press)
		}
	}
}

// The conversation, not the transcript, is what a quit would lose: /clear empties the screen only.
func TestQuitStillAsksAfterClear(t *testing.T) {
	m := withConversation(t)
	m, _ = typeAndEnter(t, m, "/clear")

	if _, cmd := update(m, ctrl('c')); isQuit(cmd) {
		t.Error("Ctrl-C quit at once although the conversation is still in memory")
	}
}

func TestQuitDoesNotAskAfterReset(t *testing.T) {
	a := &fakeAgent{conversation: true}
	m := newTestModel(a)
	m.addEntry(entry{role: roleUser, text: "old"})
	a.conversation = false // what /reset does to the agent

	if _, cmd := update(m, ctrl('c')); !isQuit(cmd) {
		t.Error("nothing is left to lose, so Ctrl-C should quit")
	}
}
