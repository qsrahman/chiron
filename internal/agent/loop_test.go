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

	"github.com/qsrahman/chiron/internal/tools"
)

// --- fixtures ---

func isCancel(err error) bool { return errors.Is(err, context.Canceled) }

// functionCall is a function_call output item as the API sends it.
func functionCall(callID, name string, args any) string {
	arguments, _ := json.Marshal(args)
	quoted, _ := json.Marshal(string(arguments))
	return fmt.Sprintf(`{"type":"function_call","id":"fc_%s","call_id":%q,"name":%q,"arguments":%s,"status":"completed"}`,
		callID, callID, name, quoted)
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
	return New(srv.client(), testSettings("m"), "", reg)
}

// outputsOf returns the function_call_output items of a request, in order.
func outputsOf(t *testing.T, req map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, it := range inputItems(t, req) {
		if it["type"] == "function_call_output" {
			out = append(out, it)
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

// lastNote returns the text of the closing assistant note of the saved history.
func lastNote(a *Agent) string {
	items := a.history.items
	if len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	if last.kind != kindAssistant {
		return ""
	}
	raw, _ := json.Marshal(last.param)
	var p struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Content
}

// --- tests ---

func TestToolCallRoundTrip(t *testing.T) {
	log := &recorder{}
	srv := newFakeServer(t, callsThenAnswer(functionCall("call_1", "read", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, log))

	events := withoutUsage(drain(t, a.Send(context.Background(), "go")))

	var kindsOfEvents []string
	for _, e := range events {
		kindsOfEvents = append(kindsOfEvents, fmt.Sprintf("%T", e))
	}
	want := []string{"agent.ToolStarted", "agent.ToolFinished", "agent.TextDelta", "agent.Done"}
	if strings.Join(kindsOfEvents, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", kindsOfEvents, want)
	}
	started, finished := events[0].(ToolStarted), events[1].(ToolFinished)
	if started.CallID != "call_1" || started.Name != "read" || started.Arguments != `{"name":"a"}` {
		t.Errorf("ToolStarted = %+v", started)
	}
	if finished.Output != "result a" || finished.Outcome != CallOK {
		t.Errorf("ToolFinished = %+v", finished)
	}

	toolList, _ := srv.request(t, 0)["tools"].([]any)
	if len(toolList) != 1 || toolList[0].(map[string]any)["name"] != "read" {
		t.Errorf("tools sent = %v", srv.request(t, 0)["tools"])
	}
	outs := outputsOf(t, srv.request(t, 1))
	if len(outs) != 1 || outs[0]["call_id"] != "call_1" || outs[0]["output"] != "result a" {
		t.Errorf("function_call_output items = %v", outs)
	}
	if got := shape(inputItems(t, srv.request(t, 1))); got != "user,function_call,function_call_output" {
		t.Errorf("second request = %s, want the call replayed next to its output", got)
	}
}

func TestReasoningBeforeACallIsReplayed(t *testing.T) {
	srv := newFakeServer(t,
		sse(completedWith(reasoningItem, functionCall("c1", "read", nameArgs{"a"}))),
		sse(textDelta("done"), completed("done")),
	)
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, &recorder{}))

	drain(t, a.Send(context.Background(), "go"))

	items := inputItems(t, srv.request(t, 1))
	if got := shape(items); got != "user,reasoning,function_call,function_call_output" {
		t.Fatalf("second request = %s", got)
	}
	if items[1]["encrypted_content"] != "ENCRYPTED" {
		t.Errorf("the reasoning item must come back untouched: %v", items[1])
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

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v", lastEvent(events))
	}
	if elapsed >= 2*delay {
		t.Errorf("three %v calls took %v: they did not run concurrently", delay, elapsed)
	}
	outs := outputsOf(t, srv.request(t, 1))
	for i, id := range []string{"c1", "c2", "c3"} {
		if outs[i]["call_id"] != id || outs[i]["output"] != "result "+string('a'+rune(i)) {
			t.Errorf("output %d = %v, want call %s", i, outs[i], id)
		}
	}
}

func TestParallelCallsAreCapped(t *testing.T) {
	var running, peak atomic.Int32
	slow := tools.MustNew("slow", "tracks how many run at once", true, func(ctx context.Context, _ nameArgs) (string, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return "ok", nil
	})
	var calls []string
	for i := range 3 * maxParallelCalls {
		calls = append(calls, functionCall(fmt.Sprint("c", i), "slow", nameArgs{"x"}))
	}
	srv := newFakeServer(t, callsThenAnswer(calls...)...)

	events := drain(t, newAgentWithTools(t, srv, slow).Send(context.Background(), "go"))

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v", lastEvent(events))
	}
	if got := int(peak.Load()); got > maxParallelCalls || got < 2 {
		t.Errorf("peak concurrency = %d, want between 2 and %d", got, maxParallelCalls)
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

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("the turn should still finish, got %#v", lastEvent(events))
	}
	outs := outputsOf(t, srv.request(t, 1))
	wants := []string{"error: boom", `error: unknown tool "nope". Available tools: fail, quiet.`, "error: invalid arguments", "(no output)"}
	for i, want := range wants {
		if got, _ := outs[i]["output"].(string); !strings.HasPrefix(got, want) {
			t.Errorf("output %d = %q, want prefix %q", i, got, want)
		}
	}
	failed := map[string]bool{}
	for _, e := range events {
		if f, ok := e.(ToolFinished); ok {
			failed[f.CallID] = f.Outcome == CallFailed
		}
	}
	for id, want := range map[string]bool{"c1": true, "c2": true, "c3": true, "c4": false} {
		if failed[id] != want {
			t.Errorf("Failed[%s] = %v, want %v", id, failed[id], want)
		}
	}
}

func TestUnknownToolWithNoToolsSaysSo(t *testing.T) {
	if got := unknownToolText("x", nil); got != `error: unknown tool "x". Available tools: none.` {
		t.Errorf("got %q", got)
	}
}

func TestLongToolOutputIsCut(t *testing.T) {
	big := tools.MustNew("big", "returns a lot", true, func(context.Context, nameArgs) (string, error) {
		return strings.Repeat("é", maxOutputBytes), nil // two bytes each: the cut must not split one
	})
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "big", nameArgs{"x"}))...)

	drain(t, newAgentWithTools(t, srv, big).Send(context.Background(), "go"))

	out, _ := outputsOf(t, srv.request(t, 1))[0]["output"].(string)
	if !strings.Contains(out, "[output truncated: ") || !strings.HasSuffix(out, " more characters]") {
		t.Errorf("output does not say it was cut: ...%q", out[max(0, len(out)-60):])
	}
	if len(out) > maxOutputBytes+100 {
		t.Errorf("output is %d bytes, want about %d", len(out), maxOutputBytes)
	}
	if strings.Contains(out, "�") {
		t.Error("the cut split a multi-byte character")
	}
}

func TestOlderTurnsKeepOnlyTheirMessages(t *testing.T) {
	srv := newFakeServer(t,
		sse(completedWith(functionCall("c1", "read", nameArgs{"a"}))), sse(textDelta("one"), completed("one")),
		sse(completedWith(functionCall("c2", "read", nameArgs{"b"}))), sse(textDelta("two"), completed("two")),
		sse(textDelta("three"), completed("three")),
	)
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, &recorder{}))

	drain(t, a.Send(context.Background(), "q1"))
	drain(t, a.Send(context.Background(), "q2"))
	drain(t, a.Send(context.Background(), "q3"))

	// Request 3 is turn 2's first one (index 2); the first of turn 3 is index 4.
	// Turn 1's tool work is gone, turn 2 (the newest) still has its call and output.
	if got := shape(inputItems(t, srv.request(t, 4))); got != "user,assistant,user,function_call,function_call_output,assistant,user" {
		t.Errorf("third turn's request = %s", got)
	}
	// While turn 2 ran, turn 1 was the newest and kept everything.
	if got := shape(inputItems(t, srv.request(t, 2))); got != "user,function_call,function_call_output,assistant,user" {
		t.Errorf("second turn's first request = %s", got)
	}
}

func TestStepLimitKeepsTheTurnWithANote(t *testing.T) {
	// Every call returns something new, so this is not a repeat and only the step limit can stop it.
	var n atomic.Int32
	counter := tools.MustNew("count", "returns a different number each time", true,
		func(context.Context, nameArgs) (string, error) { return fmt.Sprint(n.Add(1)), nil })
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "count", nameArgs{"a"}))))
	a := newAgentWithTools(t, srv, counter)

	events := drain(t, a.Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "Stopped after 25 model calls without a final answer") {
		t.Fatalf("error = %v", err)
	}
	if srv.requestCount() != defaultMaxTurns {
		t.Errorf("made %d requests, want exactly %d", srv.requestCount(), defaultMaxTurns)
	}
	// The tools ran, so the model must know: the turn is kept, closed with a note.
	if note := lastNote(a); note != "[Stopped after 25 model calls without a final answer]" {
		t.Errorf("closing note = %q", note)
	}
}

func TestMaxTurnsIsASetting(t *testing.T) {
	var n atomic.Int32
	counter := tools.MustNew("count", "different each time", true,
		func(context.Context, nameArgs) (string, error) { return fmt.Sprint(n.Add(1)), nil })
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "count", nameArgs{"a"}))))
	reg, _ := tools.NewRegistry(counter)
	settings := testSettings("m")
	settings.MaxTurns = 3

	events := drain(t, New(srv.client(), settings, "", reg).Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "Stopped after 3 model calls") || srv.requestCount() != 3 {
		t.Errorf("error = %v, requests = %d", err, srv.requestCount())
	}
}

func TestRepeatedToolCallsAreStoppedAndKept(t *testing.T) {
	srv := newFakeServer(t, sse(completedWith(functionCall("c1", "read", nameArgs{"a"}))))
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, &recorder{}))

	events := drain(t, a.Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "repeating the same tool calls") {
		t.Fatalf("error = %v", err)
	}
	if want := loopMaxRepeats + 1; srv.requestCount() != want {
		t.Errorf("made %d requests, want %d (stopped at the first step over the limit)", srv.requestCount(), want)
	}
	if !strings.Contains(lastNote(a), "Stopped after 6 model calls: stopped: the model keeps repeating") {
		t.Errorf("closing note = %q", lastNote(a))
	}
}

func TestFailureAfterToolsKeepsTheTurn(t *testing.T) {
	srv := newScriptedServer(t,
		reply{body: sse(completedWith(functionCall("c1", "write", nameArgs{"x"})))},
		apiError(500, "server_error", "the server fell over"),
	)
	a := newAgentWithTools(t, srv, fakeTool("write", false, 0, &recorder{}))

	events := drain(t, a.Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "API error 500") {
		t.Fatalf("error = %v", err)
	}
	// The write ran. The history keeps it, and the note says why the turn stopped (nothing the server wrote).
	if got := kinds(a.history.items); got != "user,function_call,function_call_output,assistant" {
		t.Errorf("history = %s", got)
	}
	if lastNote(a) != "[Stopped after 1 model calls: API error 500]" {
		t.Errorf("closing note = %q", lastNote(a))
	}
	if strings.Contains(lastNote(a), "fell over") {
		t.Error("text from the server must not go into the note the model reads")
	}
}

func TestCancelWhileToolRunsKeepsTheTurnAndAnswersTheCall(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan error, 1)
	blocking := tools.MustNew("block", "waits for cancel", false, func(ctx context.Context, _ nameArgs) (string, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return "", ctx.Err()
	})
	var ran atomic.Int32
	later := tools.MustNew("later", "must not run after a cancel", false, func(context.Context, nameArgs) (string, error) {
		ran.Add(1)
		return "ran", nil
	})
	srv := newFakeServer(t, sse(completedWith(
		functionCall("c1", "block", nameArgs{"x"}),
		functionCall("c2", "later", nameArgs{"x"}),
	)))
	a := newAgentWithTools(t, srv, blocking, later)

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Send(ctx, "go")
	go func() { <-started; cancel() }()
	got := drain(t, events)

	if err := lastError(t, got); !isCancel(err) {
		t.Fatalf("error = %v, want a cancellation", err)
	}
	if err := <-stopped; err == nil {
		t.Error("the tool never saw the cancellation")
	}
	if ran.Load() != 0 {
		t.Error("a call that had not run yet must not start after the cancel")
	}
	for _, e := range got {
		if f, ok := e.(ToolFinished); ok && (f.Outcome != CallCancelled || f.Output != interruptedOutput) {
			t.Errorf("an interrupted call should be cancelled and say so: %+v", f)
		}
	}
	if srv.requestCount() != 1 {
		t.Errorf("the model was asked again after cancellation (%d requests)", srv.requestCount())
	}
	// Both calls are in the history with an output, and the note says the user stopped it.
	if got := kinds(a.history.items); got != "user,function_call,function_call,function_call_output,function_call_output,assistant" {
		t.Errorf("history = %s", got)
	}
	if lastNote(a) != "[Stopped after 1 model calls: interrupted by the user]" {
		t.Errorf("closing note = %q", lastNote(a))
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
	a := New(client, testSettings("m"), "", &tools.Registry{})
	a.idleTimeout = 150 * time.Millisecond

	begin := time.Now()
	events := drain(t, a.Send(context.Background(), "hi"))

	err := lastError(t, events)
	if !strings.Contains(err.Error(), "stopped sending data") {
		t.Fatalf("error = %v, want the idle-timeout error", err)
	}
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Errorf("took %v to give up on a stream with a 150ms idle timeout", elapsed)
	}
	if isCancel(err) {
		t.Error("an idle timeout is not a user cancellation and must not look like one")
	}
	if len(retries(events)) != 0 {
		t.Error("a stalled stream is not retried: it would only stall again")
	}
	if len(a.history.items) != 0 {
		t.Error("history must not change when the stream stalls in the first round")
	}
}

func TestUsageIsReported(t *testing.T) {
	withUsage := `{"type":"response.completed","sequence_number":3,"response":{"id":"r","object":"response","status":"completed",
		"output":[` + messageItem("hi") + `],
		"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}`
	srv := newFakeServer(t, sse(withUsage))

	events := drain(t, newTestAgent(srv).Send(context.Background(), "hi"))

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
