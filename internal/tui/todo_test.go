package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

func step(content, status string) agent.Todo { return agent.Todo{Content: content, Status: status} }

func manyTodos() []agent.Todo {
	items := []agent.Todo{step("read the code", "completed"), step("drop the old plan", "cancelled"), step("fix the parser", "in_progress")}
	for i := 1; i <= 7; i++ {
		items = append(items, step(fmt.Sprint("step ", i), "pending"))
	}
	return items
}

func TestThePanelListsTheOpenStepsAndCountsTheFinishedAndTheRest(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 30)
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: manyTodos()}})

	got := screen(m)
	for _, want := range []string{"todo · 1/10 done", "▸ fix the parser", "○ step 1", "○ step 4", "+ 3 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("the panel is missing %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"read the code", "step 5"} {
		if strings.Contains(got, not) {
			t.Errorf("the panel shows %q, which it should leave out:\n%s", not, got)
		}
	}
}

func TestThePanelIsHiddenWhenNothingIsLeftToDoAndWhenTheListIsCleared(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: []agent.Todo{step("a", "in_progress")}}})
	if !strings.Contains(screen(m), "todo · 0/1 done") {
		t.Fatalf("the panel did not open:\n%s", screen(m))
	}
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: []agent.Todo{step("a", "completed"), step("b", "cancelled")}}})
	if strings.Contains(screen(m), "todo ·") {
		t.Errorf("the panel stays after the last step is done:\n%s", screen(m))
	}
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: []agent.Todo{step("a", "pending")}}})
	m, _ = update(m, eventMsg{agent.TodoUpdated{}})
	if strings.Contains(screen(m), "todo ·") {
		t.Errorf("the panel stays after the list is cleared:\n%s", screen(m))
	}
}

func TestThePanelTakesItsRoomFromTheTranscriptAndFitsEveryWidth(t *testing.T) {
	for _, width := range []int{30, 60, 120} {
		m := sized(&fakeAgent{}, width, 24)
		before := m.viewport.Height()
		m, _ = update(m, eventMsg{agent.TodoUpdated{Items: manyTodos()}})

		if got := before - m.viewport.Height(); got != 7 { // the title, five steps and "+ 3 more"
			t.Errorf("width %d: the panel took %d rows from the transcript, want 7", width, got)
		}
		for _, line := range strings.Split(screen(m), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width %d: %q is %d cells wide", width, line, w)
			}
		}
		if rows := strings.Count(screen(m), "\n"); rows > 24 {
			t.Errorf("width %d: the screen is %d rows high", width, rows)
		}
	}
}

func TestTextFromTheModelInTheListCannotReachTheTerminal(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: []agent.Todo{step("clean\x1b]52;c;ZXZpbA==\x07 up\r", "in_progress")}}})

	view := m.View().Content
	if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\r") || strings.Contains(view, "\x07") {
		t.Error("an escape sequence from the model reached the screen")
	}
}

func TestTheTodoCommandPrintsTheWholeList(t *testing.T) {
	a := &fakeAgent{todos: manyTodos()}
	m, _ := typeAndEnter(t, newTestModel(a), "/todo")

	got := said(m)
	for _, want := range []string{"[x]", "read the code", "[-]", "drop the old plan", "[>]", "fix the parser", "[ ]", "step 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("/todo is missing %q:\n%s", want, got)
		}
	}
	m, _ = typeAndEnter(t, newTestModel(&fakeAgent{}), "/todo")
	if said(m) != "No todos" {
		t.Errorf("said %q", said(m))
	}
}

func TestAResumedConversationShowsItsListAtOnce(t *testing.T) {
	m := New(&fakeAgent{todos: []agent.Todo{step("fix", "in_progress")}}, Info{})
	if len(m.todoRows()) == 0 {
		t.Error("the list of a resumed session is not shown")
	}
}

func TestResetAndResumingASessionTakeTheListFromTheAgent(t *testing.T) {
	a := &fakeAgent{
		todos:        []agent.Todo{step("old plan", "in_progress")},
		sessions:     []agent.SessionInfo{{ID: "s-1", Turns: 1, Prompt: "hi", Current: false}},
		resumedTodos: []agent.Todo{step("saved plan", "pending")},
	}
	m := New(a, Info{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if !strings.Contains(screen(m), "old plan") {
		t.Fatalf("the list is not shown:\n%s", screen(m))
	}

	m, _ = typeAndEnter(t, m, "/sessions")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := screen(m); !strings.Contains(got, "saved plan") || strings.Contains(got, "old plan") {
		t.Errorf("after resuming:\n%s", got)
	}

	m, _ = typeAndEnter(t, m, "/reset")
	if strings.Contains(screen(m), "todo ·") {
		t.Errorf("the panel stays after /reset:\n%s", screen(m))
	}
}

func TestGoldenTodoPanel(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m, _ = typeAndEnter(t, m, "Fix the parser and update the docs")
	m, _ = update(m, eventMsg{agent.TodoUpdated{Items: manyTodos()[:5]}})
	checkGolden(t, "todo_100x24", screen(m))
}
