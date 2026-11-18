package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

var (
	tab      = tea.KeyPressMsg{Code: tea.KeyTab}
	shiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	escKey   = tea.KeyPressMsg{Code: tea.KeyEscape}
	space    = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
)

// lines returns n numbered lines of output.
func lines(n int) string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("row %d", i))
	}
	return strings.Join(out, "\n")
}

// withCards returns an idle model with one finished tool card per given output.
func withCards(t *testing.T, outputs ...string) Model {
	t.Helper()
	m := sized(&fakeAgent{}, 100, 40)
	for i, out := range outputs {
		id := fmt.Sprintf("c%d", i)
		m, _ = update(m, eventMsg{agent.ToolStarted{CallID: id, Name: "read_file", Arguments: fmt.Sprintf(`{"path":"f%d.go"}`, i)}})
		m, _ = update(m, eventMsg{agent.ToolFinished{CallID: id, Name: "read_file", Output: out}})
	}
	return m
}

func screenText(m Model) string { return ansi.Strip(m.View().Content) }

func TestTabSelectsTheNewestCardAndMovesBetweenCards(t *testing.T) {
	m := withCards(t, "a", "b", "c")
	cards := m.toolCards()

	m, _ = update(m, tab)
	if m.cards.selected != cards[2] {
		t.Fatalf("first Tab selected entry %d, want the newest card %d", m.cards.selected, cards[2])
	}
	m, _ = update(m, tab)
	if m.cards.selected != cards[0] {
		t.Errorf("Tab after the newest should wrap to the first, got %d", m.cards.selected)
	}
	m, _ = update(m, shiftTab)
	if m.cards.selected != cards[2] {
		t.Errorf("Shift+Tab should go back to the newest, got %d", m.cards.selected)
	}
	if !strings.Contains(screenText(m), "▸ ✓ read_file  f2.go") {
		t.Errorf("the selected card has no marker:\n%s", screenText(m))
	}
}

func TestEnterAndSpaceToggleTheWholeOutput(t *testing.T) {
	for name, k := range map[string]tea.KeyPressMsg{"enter": enter, "space": space} {
		t.Run(name, func(t *testing.T) {
			m := withCards(t, lines(20))
			m, _ = update(m, tab)

			closed := screenText(m)
			if strings.Contains(closed, "row 1") || !strings.Contains(closed, "20 lines") {
				t.Errorf("a card is closed by default: its header and the line count, no output:\n%s", closed)
			}

			m, _ = update(m, k)
			opened := screenText(m)
			if !strings.Contains(opened, "row 1 ") || !strings.Contains(opened, "row 20") {
				t.Errorf("an opened card shows the whole output:\n%s", opened)
			}

			m, _ = update(m, k)
			if strings.Contains(screenText(m), "row 20") {
				t.Error("a second press should collapse the card again")
			}
		})
	}
}

func TestEnterOnASelectedCardDoesNotSend(t *testing.T) {
	m := withCards(t, lines(20))
	a := m.agent.(*fakeAgent)
	m, _ = update(m, tab)

	m, cmd := update(m, enter)

	if cmd != nil || len(a.texts) != 0 || m.events != nil {
		t.Errorf("Enter on a selected card started a turn: cmd %v, sent %v", cmd != nil, a.texts)
	}
}

func TestEscapeEndsTheSelection(t *testing.T) {
	m := withCards(t, "a")
	m, _ = update(m, tab)

	m, _ = update(m, escKey)

	if m.cards.selected != -1 || strings.Contains(screenText(m), "▸") {
		t.Error("Esc should clear the selection")
	}
}

func TestTypingAfterSelectingGoesBackToTheInput(t *testing.T) {
	m := withCards(t, "a")
	m, _ = update(m, tab)

	m, _ = update(m, letter('h'))

	if m.cards.selected != -1 || m.input.Value() != "h" {
		t.Errorf("card %d, input %q: the key should clear the selection and be typed", m.cards.selected, m.input.Value())
	}
}

func TestTabDoesNothingWithoutCardsOrWithTextTyped(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m, _ = update(m, tab)
	if m.cards.selected != -1 {
		t.Error("selected a card that does not exist")
	}

	m = withCards(t, "a")
	m.input.SetValue("typed")
	m, _ = update(m, tab)
	if m.cards.selected != -1 {
		t.Error("Tab with text in the input must not select a card")
	}
}

func TestCardsCanBeChosenWhileATurnRuns(t *testing.T) {
	a := &fakeAgent{block: true}
	m, _ := typeAndEnter(t, sized(a, 100, 24), "go")
	t.Cleanup(func() { m.cancel() })
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: `{"path":"a"}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "read_file", Output: lines(9)}})

	m, _ = update(m, tab)
	m, _ = update(m, enter)

	if !strings.Contains(screenText(m), "row 9") || len(a.texts) != 1 {
		t.Errorf("sent %v:\n%s", a.texts, screenText(m))
	}
}

func TestAnExpandedFailureShowsEverything(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"make"}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "shell", Outcome: agent.CallFailed, Output: "error: " + lines(12)}})
	if strings.Contains(screenText(m), "row 12") {
		t.Fatal("the preview should cut the failure")
	}

	m, _ = update(m, tab)
	m, _ = update(m, enter)

	if !strings.Contains(screenText(m), "row 12") {
		t.Errorf("expanded failure:\n%s", screenText(m))
	}
}

// Choosing a card far above the window scrolls to it, and expanding a card below the window scrolls it into view.
func TestTheSelectedCardStaysInView(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 20)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "old", Name: "read_file", Arguments: `{"path":"old.go"}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "old", Name: "read_file", Output: "x"}})
	for i := 0; i < 30; i++ {
		m.addEntry(entry{role: roleNotice, text: fmt.Sprintf("filler %d", i)})
	}
	m.refresh()
	if strings.Contains(screenText(m), "old.go") {
		t.Fatal("the old card should be out of the window to begin with")
	}

	m.selectCard(m.toolCards()[0])
	if !strings.Contains(screenText(m), "old.go") {
		t.Errorf("the selected card is not in view:\n%s", screenText(m))
	}

	// A card below the window that fits comes fully into view when it is expanded...
	m.selectCard(-1)
	m.viewport.GotoBottom()
	m.addEntry(entry{role: roleTool, callID: "new", name: "read_file", args: `{"path":"new.go"}`, done: true, output: lines(10)})
	m.refresh()
	m.selectCard(m.toolCards()[1])
	m.toggleCard()
	if got := screenText(m); !strings.Contains(got, "new.go") || !strings.Contains(got, "row 10") {
		t.Errorf("the expanded card is not fully in view:\n%s", got)
	}

	// ...and one taller than the window shows its top.
	m.entries[m.cards.selected].output = lines(40)
	m.toggleCard()
	m.toggleCard()
	if got := screenText(m); !strings.Contains(got, "new.go") || !strings.Contains(got, "row 1 ") || strings.Contains(got, "row 40") {
		t.Errorf("a card taller than the window should show from its top:\n%s", got)
	}
}

// A card in the middle of the transcript that grows past the bottom of the window is scrolled up into view.
func TestExpandingACardAtTheBottomEdgeScrollsIt(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 20)
	for i := 0; i < 20; i++ {
		m.addEntry(entry{role: roleNotice, text: fmt.Sprintf("before %d", i)})
	}
	m.addEntry(entry{role: roleTool, callID: "mid", name: "read_file", args: `{"path":"mid.go"}`, done: true, output: lines(10)})
	for i := 0; i < 20; i++ {
		m.addEntry(entry{role: roleNotice, text: fmt.Sprintf("after %d", i)})
	}
	m.refresh()
	card := m.toolCards()[0]
	top, height := m.cardOffset(card)
	m.viewport.SetYOffset(top + height - m.viewport.Height()) // the card's last line is the window's last line
	m.selectCard(card)

	m.toggleCard()

	if got := screenText(m); !strings.Contains(got, "mid.go") || !strings.Contains(got, "row 10") {
		t.Errorf("the grown card is not in view:\n%s", got)
	}
}

func TestClearForgetsTheSelection(t *testing.T) {
	m := withCards(t, "a")
	m, _ = update(m, tab)

	m, _ = typeAndEnter(t, m, "/clear")

	if m.cards.selected != -1 {
		t.Errorf("card = %d after /clear", m.cards.selected)
	}
}

func TestGoldenExpandedCard(t *testing.T) {
	m := withCards(t, lines(14), "short")
	m, _ = update(m, shiftTab) // the newest
	m, _ = update(m, shiftTab) // the one before
	m, _ = update(m, enter)
	checkGolden(t, "expanded_card_100x40", screen(m))
}

func TestAFailedCardStaysClosedOnOneLineOfTheError(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"make"}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "shell", Outcome: agent.CallFailed, Output: "error: first line\nsecond line\nthird line"}})

	closed := ansi.Strip(m.entries[0].rendered)

	if !strings.Contains(closed, "ERROR") || !strings.Contains(closed, "first line") || strings.Contains(closed, "second line") {
		t.Errorf("a failed card shows the first line of the error without being opened:\n%s", closed)
	}
}

func TestCancelledAndDeniedCardsSayWhatHappenedInTheirHeader(t *testing.T) {
	for outcome, want := range map[agent.Outcome]string{agent.CallCancelled: "cancelled", agent.CallDenied: "denied by you"} {
		m := sized(&fakeAgent{}, 100, 40)
		m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "write", Arguments: `{}`}})
		m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "write", Outcome: outcome, Output: "error: whatever"}})

		card := ansi.Strip(m.entries[0].rendered)
		if strings.Count(card, "\n") != 0 || !strings.Contains(card, want) || strings.Contains(card, "ERROR") {
			t.Errorf("%s card:\n%s", want, card)
		}
	}
}

func TestACallWithNothingToShowHasNoLineCount(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "write", Arguments: `{}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "write", Output: "(no output)"}})

	if card := ansi.Strip(m.entries[0].rendered); strings.Contains(card, "line") {
		t.Errorf("card: %q", card)
	}
}

func TestTheMetaKeepsItsPlaceWhenTheCommandIsLong(t *testing.T) {
	m := sized(&fakeAgent{}, 80, 40)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"` + strings.Repeat("echo something long; ", 20) + `"}`}})
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "shell", Output: "a\nb\nc"}})

	header := strings.TrimRight(ansi.Strip(m.entries[0].rendered), " ")

	if !strings.HasSuffix(header, "3 lines") || !strings.Contains(header, "…") || ansi.StringWidth(header) > m.contentWidth() {
		t.Errorf("header (%d cells):\n%s", ansi.StringWidth(header), header)
	}
}

func TestTheServerOfAnMCPToolIsMutedInItsTitle(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "docs__search", Title: "docs → search", Arguments: `{}`}})

	card := m.entries[0].rendered

	if !strings.Contains(ansi.Strip(card), "docs → search") || strings.Contains(ansi.Strip(card), "docs__search") {
		t.Errorf("title: %q", ansi.Strip(card))
	}
	if !strings.Contains(card, m.sty.toolMeta.Render("docs → ")) || !strings.Contains(card, m.sty.toolName.Render("search")) {
		t.Errorf("the server and the tool should be styled apart: %q", card)
	}
}

// A run of calls reads as a list: no blank line between cards, a blank line around everything else.
func TestCardsInARunHaveNoBlankLinesBetweenThem(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m.addEntry(entry{role: roleAssistant, text: "Looking."})
	for i := 0; i < 3; i++ {
		m.addEntry(entry{role: roleTool, callID: fmt.Sprint(i), name: "read_file", args: fmt.Sprintf(`{"path":"f%d"}`, i), done: true, output: "x"})
	}
	m.addEntry(entry{role: roleAssistant, text: "Done."})
	m.refresh()

	var rows []string
	for _, l := range strings.Split(ansi.Strip(m.viewport.GetContent()), "\n") {
		rows = append(rows, strings.TrimSpace(l))
	}
	got := strings.Join(rows, "|")

	want := "Looking.||✓ read_file  f0       1 line|✓ read_file  f1       1 line|✓ read_file  f2       1 line||Done."
	if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
		t.Errorf("transcript rows:\n%s\nwant:\n%s", got, want)
	}
}

// The scroll position of a card is counted with the same gaps the transcript is drawn with.
func TestCardOffsetsMatchWhatIsDrawn(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 40)
	m.addEntry(entry{role: roleAssistant, text: "Intro."})
	for i := 0; i < 3; i++ {
		m.addEntry(entry{role: roleTool, callID: fmt.Sprint(i), name: "read_file", args: fmt.Sprintf(`{"path":"marker%d"}`, i), done: true, output: "x"})
	}
	m.addEntry(entry{role: roleAssistant, text: "Outro."})
	m.refresh()

	rows := strings.Split(ansi.Strip(m.viewport.GetContent()), "\n")
	for i := 0; i < 3; i++ {
		top, _ := m.cardOffset(m.toolCards()[i])
		if !strings.Contains(rows[top], fmt.Sprintf("marker%d", i)) {
			t.Errorf("card %d is said to start at row %d, which holds %q", i, top, rows[top])
		}
	}
}
