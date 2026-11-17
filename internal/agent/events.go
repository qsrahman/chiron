package agent

// Event is something a front-end should show. The agent knows nothing about how it is rendered.
type Event interface{ isEvent() }

// TextDelta is a chunk of assistant text, in arrival order.
type TextDelta struct{ Text string }

// ToolStarted announces a tool call. Calls of one model turn may start in any order.
type ToolStarted struct {
	CallID    string
	Name      string
	Arguments string // raw JSON as sent by the model
}

// ToolFinished reports a tool call's result. Output is what the model sees, including error text.
type ToolFinished struct {
	CallID string
	Name   string
	Output string
	Failed bool
}

// Usage reports the token counts of one model response. Input plus output is roughly the size of the
// conversation the next request will start from.
type Usage struct{ InputTokens, OutputTokens int64 }

// Done marks a completed turn; it is the last event of a successful Send.
type Done struct{}

// Error marks a failed or cancelled turn; it is the last event of an unsuccessful Send.
// The conversation history is left as it was before the turn.
type Error struct{ Err error }

func (TextDelta) isEvent()    {}
func (ToolStarted) isEvent()  {}
func (ToolFinished) isEvent() {}
func (Usage) isEvent()        {}
func (Done) isEvent()         {}
func (Error) isEvent()        {}
