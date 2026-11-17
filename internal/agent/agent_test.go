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
)

// fakeServer replays one scripted SSE body per request and records the request bodies.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	replies  []string
}

func newFakeServer(t *testing.T, replies ...string) *fakeServer {
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

		w.Header().Set("Content-Type", "text/event-stream")
		if n < len(f.replies) {
			_, _ = io.WriteString(w, f.replies[n])
		}
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

// completed builds a response.completed event holding a reasoning item and an assistant message.
func completed(text string) string {
	return fmt.Sprintf(`{"type":"response.completed","sequence_number":9,"response":{
		"id":"resp_1","object":"response","status":"completed",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENCRYPTED"},
			{"type":"message","id":"msg_1","role":"assistant","status":"completed",
			 "content":[{"type":"output_text","text":%q,"annotations":[]}]}
		]}}`, text)
}

// drain reads every event until the channel closes, as callers must.
func drain(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var got []Event
	timeout := time.After(5 * time.Second)
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

func TestSendStreamsText(t *testing.T) {
	srv := newFakeServer(t, sse(textDelta("Hello "), textDelta("there"), completed("Hello there")))
	a := New(srv.client(), "test-model", "be brief")

	events := drain(t, a.Send(context.Background(), "hi"))

	if got := streamedText(events); got != "Hello there" {
		t.Errorf("streamed text = %q, want %q", got, "Hello there")
	}
	if _, ok := events[len(events)-1].(Done); !ok {
		t.Errorf("last event = %#v, want Done", events[len(events)-1])
	}

	req := srv.request(t, 0)
	if req["model"] != "test-model" || req["instructions"] != "be brief" || req["store"] != false {
		t.Errorf("unexpected request fields: model=%v instructions=%v store=%v", req["model"], req["instructions"], req["store"])
	}
	if inc, _ := req["include"].([]any); len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want [reasoning.encrypted_content]", req["include"])
	}
}

func TestSecondTurnReplaysHistory(t *testing.T) {
	srv := newFakeServer(t,
		sse(textDelta("first answer"), completed("first answer")),
		sse(textDelta("second answer"), completed("second answer")),
	)
	a := New(srv.client(), "m", "")

	drain(t, a.Send(context.Background(), "question one"))
	drain(t, a.Send(context.Background(), "question two"))

	items := inputItems(t, srv.request(t, 1))
	if len(items) != 4 {
		t.Fatalf("second request has %d input items, want 4 (user, reasoning, assistant, user): %v", len(items), items)
	}
	if items[0]["role"] != "user" || items[0]["content"] != "question one" {
		t.Errorf("item 0 = %v, want the first user message", items[0])
	}
	// The encrypted reasoning content must come back untouched, or stateless replay fails.
	if items[1]["type"] != "reasoning" || items[1]["encrypted_content"] != "ENCRYPTED" {
		t.Errorf("item 1 = %v, want the reasoning item with its encrypted content", items[1])
	}
	if items[2]["type"] != "message" || items[2]["role"] != "assistant" {
		t.Errorf("item 2 = %v, want the assistant message", items[2])
	}
	if items[3]["role"] != "user" || items[3]["content"] != "question two" {
		t.Errorf("item 3 = %v, want the second user message", items[3])
	}
	if _, present := srv.request(t, 1)["instructions"]; present {
		t.Error("empty instructions should be omitted from the request")
	}
}

func TestFailedTurnLeavesHistoryUnchanged(t *testing.T) {
	failed := `{"type":"response.failed","sequence_number":2,"response":{"id":"r","object":"response","status":"failed","output":[],"error":{"code":"server_error","message":"boom"}}}`
	srv := newFakeServer(t,
		sse(failed),
		sse(textDelta("ok"), completed("ok")),
	)
	a := New(srv.client(), "m", "")

	events := drain(t, a.Send(context.Background(), "will fail"))
	last, ok := events[len(events)-1].(Error)
	if !ok || !strings.Contains(last.Err.Error(), "boom") {
		t.Fatalf("last event = %#v, want an Error mentioning boom", events[len(events)-1])
	}

	drain(t, a.Send(context.Background(), "retry"))
	items := inputItems(t, srv.request(t, 1))
	if len(items) != 1 || items[0]["content"] != "retry" {
		t.Errorf("retry request input = %v, want only the retry message", items)
	}
}

func TestStreamWithoutCompletionIsAnError(t *testing.T) {
	srv := newFakeServer(t, sse(textDelta("partial")))
	a := New(srv.client(), "m", "")

	events := drain(t, a.Send(context.Background(), "hi"))
	if _, ok := events[len(events)-1].(Error); !ok {
		t.Fatalf("last event = %#v, want Error", events[len(events)-1])
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
	a := New(client, "m", "")

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Send(ctx, "hi")
	time.AfterFunc(50*time.Millisecond, cancel)

	got := drain(t, events)
	if _, ok := got[len(got)-1].(Error); !ok {
		t.Fatalf("last event = %#v, want Error after cancel", got[len(got)-1])
	}
	if len(a.history) != 0 {
		t.Errorf("history has %d items after a cancelled turn, want 0", len(a.history))
	}
}
