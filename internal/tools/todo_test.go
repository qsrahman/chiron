package tools

import (
	"context"
	"strings"
	"testing"
)

func steps(pairs ...string) []TodoItem {
	var items []TodoItem
	for i := 0; i < len(pairs); i += 2 {
		items = append(items, TodoItem{Content: pairs[i], Status: pairs[i+1]})
	}
	return items
}

func TestTheListIsReplacedWholeAndClearedByAnEmptyOne(t *testing.T) {
	var l TodoList
	if err := l.Set(steps("read", TodoCompleted, "fix", TodoInProgress)); err != nil {
		t.Fatal(err)
	}
	if err := l.Set(steps("only", TodoPending)); err != nil {
		t.Fatal(err)
	}
	if got := l.Items(); len(got) != 1 || got[0].Content != "only" {
		t.Errorf("items = %+v", got)
	}
	if err := l.Set(nil); err != nil || len(l.Items()) != 0 || l.Summary() != "Todos cleared" {
		t.Errorf("an empty list must clear it: %+v, %v, %q", l.Items(), err, l.Summary())
	}
}

func TestARefusedListLeavesTheOldOneAndSaysHowToFixIt(t *testing.T) {
	tests := []struct {
		name  string
		items []TodoItem
		want  string
	}{
		{"two in progress", steps("a", TodoInProgress, "b", TodoInProgress), "only one todo can be in_progress at a time, but 2 are"},
		{"empty content", steps("  ", TodoPending), `todo 1 has no "content"`},
		{"unknown status", steps("a", TodoPending, "b", "doing"), `todo 2 has the status "doing": use pending, in_progress, completed or cancelled`},
		{"too long", steps(strings.Repeat("x", maxTodoLength+1), TodoPending), "longer than 200 characters"},
		{"too many", make([]TodoItem, maxTodos+1), "at most 30 todos"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l TodoList
			if err := l.Set(steps("kept", TodoPending)); err != nil {
				t.Fatal(err)
			}
			before := l.Revision()

			err := l.Set(tt.items)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one with %q", err, tt.want)
			}
			if got := l.Items(); len(got) != 1 || got[0].Content != "kept" || l.Revision() != before {
				t.Errorf("a refused list changed the old one: %+v, revision %d", got, l.Revision())
			}
		})
	}
}

func TestEveryChangeIsCountedAndAnUnchangedListIsNot(t *testing.T) {
	var l TodoList
	_ = l.Set(nil)
	l.Clear()
	if l.Revision() != 0 {
		t.Errorf("clearing an empty list counted: %d", l.Revision())
	}
	_ = l.Set(steps("a", TodoPending))
	_ = l.Set(steps("a", TodoPending))
	if l.Revision() != 1 {
		t.Errorf("sending the same list again counted: %d", l.Revision())
	}
	_ = l.Set(steps("a", TodoCompleted))
	l.Clear()
	if l.Revision() != 3 {
		t.Errorf("revision = %d, want 3", l.Revision())
	}
}

func TestSummaryCountsTheStepsAndNamesTheActiveOne(t *testing.T) {
	var l TodoList
	_ = l.Set(steps("read", TodoCompleted, "fix the parser", TodoInProgress, "test", TodoPending, "docs", TodoPending, "old", TodoCancelled))

	want := "Todos updated: 1/5 completed, 1 in progress, 2 pending, 1 cancelled. Now: fix the parser"
	if got := l.Summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestNotesListTheStepsOnlyWhileOneIsOpen(t *testing.T) {
	var l TodoList
	if l.Notes() != "" {
		t.Error("an empty list has notes")
	}
	_ = l.Set(steps("read", TodoCompleted, "fix", TodoInProgress, "test", TodoPending, "old", TodoCancelled))
	notes := l.Notes()
	for _, want := range []string{"# Your todo list", "[x] read", "[>] fix", "[ ] test", "[-] old"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes are missing %q:\n%s", want, notes)
		}
	}
	_ = l.Set(steps("read", TodoCompleted, "old", TodoCancelled))
	if l.Notes() != "" {
		t.Error("a list with nothing left to do must say nothing")
	}
}

func TestRestoreTakesASavedListAndDropsOneThatDoesNotHoldUp(t *testing.T) {
	var l TodoList
	l.Restore(steps("a", TodoInProgress))
	if len(l.Items()) != 1 {
		t.Fatalf("items = %+v", l.Items())
	}
	l.Restore(steps("a", TodoInProgress, "b", TodoInProgress)) // two active: a hand-edited file
	if len(l.Items()) != 0 {
		t.Errorf("a bad saved list was used: %+v", l.Items())
	}
}

func TestTheTodoToolRunsWithoutAskingAndOneAtATime(t *testing.T) {
	tool := NewTodoTool(&TodoList{})

	trusted, ok := tool.(Trusted)
	if !ok || !trusted.Trusted() {
		t.Error("the todo tool must run without asking")
	}
	if tool.ReadOnly() {
		t.Error("the todo tool must not be read-only: two calls in one response must run in order")
	}
	if _, ok := tool.(Validator); !ok {
		t.Error("the todo tool lost the schema check")
	}
	if tool.Name() != "todo" {
		t.Errorf("name = %q", tool.Name())
	}
}

func TestTheTodoToolReplacesTheListAndAnswersWithTheSummary(t *testing.T) {
	var l TodoList
	tool := NewTodoTool(&l)

	out, err := tool.Run(context.Background(), `{"todos":[{"content":"read","status":"completed"},{"content":"fix","status":"in_progress"}]}`)

	if err != nil || out != "Todos updated: 1/2 completed, 1 in progress. Now: fix" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if len(l.Items()) != 2 {
		t.Errorf("items = %+v", l.Items())
	}
	if _, err := tool.Run(context.Background(), `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`); err == nil || len(l.Items()) != 2 {
		t.Errorf("two in_progress: %v, items %+v", err, l.Items())
	}
	if out, err := tool.Run(context.Background(), `{"todos":[]}`); err != nil || out != "Todos cleared" {
		t.Errorf("clear: %q, %v", out, err)
	}
}

// Models trained on other agents add fields of those agents' lists. The list they meant is clear, so the call is not
// refused for them.
func TestTheTodoToolIgnoresFieldsItDoesNotKnow(t *testing.T) {
	var l TodoList
	tool := NewTodoTool(&l)

	out, err := tool.Run(context.Background(), `{"todos":[{"content":"read","status":"in_progress","activeForm":"Reading","priority":"high"}],"note":"x"}`)

	if err != nil || !strings.Contains(out, "Now: read") || len(l.Items()) != 1 {
		t.Errorf("out = %q, err = %v, items = %+v", out, err, l.Items())
	}
	if err := tool.(Validator).Validate(`{"todos":[{"content":"read","status":"pending","activeForm":"Reading"}]}`); err != nil {
		t.Errorf("Validate refused an extra field: %v", err)
	}
}

func TestTheTodoToolRefusesACallThatCannotChangeTheListBeforeItRuns(t *testing.T) {
	tool := NewTodoTool(&TodoList{}).(Validator)

	for _, bad := range []string{`{"todos":"a"}`, `{"todos":[{"content":"a"}]}`, `{}`, `not json`, `{"todos":null}`} {
		if err := tool.Validate(bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	if err := tool.Validate(`{"todos":[{"content":"a","status":"pending"}]}`); err != nil {
		t.Errorf("a good call was refused: %v", err)
	}
}
