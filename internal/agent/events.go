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
	// Cancelled is set when the call failed because the turn was cancelled, so front-ends can show it
	// as interrupted rather than as an error.
	Cancelled bool
}

// Usage reports the token counts of one model response. Input plus output is roughly the size of the
// conversation the next request will start from.
type Usage struct{ InputTokens, OutputTokens int64 }

// Retry reports that a request is being sent again: a stream broke, or the model sent an empty reply. A
// front-end should drop the text it streamed for the attempt that failed, since the reply starts over.
type Retry struct{ Reason string }

// Done marks a completed turn; it is the last event of a successful Send.
type Done struct{}

// Error marks a failed or cancelled turn; it is the last event of an unsuccessful Send.
// A turn that failed before any tool ran leaves the history as it was; one that stopped after tool work is kept.
type Error struct{ Err error }

func (TextDelta) isEvent()    {}
func (ToolStarted) isEvent()  {}
func (ToolFinished) isEvent() {}
func (Usage) isEvent()        {}
func (Retry) isEvent()        {}
func (Done) isEvent()         {}
func (Error) isEvent()        {}
