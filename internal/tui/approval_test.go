package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

// asking returns a model whose turn has asked about a call, once the guard against stray keys has passed, and the
// channel the answer arrives on.
func asking(t *testing.T, details string) (Model, chan agent.Decision, *fakeAgent) {
	t.Helper()
	m, reply, a := askingNow(t, details)
	return waitOut(m), reply, a
}

// waitOut moves the clock past the guard.
func waitOut(m Model) Model {
	now := m.now()
	m.now = func() time.Time { return now.Add(answerGuard) }
	return m
}

// askingNow is asking without the wait: the modal has just opened.
func askingNow(t *testing.T, details string) (Model, chan agent.Decision, *fakeAgent) {
	t.Helper()
	a := &fakeAgent{block: true}
	m := sized(a, 100, 30)
	m, _ = typeAndEnter(t, m, "write it")
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "write", Arguments: `{"name":"a.txt"}`}})
	reply := make(chan agent.Decision, 1)
	m, _ = update(m, eventMsg{agent.ApprovalRequested{CallID: "c1", Tool: "write", Summary: "Write a.txt", Details: details, GrantKey: "a.txt", Reply: reply}})
	return m, reply, a
}

func letter(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

func TestModalOpensOnDenyAndEnterRefuses(t *testing.T) {
	m, reply, _ := asking(t, "contents")
	if !m.approval.open || m.approval.choice != choiceDeny {
		t.Fatalf("the modal should open with Deny selected: %+v", m.approval)
	}

	m, _ = update(m, enter)

	if d := <-reply; d != agent.Deny {
		t.Errorf("Enter on the preselected button answered %v, want Deny", d)
	}
	if m.approval.open {
		t.Error("the modal stayed open after the answer")
	}
}

func TestModalKeys(t *testing.T) {
	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want agent.Decision
	}{
		{"y allows", []tea.KeyPressMsg{letter('y')}, agent.Allow},
		{"s allows for the session", []tea.KeyPressMsg{letter('s')}, agent.AllowSession},
		{"n denies", []tea.KeyPressMsg{letter('n')}, agent.Deny},
		{"esc denies", []tea.KeyPressMsg{{Code: tea.KeyEscape}}, agent.Deny},
		{"left then enter picks the button to the left of Deny", []tea.KeyPressMsg{{Code: tea.KeyLeft}, enter}, agent.AllowSession},
		{"right wraps to Allow", []tea.KeyPressMsg{{Code: tea.KeyRight}, enter}, agent.Allow},
		{"tab moves right too", []tea.KeyPressMsg{{Code: tea.KeyTab}, enter}, agent.Allow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, reply, _ := asking(t, "contents")
			for _, k := range tt.keys {
				m, _ = update(m, k)
			}
			if d := <-reply; d != tt.want {
				t.Errorf("answered %v, want %v", d, tt.want)
			}
		})
	}
}

func TestModalTakesEveryKeyExceptCtrlC(t *testing.T) {
	m, _, a := asking(t, "contents")

	m, _ = update(m, letter('x'))
	m, _ = update(m, enter) // answers Deny; must not send a prompt

	if m.input.Value() != "" || len(a.texts) != 1 {
		t.Errorf("a key reached the input: %q, prompts sent %v", m.input.Value(), a.texts)
	}
}

func TestCtrlCWorksAtOnceWithTheModalOpen(t *testing.T) {
	m, _, a := askingNow(t, "contents")

	m, _ = update(m, ctrl('c'))

	if !m.cancelling || a.ctx.Err() == nil {
		t.Error("Ctrl-C with the modal open should cancel the turn, even inside the guard time")
	}
}

// A prompt can appear while the user is typing; a "y" in the middle of a word must not answer it.
func TestKeysRightAfterTheModalOpensAreIgnored(t *testing.T) {
	m, reply, _ := askingNow(t, "rm -rf x")

	for _, r := range "yes please" {
		m, _ = update(m, letter(r))
	}
	m, _ = update(m, enter)

	if len(reply) != 0 || !m.approval.open {
		t.Fatalf("a key answered the prompt inside the guard time (open %v)", m.approval.open)
	}

	m = waitOut(m)
	m, _ = update(m, letter('n'))
	if d := <-reply; d != agent.Deny {
		t.Errorf("answered %v", d)
	}
}

func TestCancelledTurnClosesTheModalAndMarksTheCard(t *testing.T) {
	m, _, _ := asking(t, "contents")
	m, _ = update(m, ctrl('c'))

	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "write", Outcome: agent.CallCancelled}})
	m, _ = update(m, eventMsg{agent.Error{Err: context.Canceled}})
	m, _ = update(m, turnClosedMsg{})

	if m.approval.open {
		t.Error("the modal is still open after the turn ended")
	}
}

func TestDeniedCardSaysSo(t *testing.T) {
	m, _, _ := asking(t, "contents")
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "write", Outcome: agent.CallDenied, Output: "error: the user denied this call"}})

	card := ansi.Strip(m.entries[1].rendered)
	if !strings.Contains(card, "⊘") || !strings.Contains(card, "denied by you") || strings.Contains(card, "ERROR") {
		t.Errorf("card:\n%s", card)
	}
}

// What the user allows must be what runs: control characters are shown as text, never removed or acted on.
func TestDetailsShowControlCharactersAsEscapes(t *testing.T) {
	m, _, _ := asking(t, "echo ok\rrm -rf ~\x1b[2J and \u202e")

	view := m.View().Content
	plain := ansi.Strip(view)
	for _, want := range []string{`echo ok\rrm -rf ~\x1b[2J and \u202e`} {
		if !strings.Contains(plain, want) {
			t.Errorf("the modal does not show %q:\n%s", want, plain)
		}
	}
	for name, bad := range map[string]string{"carriage return": "\r", "bidi override": "\u202e", "escape": "\x1b]"} {
		if strings.Contains(view, bad) {
			t.Errorf("the view contains a raw %s", name)
		}
	}
}

func TestLongDetailsAreNeverCutAndScroll(t *testing.T) {
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, "line "+strings.Repeat("x", i%7)+" "+string(rune('a'+i%26)))
	}
	details := strings.Join(lines, "\n")
	m, reply, _ := asking(t, details)

	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "line") || !strings.Contains(plain, "60 lines") || !strings.Contains(plain, "scroll") {
		t.Errorf("the modal should show a size line and say it scrolls:\n%s", plain)
	}
	if m.approval.details.TotalLineCount() != 60 {
		t.Errorf("the details hold %d lines, want all 60", m.approval.details.TotalLineCount())
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.approval.details.YOffset() == 0 {
		t.Error("PgDn did not scroll the details")
	}
	if len(reply) != 0 {
		t.Error("scrolling must not answer")
	}
}

func TestLongLinesAreWrappedNotCut(t *testing.T) {
	long := strings.Repeat("0123456789", 30)
	m, _, _ := asking(t, long)

	shown := strings.Split(ansi.Strip(m.approval.details.View()), "\n")
	if got := strings.ReplaceAll(strings.Join(shown, ""), " ", ""); got != long {
		t.Errorf("the details show %d of the %d characters", len(got), len(long))
	}
	for _, l := range shown {
		if ansi.StringWidth(l) > m.modalInnerWidth() {
			t.Errorf("a details line is %d cells wide, wider than the %d of the modal", ansi.StringWidth(l), m.modalInnerWidth())
		}
	}
}

// The modal must never make the screen taller than the window, and must keep its buttons when it can.
func TestModalFitsSmallWindows(t *testing.T) {
	for _, height := range []int{8, 11, 12, 14, 16, 24, 40} {
		m, _, _ := asking(t, strings.Repeat("line\n", 40))
		m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: height})

		out := ansi.Strip(m.View().Content)
		if rows := strings.Count(out, "\n") + 1; rows > height {
			t.Errorf("height %d: the screen has %d rows", height, rows)
		}
		// From 12 rows the whole modal fits: both of its bottom corners and the input box's are on screen.
		if height >= 12 && strings.Count(out, "╯") < 2 {
			t.Errorf("height %d: the modal is cut off:\n%s", height, out)
		}
		// From 16 rows it leaves the title row free, so the window does not look filled by the dialog alone.
		if height >= 16 && !strings.Contains(strings.SplitN(out, "\n", 2)[0], "chiron") {
			t.Errorf("height %d: the dialog covers the header:\n%s", height, out)
		}
	}
}

// What the session grant covers is shown as exactly as the details.
func TestGrantKeyIsShownWithEscapes(t *testing.T) {
	m, _, _ := asking(t, "contents")
	m.approval.req.GrantKey = "echo hi\rrm -rf ~"

	view := m.View().Content

	if strings.Contains(view, "\r") || !strings.Contains(ansi.Strip(view), `echo hi\rrm -rf ~`) {
		t.Errorf("the grant key is not shown with its escapes:\n%s", ansi.Strip(view))
	}
}

func TestToolsCommand(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		m, _ := typeAndEnter(t, newTestModel(&fakeAgent{}), "/tools")
		if said(m) != "Tools: none" {
			t.Errorf("said %q", said(m))
		}
	})
	t.Run("groups", func(t *testing.T) {
		a := &fakeAgent{tools: agent.ToolList{Free: []string{"read", "list"}, Allowed: []string{"shell: git status"}, Ask: []string{"shell", "write"}}}
		m, _ := typeAndEnter(t, newTestModel(a), "/tools")
		got := said(m)
		for _, want := range []string{"run without asking", "read, list", "allowed this session", "shell: git status", "ask first", "shell, write"} {
			if !strings.Contains(got, want) {
				t.Errorf("/tools is missing %q:\n%s", want, got)
			}
		}
	})
	t.Run("--yes", func(t *testing.T) {
		a := &fakeAgent{settings: agent.Settings{AutoApprove: true}, tools: agent.ToolList{Ask: []string{"write"}}}
		m, _ := typeAndEnter(t, newTestModel(a), "/tools")
		if !strings.Contains(ansi.Strip(m.entries[0].rendered), "--yes") {
			t.Error("/tools should say that every call is allowed")
		}
	})
}

func TestGoldenApproval(t *testing.T) {
	m, _, _ := asking(t, "git commit -m 'fix the thing'\ngit push origin main")
	checkGolden(t, "approval_100x30", screen(m))

	m, _, _ = asking(t, "echo ok\rrm -rf ~\x1b[2J")
	checkGolden(t, "approval_escapes_100x30", screen(m))
}
