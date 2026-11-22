package agent

import "github.com/qsrahman/chiron/internal/tools"

// Todo is one step of the model's todo list.
type Todo = tools.TodoItem

// TodoTool is the tool the model calls to keep its todo list. It is bound to this agent's list, which lives as long
// as the agent. Get the registry with RegistryFor, which adds it.
func (a *Agent) TodoTool() tools.Tool { return tools.NewTodoTool(a.todos) }

// Todos returns the model's todo list as it is now.
func (a *Agent) Todos() []Todo { return a.todos.Items() }

// RegistryFor returns a registry of the given tools and the todo tool. With no other tool there is none, so that a
// chat without tools stays a plain chat, and the prompt that mentions the tool is used exactly when it is offered.
func (a *Agent) RegistryFor(list ...tools.Tool) (*tools.Registry, error) {
	if len(list) > 0 {
		list = append(list, a.TodoTool())
	}
	return tools.NewRegistry(list...)
}

// currentInstructions is what a request carries as instructions: the system prompt, the todo list while a step of it is
// open, and the notice to a model that repeats itself (see guard.go). The list goes last, so that the text before it can still be cached. The history keeps the tool calls of
// the newest turn only, so the instructions are the one place the list is sure to survive.
func (a *Agent) currentInstructions() string { return a.instructions + a.todos.Notes() + a.loopNote }
