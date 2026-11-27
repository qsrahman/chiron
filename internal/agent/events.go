package agent

// Event is something a front-end should show. The agent knows nothing about how it is rendered.
type Event interface{ isEvent() }

// TextDelta is a chunk of assistant text, in arrival order.
type TextDelta struct{ Text string }

// ToolStarted announces a tool call. Calls of one model turn may start in any order.
type ToolStarted struct {
	CallID    string
	Name      string
	Title     string // what to show instead of Name when the tool has a name for people; empty otherwise
	Arguments string // raw JSON as sent by the model
}

// Outcome is how a tool call ended.
type Outcome int

const (
	CallOK        Outcome = iota
	CallFailed            // an error, whose text the model read
	CallCancelled         // the turn was cancelled while the call ran or before it ran: shown as interrupted, not as an error
	CallDenied            // the user refused it, and the model was told so
)

// ToolFinished reports a tool call's result. Output is what the model sees, including error text.
type ToolFinished struct {
	CallID  string
	Name    string
	Output  string
	Outcome Outcome
}

// Decision is the user's answer to an approval request. The zero value refuses, so an answer that was never given
// can never allow a call.
type Decision int

const (
	Deny Decision = iota
	Allow
	AllowSession // allow this call and every later one with the same grant key
)

// ApprovalRequested asks the user to allow a call to a tool that is not read-only. The turn waits until a
// Decision is sent on Reply, or its context is cancelled. Reply has room for the answer, so sending never blocks.
type ApprovalRequested struct {
	CallID   string
	Tool     string
	Summary  string // one line
	Details  string // what is about to happen; may hold any character, so show it with care
	GrantKey string // what Allow for session covers besides the tool; empty covers the tool
	Source   string // where the tool comes from when it is not built in, for the user to judge; may be empty
	Reply    chan<- Decision
}

// Usage reports the token counts of one model response. Input plus output is roughly the size of the
// conversation the next request will start from.
type Usage struct{ InputTokens, OutputTokens int64 }

// TodoUpdated reports that the model changed its todo list, which is Items as it is now; an empty list was cleared.
type TodoUpdated struct{ Items []Todo }

// Retry reports that a request is being sent again: a stream broke, the server reported a passing fault or that the
// request was too long, or the model sent an empty reply. A
// front-end should drop the text it streamed for the attempt that failed, since the reply starts over.
type Retry struct{ Reason string }

// Notice tells the user something the agent did on its own: it summarised, or shortened results to fit the budget.
type Notice struct{ Text string }

// Done marks a completed turn; it is the last event of a successful Send.
type Done struct{}

// Error marks a failed or cancelled turn; it is the last event of an unsuccessful Send.
// A turn that failed before any tool ran leaves the history as it was; one that stopped after tool work is kept.
type Error struct{ Err error }

func (TextDelta) isEvent()         {}
func (ToolStarted) isEvent()       {}
func (ToolFinished) isEvent()      {}
func (ApprovalRequested) isEvent() {}
func (Usage) isEvent()             {}
func (TodoUpdated) isEvent()       {}
func (Retry) isEvent()             {}
func (Notice) isEvent()            {}
func (Done) isEvent()              {}
func (Error) isEvent()             {}
