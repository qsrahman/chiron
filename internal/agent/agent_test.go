package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/desk-agent/internal/tools"
)

// --- a fake Responses API ---

// reply is one scripted answer of the fake server.
type reply struct {
	status int    // HTTP status; 0 means 200
	body   string // an SSE stream, or JSON when json is set
	json   bool
}

// fakeServer replays one scripted reply per request (repeating the last one) and records the request bodies.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	replies  []reply
}

// newFakeServer scripts SSE streams.
func newFakeServer(t *testing.T, streams ...string) *fakeServer {
	t.Helper()
	replies := make([]reply, len(streams))
	for i, s := range streams {
		replies[i] = reply{body: s}
	}
	return newScriptedServer(t, replies...)
}

func newScriptedServer(t *testing.T, replies ...reply) *fakeServer {
	t.Helper()
	f := &fakeServer{replies: replies}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		f.mu.Lock()
		n := len(f.requests)
		f.requests = append(f.requests, req)
		f.mu.Unlock()

		if len(f.replies) == 0 {
			return
		}
		// Once the script runs out the last reply repeats, which lets tests simulate a model that never stops.
		rep := f.replies[min(n, len(f.replies)-1)]
		if rep.json {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
		}
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) client() openai.Client {
	return openai.NewClient(
		option.WithBaseURL(f.URL),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
}

func (f *fakeServer) request(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("server saw %d requests, want at least %d", len(f.requests), i+1)
	}
	return f.requests[i]
}

func (f *fakeServer) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// testSettings are the settings most tests use. They allow every call, since most tests are about something other
// than approval; the approval tests turn that off.
func testSettings(model string) Settings {
	return Settings{Model: model, Streaming: true, AutoApprove: true}
}

// newTestAgent returns an agent without tools, talking to the fake server.
func newTestAgent(srv *fakeServer) *Agent {
	return New(srv.client(), testSettings("m"), "", &tools.Registry{})
}

// --- scripted events ---

// sse formats events the way the Responses API streams them.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		// SSE data must be one line, so squash the pretty-printed fixtures.
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(e)); err != nil {
			panic("bad test fixture: " + err.Error())
		}
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(compact.Bytes(), &head)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", head.Type, compact.String())
	}
	return b.String()
}

func textDelta(s string) string {
	return fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":%q,"sequence_number":1}`, s)
}

const reasoningItem = `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENCRYPTED"}`

func messageItem(text string) string {
	return fmt.Sprintf(`{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}`, text)
}

// terminal builds a final response event of the given type and status around output items.
func terminal(eventType, status string, extra string, items ...string) string {
	return fmt.Sprintf(`{"type":%q,"sequence_number":9,"response":{"id":"resp_1","object":"response","status":%q,"output":[%s]%s}}`,
		eventType, status, strings.Join(items, ","), extra)
}

// completed is a normal ending: a reasoning item and an assistant message.
func completed(text string) string {
	return terminal("response.completed", "completed", "", reasoningItem, messageItem(text))
}

// completedWith is a normal ending holding the given output items.
func completedWith(items ...string) string {
	return terminal("response.completed", "completed", "", items...)
}

func failedEvent(code, message string) string {
	return terminal("response.failed", "failed", fmt.Sprintf(`,"error":{"code":%q,"message":%q}`, code, message))
}

func incompleteEvent(text string) string {
	return terminal("response.incomplete", "incomplete", `,"incomplete_details":{"reason":"max_output_tokens"}`, messageItem(text))
}

func errorEvent(code, message string) string {
	return fmt.Sprintf(`{"type":"error","code":%q,"message":%q,"sequence_number":2}`, code, message)
}

const refusalItem = `{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I can't help with that."}]}`

// jsonResponse is a non-streaming answer.
func jsonResponse(items ...string) reply {
	return reply{json: true, body: fmt.Sprintf(`{"id":"resp_1","object":"response","status":"completed","output":[%s]}`, strings.Join(items, ","))}
}

// apiError is an HTTP error answer such as OpenAI sends.
func apiError(status int, code, message string) reply {
	return reply{status: status, json: true, body: fmt.Sprintf(`{"error":{"message":%q,"type":"error","code":%q}}`, message, code)}
}

// --- reading events and requests ---

// drain reads every event until the channel closes, as callers must.
func drain(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var got []Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-timeout:
			t.Fatal("timed out waiting for the event channel to close")
		}
	}
}

func streamedText(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		if d, ok := e.(TextDelta); ok {
			b.WriteString(d.Text)
		}
	}
	return b.String()
}

func lastEvent(events []Event) Event { return events[len(events)-1] }

func lastError(t *testing.T, events []Event) error {
	t.Helper()
	e, ok := lastEvent(events).(Error)
	if !ok {
		t.Fatalf("last event = %#v, want Error", lastEvent(events))
	}
	return e.Err
}

func retries(events []Event) []Retry {
	var out []Retry
	for _, e := range events {
		if r, ok := e.(Retry); ok {
			out = append(out, r)
		}
	}
	return out
}

// inputItems returns the input items of a request.
func inputItems(t *testing.T, req map[string]any) []map[string]any {
	t.Helper()
	raw, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("input is %T, want a list", req["input"])
	}
	items := make([]map[string]any, len(raw))
	for i, r := range raw {
		items[i] = r.(map[string]any)
	}
	return items
}

// shape labels input items by what they are: the API type, or the role for plain messages.
func shape(items []map[string]any) string {
	labels := make([]string, len(items))
	for i, it := range items {
		if ty, ok := it["type"].(string); ok && ty != "message" {
			labels[i] = ty
		} else {
			labels[i], _ = it["role"].(string)
		}
	}
	return strings.Join(labels, ",")
}

// kinds labels the agent's own history items.
func kinds(items []item) string {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.kind
	}
	return strings.Join(labels, ",")
}

// --- tests ---

func TestSendStreamsText(t *testing.T) {
	srv := newFakeServer(t, sse(textDelta("Hello "), textDelta("there"), completed("Hello there")))
	a := New(srv.client(), testSettings("test-model"), "be brief", &tools.Registry{})

	events := drain(t, a.Send(context.Background(), "hi"))

	if got := streamedText(events); got != "Hello there" {
		t.Errorf("streamed text = %q, want %q", got, "Hello there")
	}
	if _, ok := lastEvent(events).(Done); !ok {
		t.Errorf("last event = %#v, want Done", lastEvent(events))
	}

	req := srv.request(t, 0)
	if req["model"] != "test-model" || req["instructions"] != "be brief" || req["store"] != false {
		t.Errorf("unexpected request fields: model=%v instructions=%v store=%v", req["model"], req["instructions"], req["store"])
	}
	if inc, _ := req["include"].([]any); len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want [reasoning.encrypted_content]", req["include"])
	}
	if _, present := req["reasoning"]; present {
		t.Error("no reasoning setting must be sent unless the user set one")
	}
}

func TestSecondTurnReplaysTheMessages(t *testing.T) {
	srv := newFakeServer(t,
		sse(textDelta("first answer"), completed("first answer")),
		sse(textDelta("second answer"), completed("second answer")),
	)
	a := newTestAgent(srv)

	drain(t, a.Send(context.Background(), "question one"))
	drain(t, a.Send(context.Background(), "question two"))

	items := inputItems(t, srv.request(t, 1))
	// The final reply is kept as plain text: its reasoning is not replayed.
	if got := shape(items); got != "user,assistant,user" {
		t.Fatalf("second request items = %s, want user,assistant,user: %v", got, items)
	}
	if items[0]["content"] != "question one" || items[1]["content"] != "first answer" || items[2]["content"] != "question two" {
		t.Errorf("unexpected contents: %v", items)
	}
	if _, present := srv.request(t, 1)["instructions"]; present {
		t.Error("empty instructions should be omitted from the request")
	}
}

func TestReasoningEffortIsSentOnlyWhenSet(t *testing.T) {
	srv := newFakeServer(t, sse(completed("ok")))
	settings := testSettings("m")
	settings.ReasoningEffort = "low"

	drain(t, New(srv.client(), settings, "", &tools.Registry{}).Send(context.Background(), "hi"))

	reasoning, _ := srv.request(t, 0)["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" {
		t.Errorf("reasoning = %v, want effort low", srv.request(t, 0)["reasoning"])
	}
}

func TestNonStreamingMode(t *testing.T) {
	srv := newScriptedServer(t, jsonResponse(messageItem("whole reply")))
	settings := testSettings("m")
	settings.Streaming = false

	events := drain(t, New(srv.client(), settings, "", &tools.Registry{}).Send(context.Background(), "hi"))

	if got := streamedText(events); got != "whole reply" {
		t.Errorf("text = %q", got)
	}
	if _, ok := lastEvent(events).(Done); !ok {
		t.Errorf("last event = %#v", lastEvent(events))
	}
	if srv.request(t, 0)["stream"] == true {
		t.Error("a non-streaming request must not ask for a stream")
	}
}

func TestFailedFirstRoundSavesNothing(t *testing.T) {
	srv := newFakeServer(t,
		sse(failedEvent("server_error", "boom")),
		sse(textDelta("ok"), completed("ok")),
	)
	a := newTestAgent(srv)

	events := drain(t, a.Send(context.Background(), "will fail"))
	if err := lastError(t, events); !strings.Contains(err.Error(), "Response failed: server_error: boom") {
		t.Errorf("error = %v", err)
	}

	drain(t, a.Send(context.Background(), "retry"))
	items := inputItems(t, srv.request(t, 1))
	if len(items) != 1 || items[0]["content"] != "retry" {
		t.Errorf("retry request input = %v, want only the retry message", items)
	}
}

func TestBrokenStreamIsRetriedAndTheTextStartsOver(t *testing.T) {
	srv := newFakeServer(t,
		sse(textDelta("partial")), // ends with no final event
		sse(textDelta("full reply"), completed("full reply")),
	)
	a := newTestAgent(srv)

	events := drain(t, a.Send(context.Background(), "hi"))

	got := retries(events)
	if len(got) != 1 || !strings.Contains(got[0].Reason, "Stream ended without a final response; trying again (1 of 2)") {
		t.Errorf("retry events = %+v", got)
	}
	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v, want Done", lastEvent(events))
	}
	// The same request was sent again, and the failed attempt left nothing in the history.
	if srv.requestCount() != 2 || shape(inputItems(t, srv.request(t, 1))) != "user" {
		t.Errorf("requests = %d, second input = %v", srv.requestCount(), srv.request(t, 1)["input"])
	}
	if len(a.history.items) != 2 {
		t.Errorf("history = %s, want the user message and the reply", kinds(a.history.items))
	}
}

func TestStreamThatKeepsBreakingGivesUp(t *testing.T) {
	srv := newFakeServer(t, sse(textDelta("partial")))
	a := newTestAgent(srv)

	events := drain(t, a.Send(context.Background(), "hi"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "Stream ended without a final response") {
		t.Errorf("error = %v", err)
	}
	if want := 1 + streamRetries; srv.requestCount() != want {
		t.Errorf("made %d requests, want %d", srv.requestCount(), want)
	}
	if len(retries(events)) != streamRetries {
		t.Errorf("retry events = %d, want %d", len(retries(events)), streamRetries)
	}
	if len(a.history.items) != 0 {
		t.Error("a turn that never got a response must not change the history")
	}
}

func TestErrorEventIsNotRetried(t *testing.T) {
	srv := newFakeServer(t, sse(errorEvent("rate_limit", "slow down")))
	a := newTestAgent(srv)

	events := drain(t, a.Send(context.Background(), "hi"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "Stream error: rate_limit: slow down") {
		t.Errorf("error = %v", err)
	}
	if srv.requestCount() != 1 {
		t.Errorf("an error event means the same request would fail again, but %d requests were made", srv.requestCount())
	}
}

func TestEmptyReplyIsRetriedOnce(t *testing.T) {
	t.Run("then it works", func(t *testing.T) {
		srv := newFakeServer(t, sse(completedWith()), sse(textDelta("ok"), completed("ok")))
		events := drain(t, newTestAgent(srv).Send(context.Background(), "hi"))

		if got := retries(events); len(got) != 1 || !strings.Contains(got[0].Reason, "empty reply") {
			t.Errorf("retry events = %+v", got)
		}
		if _, ok := lastEvent(events).(Done); !ok || srv.requestCount() != 2 {
			t.Errorf("last = %#v, requests = %d", lastEvent(events), srv.requestCount())
		}
	})
	t.Run("then it is reported", func(t *testing.T) {
		srv := newFakeServer(t, sse(completedWith()))
		events := drain(t, newTestAgent(srv).Send(context.Background(), "hi"))

		if err := lastError(t, events); err.Error() != noText {
			t.Errorf("error = %v, want %q", err, noText)
		}
		if srv.requestCount() != 2 {
			t.Errorf("an empty reply is asked for again once, not %d times", srv.requestCount()-1)
		}
	})
}

func TestRefusalIsReported(t *testing.T) {
	srv := newFakeServer(t, sse(completedWith(refusalItem)))

	events := drain(t, newTestAgent(srv).Send(context.Background(), "hi"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "Refused: I can't help with that.") {
		t.Errorf("error = %v", err)
	}
	if srv.requestCount() != 1 {
		t.Error("a refusal is not an empty reply and must not be retried")
	}
}

func TestIncompleteReplyKeepsItsText(t *testing.T) {
	srv := newFakeServer(t,
		sse(textDelta("half of an ans"), incompleteEvent("half of an ans")),
		sse(textDelta("ok"), completed("ok")),
	)
	a := newTestAgent(srv)

	events := drain(t, a.Send(context.Background(), "question"))
	if err := lastError(t, events); !strings.Contains(err.Error(), "Response incomplete: max_output_tokens") {
		t.Errorf("error = %v", err)
	}

	// The user has seen the partial reply, so it is part of the conversation.
	drain(t, a.Send(context.Background(), "go on"))
	items := inputItems(t, srv.request(t, 1))
	if shape(items) != "user,assistant,user" || items[1]["content"] != "half of an ans" {
		t.Errorf("second request = %v, want the partial reply kept", items)
	}
}

func TestStreamsWithoutOutputInTheFinalEventUseTheFinishedItems(t *testing.T) {
	call := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"name\":\"a\"}","status":"completed"}`
	itemDone := fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"sequence_number":3,"item":%s}`, call)
	// Some compatible servers send response.completed with an empty output.
	srv := newFakeServer(t,
		sse(itemDone, completedWith()),
		sse(textDelta("done"), completed("done")),
	)
	log := &recorder{}
	a := newAgentWithTools(t, srv, fakeTool("read", true, 0, log))

	events := drain(t, a.Send(context.Background(), "go"))

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v", lastEvent(events))
	}
	if log.index(t, "start a") < 0 {
		t.Error("the call from the finished item was not run")
	}
}

func TestAPIErrorsAreDescribedForTheUser(t *testing.T) {
	tests := []struct {
		name string
		rep  reply
		want []string
	}{
		{"wrong key", apiError(401, "invalid_api_key", "Incorrect API key"), []string{"API error 401 (invalid_api_key): Incorrect API key", "Check the API key"}},
		{"unknown model", apiError(404, "model_not_found", "no such model"), []string{"API error 404", "Check the model and base_url"}},
		{"rate limit", apiError(429, "rate_limit_exceeded", "slow down"), []string{"API error 429", "out of quota, or rate limited"}},
		{"context too long", apiError(400, "context_length_exceeded", "too many tokens"), []string{"API error 400", "too long for the model"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newScriptedServer(t, tt.rep)
			events := drain(t, newTestAgent(srv).Send(context.Background(), "hi"))

			err := lastError(t, events)
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if srv.requestCount() != 1 {
				t.Errorf("an API error is not retried by the agent (%d requests)", srv.requestCount())
			}
		})
	}
}

func TestCancelStopsTurn(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select { // hang like a slow model until the client gives up
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	client := openai.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0))
	a := New(client, testSettings("m"), "", &tools.Registry{})

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Send(ctx, "hi")
	time.AfterFunc(50*time.Millisecond, cancel)

	if err := lastError(t, drain(t, events)); !isCancel(err) {
		t.Errorf("error = %v, want a cancellation", err)
	}
	if len(a.history.items) != 0 {
		t.Errorf("history has %d items after a turn cancelled before any tool ran, want 0", len(a.history.items))
	}
}
