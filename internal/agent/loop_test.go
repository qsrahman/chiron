package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/desk-agent/internal/tools"
)

// --- fixtures ---

// functionCall is a function_call output item as the API sends it.
func functionCall(callID, name string, args any) string {
	arguments, _ := json.Marshal(args)
	quoted, _ := json.Marshal(string(arguments))
	return fmt.Sprintf(`{"type":"function_call","id":"fc_%s","call_id":%q,"name":%q,"arguments":%s,"status":"completed"}`,
		callID, callID, name, quoted)
}

// completedWith builds a response.completed event holding the given output items.
func completedWith(items ...string) string {
	return fmt.Sprintf(`{"type":"response.completed","sequence_number":9,"response":{
		"id":"resp_x","object":"response","status":"completed","output":[%s]}}`, strings.Join(items, ","))
}

// callsThenAnswer scripts a model that makes the given calls, then answers "done".
func callsThenAnswer(calls ...string) []string {
	return []string{sse(completedWith(calls...)), sse(textDelta("done"), completed("done"))}
}

type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *recorder) index(t *testing.T, s string) int {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range r.log {
		if l == s {
			return i
		}
	}
	t.Fatalf("%q not in log %v", s, r.log)
	return -1
}

type nameArgs struct {
	Name string `json:"name"`
}

// fakeTool logs its start and end and takes delay to finish, or until ctx is cancelled.
func fakeTool(name string, readOnly bool, delay time.Duration, log *recorder) tools.Tool {
	return tools.MustNew(name, "fake "+name, readOnly, func(ctx context.Context, a nameArgs) (string, error) {
		log.add("start " + a.Name)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		log.add("end " + a.Name)
		return "result " + a.Name, nil
	})
}

func newAgentWithTools(t *testing.T, srv *fakeServer, toolList ...tools.Tool) *Agent {
	t.Helper()
	reg, err := tools.NewRegistry(toolList...)
	if err != nil {
		t.Fatal(err)
	}
	return New(srv.client(), "m", "", reg)
}

// outputsOf returns the function_call_output items of a request, in order.
func outputsOf(t *testing.T, req map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, item := range inputItems(t, req) {
		if item["type"] == "function_call_output" {
			out = append(out, item)
		}
	}
	return out
}

func withoutUsage(events []Event) []Event {
	var out []Event
	for _, e := range events {
		if _, ok := e.(Usage); !ok {
			out = append(out, e)
		}
	}
	return out
}

// --- tests ---

func TestToolCallRoundTrip(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("call_1", "read", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, log))

	events := withoutUsage(drain(t, a.Send(context.Background(), "go")))

	// Events: the call starts and finishes, then the model's final answer streams.
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, fmt.Sprintf("%T", e))
	}
	want := []string{"agent.ToolStarted", "agent.ToolFinished", "agent.TextDelta", "agent.Done"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", kinds, want)
	}
	started := events[0].(ToolStarted)
	finished := events[1].(ToolFinished)
	if started.CallID != "call_1" || started.Name != "read" || started.Arguments != `{"name":"a"}` {
		t.Errorf("ToolStarted = %+v", started)
	}
	if finished.Output != "result a" || finished.Failed {
		t.Errorf("ToolFinished = %+v", finished)
	}

	// The first request advertises the tool; the second carries the call and its output.
	toolList, _ := srv.request(t, 0)["tools"].([]any)
	if len(toolList) != 1 || toolList[0].(map[string]any)["name"] != "read" {
		t.Errorf("tools sent = %v", srv.request(t, 0)["tools"])
	}
	outs := outputsOf(t, srv.request(t, 1))
	if len(outs) != 1 || outs[0]["call_id"] != "call_1" || outs[0]["output"] != "result a" {
		t.Errorf("function_call_output items = %v", outs)
	}
	var sawCall bool
	for _, item := range inputItems(t, srv.request(t, 1)) {
		sawCall = sawCall || item["type"] == "function_call"
	}
	if !sawCall {
		t.Error("the model's function_call must be replayed next to its output")
	}
}

func TestReadOnlyCallsRunConcurrently(t *testing.T) {
	log := &recorder{}
	const delay = 300 * time.Millisecond
	srv := newFakeServer(t, callsThenAnswer(
		functionCall("c1", "read", nameArgs{"a"}),
		functionCall("c2", "read", nameArgs{"b"}),
		functionCall("c3", "read", nameArgs{"c"}),
	)...)
	a := newAgentWithTools(t, srv, fakeTool("read", true, delay, log))

	begin := time.Now()
	events := drain(t, a.Send(context.Background(), "go"))
	elapsed := time.Since(begin)

	if _, ok := events[len(events)-1].(Done); !ok {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
	if elapsed >= 2*delay {
		t.Errorf("three %v calls took %v: they did not run concurrently", delay, elapsed)
	}
	// Outputs go back in call order no matter which call finished first.
	outs := outputsOf(t, srv.request(t, 1))
	for i, id := range []string{"c1", "c2", "c3"} {
		if outs[i]["call_id"] != id || outs[i]["output"] != "result "+string('a'+rune(i)) {
			t.Errorf("output %d = %v, want call %s", i, outs[i], id)
		}
	}
}

func TestMutatingCallsRunOneAtATimeInOrder(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(
		functionCall("c1", "write", nameArgs{"m1"}),
		functionCall("c2", "write", nameArgs{"m2"}),
		functionCall("c3", "write", nameArgs{"m3"}),
	)...)
	a := newAgentWithTools(t, srv, fakeTool("write", false, 30*time.Millisecond, log))

	drain(t, a.Send(context.Background(), "go"))

	want := []string{"start m1", "end m1", "start m2", "end m2", "start m3", "end m3"}
	if strings.Join(log.log, ",") != strings.Join(want, ",") {
		t.Errorf("log = %v, want %v", log.log, want)
	}
}

func TestReadOnlyRunsAreSeparatedByMutatingCalls(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(
		functionCall("c1", "read", nameArgs{"r1"}),
		functionCall("c2", "read", nameArgs{"r2"}),
		functionCall("c3", "write", nameArgs{"w"}),
		functionCall("c4", "read", nameArgs{"r3"}),
	)...)
	a := newAgentWithTools(t, srv,
		fakeTool("read", true, 100*time.Millisecond, log),
		fakeTool("write", false, 20*time.Millisecond, log))

	drain(t, a.Send(context.Background(), "go"))

	// r1 and r2 overlap; the write waits for both; r3 waits for the write.
	if log.index(t, "start r2") > log.index(t, "end r1") {
		t.Errorf("r1 and r2 should overlap: %v", log.log)
	}
	if log.index(t, "start w") < max(log.index(t, "end r1"), log.index(t, "end r2")) {
		t.Errorf("the write started before the earlier reads finished: %v", log.log)
	}
	if log.index(t, "start r3") < log.index(t, "end w") {
		t.Errorf("a read after the write started before it finished: %v", log.log)
	}
}

func TestToolFailuresGoBackToTheModel(t *testing.T) {
	failing := tools.MustNew("fail", "always fails", true, func(context.Context, nameArgs) (string, error) {
		return "", fmt.Errorf("boom")
	})
	quiet := tools.MustNew("quiet", "returns nothing", true, func(context.Context, nameArgs) (string, error) {
		return "", nil
	})
	srv := newFakeServer(t, callsThenAnswer(
		functionCall("c1", "fail", nameArgs{"x"}),
		functionCall("c2", "nope", nameArgs{"x"}),
		functionCall("c3", "quiet", map[string]any{"bogus": 1}),
		functionCall("c4", "quiet", nameArgs{"x"}),
	)...)
	a := newAgentWithTools(t, srv, failing, quiet)

	events := drain(t, a.Send(context.Background(), "go"))

	if _, ok := events[len(events)-1].(Done); !ok {
		t.Fatalf("the turn should still finish, got %#v", events[len(events)-1])
	}
	outs := outputsOf(t, srv.request(t, 1))
	wants := []string{"error: boom", `error: unknown tool "nope"`, "error: invalid arguments", "(no output)"}
	for i, want := range wants {
		if got, _ := outs[i]["output"].(string); !strings.HasPrefix(got, want) {
			t.Errorf("output %d = %q, want prefix %q", i, got, want)
		}
	}
	// Calls in one concurrent group finish in any order, so compare by call id.
	failed := map[string]bool{}
	for _, e := range events {
		if f, ok := e.(ToolFinished); ok {
			failed[f.CallID] = f.Failed
		}
	}
	wantFailed := map[string]bool{"c1": true, "c2": true, "c3": true, "c4": false}
	for id, want := range wantFailed {
		if failed[id] != want {
			t.Errorf("Failed[%s] = %v, want %v", id, failed[id], want)
		}
	}
}

func TestToolCallsStayInHistoryAcrossTurns(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t,
		sse(completedWith(functionCall("c1", "read", nameArgs{"a"}))),
		sse(textDelta("first"), completed("first")),
		sse(textDelta("second"), completed("second")),
	)
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, log))

	drain(t, a.Send(context.Background(), "one"))
	drain(t, a.Send(context.Background(), "two"))

	var types []string
	for _, item := range inputItems(t, srv.request(t, 2)) {
		if ty, ok := item["type"].(string); ok {
			types = append(types, ty)
		} else {
			types = append(types, "user-message")
		}
	}
	want := "user-message,function_call,function_call_output,reasoning,message,user-message"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("history sent on turn two = %s, want %s", got, want)
	}
}

func TestStepLimitStopsRunawayToolLoop(t *testing.T) {
	// Every call returns something new, so this is not a repeat and only the step limit can stop it.
	var n atomic.Int32
	counter := tools.MustNew("count", "returns a different number each time", true,
		func(context.Context, nameArgs) (string, error) { return fmt.Sprint(n.Add(1)), nil })
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "count", nameArgs{"a"}))))
	a := newAgentWithTools(t, srv, counter)

	events := drain(t, a.Send(context.Background(), "go"))

	last, ok := events[len(events)-1].(Error)
	if !ok || !strings.Contains(last.Err.Error(), "stopped after") {
		t.Fatalf("last event = %#v, want a step-limit Error", events[len(events)-1])
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.requests) != maxSteps {
		t.Errorf("made %d requests, want exactly %d", len(srv.requests), maxSteps)
	}
	if len(a.history) != 0 {
		t.Error("a turn that hit the limit must not change history")
	}
}

func TestRepeatedToolCallsAreStopped(t *testing.T) {
	log := &recorder{}
	// The model asks for the same thing forever and always gets the same answer.
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "read", nameArgs{"a"}))))
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, log))

	events := drain(t, a.Send(context.Background(), "go"))

	last, ok := events[len(events)-1].(Error)
	if !ok || !strings.Contains(last.Err.Error(), "repeating the same tool calls") {
		t.Fatalf("last event = %#v, want a loop Error", events[len(events)-1])
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if want := loopMaxRepeats + 1; len(srv.requests) != want {
		t.Errorf("made %d requests, want %d (stopped at the first step over the limit)", len(srv.requests), want)
	}
	if len(a.history) != 0 {
		t.Error("a looping turn must not change history")
	}
}

func TestUsageIsReported(t *testing.T) {
	withUsage := `{"type":"response.completed","sequence_number":3,"response":{"id":"r","object":"response","status":"completed",
		"output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hi","annotations":[]}]}],
		"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}`
	srv := newFakeServer(t, sse(withUsage))
	a := New(srv.client(), "m", "", &tools.Registry{})

	events := drain(t, a.Send(context.Background(), "hi"))

	var got []Usage
	for _, e := range events {
		if u, ok := e.(Usage); ok {
			got = append(got, u)
		}
	}
	if len(got) != 1 || got[0] != (Usage{InputTokens: 120, OutputTokens: 30}) {
		t.Errorf("usage events = %+v, want one with 120 in and 30 out", got)
	}
}

func TestSilentStreamIsAbandoned(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(textDelta("partial")))
		w.(http.Flusher).Flush()
		select { // then go quiet, like a stalled connection
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	client := openai.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0))
	a := New(client, "m", "", &tools.Registry{})
	a.idleTimeout = 150 * time.Millisecond

	begin := time.Now()
	events := drain(t, a.Send(context.Background(), "hi"))

	last, ok := events[len(events)-1].(Error)
	if !ok || !strings.Contains(last.Err.Error(), "stopped sending data") {
		t.Fatalf("last event = %#v, want the idle-timeout Error", events[len(events)-1])
	}
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Errorf("took %v to give up on a stream with a 150ms idle timeout", elapsed)
	}
	if errors.Is(last.Err, context.Canceled) {
		t.Error("an idle timeout is not a user cancellation and must not look like one")
	}
	if len(a.history) != 0 {
		t.Error("history must not change when the stream stalls")
	}
}

func TestCancelWhileToolRuns(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan error, 1)
	blocking := tools.MustNew("block", "waits for cancel", false, func(ctx context.Context, _ nameArgs) (string, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return "", ctx.Err()
	})
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "block", nameArgs{"x"}))))
	a := newAgentWithTools(t, srv, blocking)

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Send(ctx, "go")
	go func() { <-started; cancel() }()
	got := drain(t, events)

	if _, ok := got[len(got)-1].(Error); !ok {
		t.Fatalf("last event = %#v, want Error", got[len(got)-1])
	}
	var finished *ToolFinished
	for _, e := range got {
		if f, ok := e.(ToolFinished); ok {
			finished = &f
		}
	}
	if finished == nil || !finished.Failed || !finished.Cancelled {
		t.Errorf("the interrupted call should be reported as failed and cancelled: %+v", finished)
	}
	if err := <-stopped; err == nil {
		t.Error("the tool never saw the cancellation")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.requests) != 1 {
		t.Errorf("the model was asked again after cancellation (%d requests)", len(srv.requests))
	}
	if len(a.history) != 0 {
		t.Error("history must not change on a cancelled turn")
	}
}
