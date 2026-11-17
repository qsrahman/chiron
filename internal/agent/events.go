package agent

// Event is something a front-end should show. The agent knows nothing about how it is rendered.
type Event interface{ isEvent() }

// TextDelta is a chunk of assistant text, in arrival order.
type TextDelta struct{ Text string }

// Done marks a completed turn; it is the last event of a successful Send.
type Done struct{}

// Error marks a failed or cancelled turn; it is the last event of an unsuccessful Send.
// The conversation history is left as it was before the turn.
type Error struct{ Err error }

func (TextDelta) isEvent() {}
func (Done) isEvent()      {}
func (Error) isEvent()     {}
