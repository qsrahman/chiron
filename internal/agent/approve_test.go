package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/qsrahman/chiron/internal/tools"
)

// withPrompt gives a typed tool its own prompt, and keeps the validation the typed tool has.
type withPrompt struct {
	tools.Tool
	prompt func(arguments string) tools.Prompt
}

func (d withPrompt) Describe(arguments string) tools.Prompt { return d.prompt(arguments) }
func (d withPrompt) Validate(arguments string) error {
	return d.Tool.(tools.Validator).Validate(arguments)
}

// writer is a mutating tool whose prompt names what it is about to do; calls with the same name share a grant key.
func writer(log *recorder) tools.Tool {
	return withPrompt{
		Tool: fakeTool("write", false, 0, log),
		prompt: func(arguments string) tools.Prompt {
			name := strings.Trim(strings.TrimPrefix(arguments, `{"name":`), `}"`)
			return tools.Prompt{Summary: "Write " + name, Details: "details of " + name, GrantKey: name}
		},
	}
}

// approvalSetup returns an agent that asks, since the shared settings allow everything.
func approvalSetup(t *testing.T, srv *fakeServer, toolList ...tools.Tool) *Agent {
	t.Helper()
	a := newAgentWithTools(t, srv, toolList...)
	a.settings.AutoApprove = false
	return a
}

// answering reads a turn's events and answers every approval request with decide. It returns the events and the
// requests that were asked.
func answering(t *testing.T, events <-chan Event, decide func(ApprovalRequested) Decision) ([]Event, []ApprovalRequested) {
	t.Helper()
	var all []Event
	var asked []ApprovalRequested
	for e := range events {
		all = append(all, e)
		if req, ok := e.(ApprovalRequested); ok {
			asked = append(asked, req)
			req.Reply <- decide(req)
		}
	}
	return all, asked
}

func always(d Decision) func(ApprovalRequested) Decision {
	return func(ApprovalRequested) Decision { return d }
}

func finished(events []Event, callID string) ToolFinished {
	for _, e := range events {
		if f, ok := e.(ToolFinished); ok && f.CallID == callID {
			return f
		}
	}
	return ToolFinished{}
}

func TestZeroDecisionDenies(t *testing.T) {
	var d Decision
	if d != Deny {
		t.Error("an answer that was never given must not allow a call")
	}
}

func TestAllowRunsTheCallAfterAsking(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := approvalSetup(t, srv, writer(log))

	events, asked := answering(t, a.Send(context.Background(), "go"), always(Allow))

	if len(asked) != 1 {
		t.Fatalf("asked %d times, want once", len(asked))
	}
	req := asked[0]
	if req.CallID != "c1" || req.Tool != "write" || req.Summary != "Write a.txt" || req.Details != "details of a.txt" || req.GrantKey != "a.txt" {
		t.Errorf("request = %+v", req)
	}
	if f := finished(events, "c1"); f.Outcome != CallOK || f.Output != "result a.txt" {
		t.Errorf("finished = %+v", f)
	}
	if len(a.grants) != 0 {
		t.Error("Allow once must not be remembered")
	}
}

func TestDenyDoesNotRunTheCallAndTellsTheModel(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := approvalSetup(t, srv, writer(log))

	events, _ := answering(t, a.Send(context.Background(), "go"), always(Deny))

	if len(log.log) != 0 {
		t.Errorf("the tool ran although the call was denied: %v", log.log)
	}
	if f := finished(events, "c1"); f.Outcome != CallDenied {
		t.Errorf("finished = %+v, want a denied call", f)
	}
	outs := outputsOf(t, srv.request(t, 1))
	if len(outs) != 1 || outs[0]["output"] != deniedOutput {
		t.Errorf("the model read %v, want the denial text", outs)
	}
	if !strings.Contains(deniedOutput, "do not retry") {
		t.Error("the denial text must tell the model not to retry")
	}
}

func TestAllowForSessionIsScopedByGrantKey(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t,
		append(callsThenAnswer(
			functionCall("c1", "write", nameArgs{"a.txt"}),
			functionCall("c2", "write", nameArgs{"a.txt"}),
			functionCall("c3", "write", nameArgs{"b.txt"})),
			callsThenAnswer(functionCall("c4", "write", nameArgs{"a.txt"}))...)...)
	a := approvalSetup(t, srv, writer(log))

	_, asked := answering(t, a.Send(context.Background(), "one"), func(r ApprovalRequested) Decision {
		if r.GrantKey == "a.txt" {
			return AllowSession
		}
		return Allow
	})
	if got := len(asked); got != 2 {
		t.Fatalf("asked %d times, want 2: a.txt once (the second call is covered) and b.txt", got)
	}

	// The grant survives a reset and the next turn.
	a.Reset()
	_, asked = answering(t, a.Send(context.Background(), "two"), always(Deny))
	if len(asked) != 0 {
		t.Errorf("asked %d times for a call that was allowed for the session", len(asked))
	}

	list := a.ToolList()
	if len(list.Allowed) != 1 || list.Allowed[0] != "write: a.txt" || len(list.Ask) != 1 || list.Ask[0] != "write" {
		t.Errorf("ToolList = %+v", list)
	}
}

func TestInvalidAndUnknownCallsAreNotPutToTheUser(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(
		functionCall("c1", "write", map[string]any{}), // the required name is missing
		functionCall("c2", "nothing", nameArgs{"x"}))...)
	a := approvalSetup(t, srv, writer(log))

	events, asked := answering(t, a.Send(context.Background(), "go"), always(Allow))

	if len(asked) != 0 {
		t.Errorf("the user was asked %d times about calls that could not run", len(asked))
	}
	if f := finished(events, "c1"); f.Outcome != CallFailed || !strings.Contains(f.Output, "invalid arguments") {
		t.Errorf("invalid call: %+v", f)
	}
	if f := finished(events, "c2"); f.Outcome != CallFailed || !strings.Contains(f.Output, "unknown tool") {
		t.Errorf("unknown call: %+v", f)
	}
}

func TestReadOnlyCallsNeverAsk(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "read", nameArgs{"a"}))...)
	a := approvalSetup(t, srv, fakeTool("read", true, 0, log))

	_, asked := answering(t, a.Send(context.Background(), "go"), always(Deny))

	if len(asked) != 0 || len(log.log) == 0 {
		t.Errorf("asked %d times, ran %v", len(asked), log.log)
	}
}

func TestAutoApproveAllowsWithoutAsking(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := newAgentWithTools(t, srv, writer(log)) // the shared settings allow everything

	_, asked := answering(t, a.Send(context.Background(), "go"), always(Deny))

	if len(asked) != 0 || len(log.log) != 2 {
		t.Errorf("asked %d times, log %v", len(asked), log.log)
	}
}

func TestCancelClosesAnOpenPromptAndReportsTheCallCancelled(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := approvalSetup(t, srv, writer(log))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var all []Event
	for e := range a.Send(ctx, "go") {
		all = append(all, e)
		if _, ok := e.(ApprovalRequested); ok {
			cancel() // the user pressed Ctrl-C with the prompt open
		}
	}

	f := finished(all, "c1")
	if f.Outcome != CallCancelled || len(log.log) != 0 {
		t.Errorf("finished = %+v, log %v: want cancelled, not denied, and not run", f, log.log)
	}
	if !isCancel(lastError(t, all)) {
		t.Errorf("the turn ended with %v, want a cancellation", lastEvent(all))
	}
}

func TestPromptWithoutADescriberShowsTheArguments(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := approvalSetup(t, srv, fakeTool("write", false, 0, log))

	_, asked := answering(t, a.Send(context.Background(), "go"), always(Deny))

	if len(asked) != 1 || asked[0].Summary != "Call write" || asked[0].GrantKey != "" || asked[0].Details != "name: a.txt" {
		t.Errorf("asked = %+v", asked)
	}
}

// trusted is a tool the configuration allows; titled has a name for people; both keep the typed tool's validation.
type trusted struct{ tools.Tool }

func (trusted) Trusted() bool { return true }

type titled struct {
	tools.Tool
	title string
}

func (t titled) Title() string { return t.title }

// Describe and Validate pass through, since embedding the interface would hide them.
func (t titled) Describe(arguments string) tools.Prompt {
	return t.Tool.(tools.Describer).Describe(arguments)
}
func (t titled) Validate(arguments string) error { return t.Tool.(tools.Validator).Validate(arguments) }

func TestATrustedToolRunsWithoutAskingAndIsListedAsFree(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "write", nameArgs{"a.txt"}))...)
	a := approvalSetup(t, srv, trusted{fakeTool("write", false, 0, log)})

	_, asked := answering(t, a.Send(context.Background(), "go"), always(Deny))

	if len(asked) != 0 || len(log.log) != 2 {
		t.Errorf("asked %d times, log %v: a trusted tool runs without asking", len(asked), log.log)
	}
	if list := a.ToolList(); len(list.Free) != 1 || len(list.Ask) != 0 {
		t.Errorf("ToolList = %+v, want the tool listed as running without asking", list)
	}
}

func TestToolStartedCarriesTheTitleAndTheSourceReachesThePrompt(t *testing.T) {
	log := &recorder{}
	inner := withPrompt{Tool: fakeTool("srv__write", false, 0, log), prompt: func(string) tools.Prompt {
		return tools.Prompt{Summary: "srv → write", Details: "d", Source: "npx -y thing"}
	}}
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "srv__write", nameArgs{"a"}))...)
	a := approvalSetup(t, srv, titled{Tool: inner, title: "srv → write"})

	events, asked := answering(t, a.Send(context.Background(), "go"), always(Allow))

	var started ToolStarted
	for _, e := range events {
		if s, ok := e.(ToolStarted); ok {
			started = s
		}
	}
	if started.Name != "srv__write" || started.Title != "srv → write" {
		t.Errorf("started = %+v", started)
	}
	if len(asked) != 1 || asked[0].Source != "npx -y thing" {
		t.Errorf("asked = %+v", asked)
	}
}
