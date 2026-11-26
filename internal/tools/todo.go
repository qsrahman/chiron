package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The statuses of a step.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
	TodoCancelled  = "cancelled"
)

const (
	// maxTodos and maxTodoLength keep the list small: while a step is open the whole list is part of every request.
	maxTodos      = 30
	maxTodoLength = 200
)

// TodoItem is one step of the list.
type TodoItem struct {
	Content string `json:"content" jsonschema:"what to do, in a few words"`
	Status  string `json:"status" jsonschema:"pending, in_progress, completed or cancelled"`
}

// TodoList is the model's checklist for a task with several steps. It only gives the model a place to write the steps
// down and the user something to see; nothing here acts on them. Every update sends the whole list, which replaces the
// old one, so there are no ids to keep in step.
//
// It is not safe for concurrent use: the tool changes it while the agent waits, and the agent reads it between turns.
type TodoList struct {
	items    []TodoItem
	revision int // counts the changes, so that whoever shows the list knows it changed
}

// Items returns a copy of the list.
func (l *TodoList) Items() []TodoItem { return append([]TodoItem(nil), l.items...) }

// Revision counts the changes made to the list.
func (l *TodoList) Revision() int { return l.revision }

// Set replaces the list. A list that is refused leaves the old one as it was, and the error says how to fix the call.
// Sending the list that is already there changes nothing.
func (l *TodoList) Set(items []TodoItem) error {
	if err := validateTodos(items); err != nil {
		return err
	}
	l.replace(items)
	return nil
}

func (l *TodoList) replace(items []TodoItem) {
	if slices.Equal(l.items, items) {
		return
	}
	l.items = append([]TodoItem(nil), items...)
	l.revision++
}

// Restore replaces the list with one saved with a session. A saved list that does not hold up is not used.
func (l *TodoList) Restore(items []TodoItem) {
	if validateTodos(items) != nil {
		items = nil
	}
	l.replace(items)
}

// Clear forgets the list, as /reset forgets the conversation.
func (l *TodoList) Clear() { l.Restore(nil) }

func validateTodos(items []TodoItem) error {
	if len(items) > maxTodos {
		return fmt.Errorf("at most %d todos, but there are %d: merge steps or drop the finished ones", maxTodos, len(items))
	}
	active := 0
	for i, item := range items {
		switch {
		case strings.TrimSpace(item.Content) == "":
			return fmt.Errorf(`todo %d has no "content": give every step its text in the content field`, i+1)
		case utf8.RuneCountInString(item.Content) > maxTodoLength:
			return fmt.Errorf("todo %d is longer than %d characters: shorten it", i+1, maxTodoLength)
		}
		switch item.Status {
		case TodoPending, TodoCompleted, TodoCancelled:
		case TodoInProgress:
			active++
		default:
			return fmt.Errorf("todo %d has the status %q: use pending, in_progress, completed or cancelled", i+1, item.Status)
		}
	}
	if active > 1 {
		return fmt.Errorf("only one todo can be in_progress at a time, but %d are: finish one, or set the others back to pending, then send the list again", active)
	}
	return nil
}

// Open reports whether any step is still to do.
func (l *TodoList) Open() bool { return TodosOpen(l.items) }

// TodosOpen reports whether any step of the list is still to do.
func TodosOpen(items []TodoItem) bool {
	for _, item := range items {
		if item.Status == TodoPending || item.Status == TodoInProgress {
			return true
		}
	}
	return false
}

// TodoMark is how a status is drawn in a checklist of plain text.
func TodoMark(status string) string {
	switch status {
	case TodoInProgress:
		return "[>]"
	case TodoCompleted:
		return "[x]"
	case TodoCancelled:
		return "[-]"
	}
	return "[ ]"
}

// Summary is the list in one line: how much is done, and what is being worked on. It is what the tool answers.
func (l *TodoList) Summary() string {
	if len(l.items) == 0 {
		return "Todos cleared"
	}
	counts := map[string]int{}
	for _, item := range l.items {
		counts[item.Status]++
	}
	parts := []string{fmt.Sprintf("%d/%d completed", counts[TodoCompleted], len(l.items))}
	for _, status := range []string{TodoInProgress, TodoPending, TodoCancelled} {
		if n := counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(status, "_", " ")))
		}
	}
	text := "Todos updated: " + strings.Join(parts, ", ")
	for _, item := range l.items {
		if item.Status == TodoInProgress {
			text += ". Now: " + item.Content
		}
	}
	return text
}

// Notes is what goes into the instructions of every request while a step is open, and "" otherwise. The history keeps
// the tool calls of the newest turn only, and a summary drops more, so the list would otherwise be forgotten.
func (l *TodoList) Notes() string {
	if !l.Open() {
		return ""
	}
	var lines []string
	for _, item := range l.items {
		lines = append(lines, TodoMark(item.Status)+" "+item.Content)
	}
	return "\n\n# Your todo list\n\nThese are your own notes for the task in progress. Keep them up to date with the todo tool.\n\n" +
		strings.Join(lines, "\n")
}

type todoArgs struct {
	Todos []TodoItem `json:"todos" jsonschema:"the whole list; it replaces the previous one, and an empty list clears it"`
}

const todoDescription = `Keep a checklist for a task with three or more steps, or when the user gives you several tasks. Skip it for a simple task or a plain question.

Send the whole list on every call: it replaces the previous one, and an empty list clears it. Mark a step in_progress when you start it (only one at a time) and completed as soon as it is done; use cancelled for a step you drop. Call it only when the list changes. The user sees the list, so do not repeat it in your reply.`

// todoTool is the todo tool. It changes only the program's own notes, so it never asks the user; it is not read-only,
// so that two calls in one response run one after the other, in the order the model gave them.
//
// It reads its arguments itself, and ignores fields it does not know. Models trained on other agents add fields of
// those agents' lists (activeForm is one), and a refusal for that would cost a round for nothing: the list the model
// meant is clear. The schema it is shown stays strict about what is wanted.
type todoTool struct {
	Tool // for the name, the definition and ReadOnly
	list *TodoList
}

func (todoTool) Trusted() bool { return true }

func parseTodoArgs(arguments string) ([]TodoItem, error) {
	var args struct {
		Todos *[]TodoItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, fmt.Errorf("reading the list: %w", err)
	}
	if args.Todos == nil {
		return nil, errors.New(`the "todos" list is missing`)
	}
	if err := validateTodos(*args.Todos); err != nil {
		return nil, err
	}
	return *args.Todos, nil
}

// Validate refuses a call that cannot change the list, with the reason, before it runs.
func (todoTool) Validate(arguments string) error {
	_, err := parseTodoArgs(arguments)
	return err
}

// Run replaces the list and answers with the summary.
func (t todoTool) Run(_ context.Context, arguments string) (string, error) {
	items, err := parseTodoArgs(arguments)
	if err != nil {
		return "", err
	}
	if err := t.list.Set(items); err != nil {
		return "", err
	}
	return t.list.Summary(), nil
}

// NewTodoTool returns the tool the model calls to replace the list.
func NewTodoTool(list *TodoList) Tool {
	definition := MustNew("todo", todoDescription, false, func(context.Context, todoArgs) (string, error) { return "", nil })
	return todoTool{Tool: definition, list: list}
}
