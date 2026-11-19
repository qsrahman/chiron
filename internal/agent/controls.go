package agent

import (
	"slices"

	"github.com/qsrahman/chiron/internal/tools"
)

// The methods below are what the slash commands use. They are called between turns, never while one runs: the
// agent is not safe for concurrent use.

// Reset forgets the whole conversation.
func (a *Agent) Reset() {
	a.history.reset()
	a.lastInput = 0
	a.summaryFailures = 0
	if a.saving != nil {
		a.saving.id, a.saving.title = "", "" // the old file stays; the next conversation gets its own
	}
}

// Undo drops the last exchange and reports whether there was one.
func (a *Agent) Undo() bool {
	a.lastInput = 0 // what the server counted no longer holds
	undone := a.history.undo()
	a.saveSession()
	return undone
}

// Rewind drops the last turn if it was saved and returns its prompt to send again; false if there has not been
// one. A turn that failed before any tool ran was never saved, so only the prompt comes back.
func (a *Agent) Rewind() (string, bool) {
	a.lastInput = 0
	prompt, ok := a.history.rewind()
	a.saveSession()
	return prompt, ok
}

// HasConversation reports whether there is anything to lose: the conversation only lives in memory.
func (a *Agent) HasConversation() bool { return len(a.history.items) > 0 || a.history.summary != "" }

// SetTools replaces the tools and the instructions that describe them, when MCP servers have connected or been
// reconnected. Like the other settings it changes only between turns.
func (a *Agent) SetTools(registry *tools.Registry, instructions string) {
	a.tools, a.instructions = registry, instructions
	a.lastInput = 0
}

// Settings returns the current settings.
func (a *Agent) Settings() Settings { return a.settings }

// SetModel switches the model for the next request. What the server counted belongs to the model that counted it.
func (a *Agent) SetModel(model string) {
	if model != a.settings.Model {
		// Reasoning items only make sense to the model that wrote them, as for a resumed session.
		a.history.items = slices.DeleteFunc(a.history.items, func(it item) bool { return it.kind == kindReasoning })
	}
	a.settings.Model = model
	a.lastInput = 0
}

// SetReasoningEffort sets the reasoning effort for the next request; "" sends none.
func (a *Agent) SetReasoningEffort(effort string) { a.settings.ReasoningEffort = effort }
