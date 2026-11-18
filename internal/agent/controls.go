package agent

// The methods below are what the slash commands use. They are called between turns, never while one runs: the
// agent is not safe for concurrent use.

// Reset forgets the whole conversation.
func (a *Agent) Reset() {
	a.history.reset()
	a.lastInput = 0
}

// Undo drops the last exchange and reports whether there was one.
func (a *Agent) Undo() bool {
	a.lastInput = 0 // what the server counted no longer holds
	return a.history.undo()
}

// Rewind drops the last turn if it was saved and returns its prompt to send again; false if there has not been
// one. A turn that failed before any tool ran was never saved, so only the prompt comes back.
func (a *Agent) Rewind() (string, bool) {
	a.lastInput = 0
	return a.history.rewind()
}

// HasConversation reports whether there is anything to lose: the conversation only lives in memory.
func (a *Agent) HasConversation() bool { return len(a.history.items) > 0 }

// Settings returns the current settings.
func (a *Agent) Settings() Settings { return a.settings }

// SetModel switches the model for the next request. What the server counted belongs to the model that counted it.
func (a *Agent) SetModel(model string) {
	a.settings.Model = model
	a.lastInput = 0
}

// SetReasoningEffort sets the reasoning effort for the next request; "" sends none.
func (a *Agent) SetReasoningEffort(effort string) { a.settings.ReasoningEffort = effort }
