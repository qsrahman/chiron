package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/desk-agent/internal/tools"
)

func mustOutputItem(t *testing.T, raw string) responses.ResponseOutputItemUnion {
	t.Helper()
	var o responses.ResponseOutputItemUnion
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestUndoDropsTheLastExchange(t *testing.T) {
	srv := newFakeServer(t,
		sse(completed("one")),
		sse(completed("two")),
		sse(completed("three")),
	)
	a := newTestAgent(srv)
	drain(t, a.Send(context.Background(), "q1"))
	drain(t, a.Send(context.Background(), "q2"))

	if !a.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	drain(t, a.Send(context.Background(), "q3"))

	if got := shape(inputItems(t, srv.request(t, 2))); got != "user,assistant,user" {
		t.Errorf("after undo the request carries %s, want user,assistant,user", got)
	}
	a.Undo() // q3
	a.Undo() // q1
	if a.Undo() {
		t.Error("Undo() must report false once nothing is left")
	}
}

func TestRewindResendsThePromptWithoutTheOldAnswer(t *testing.T) {
	srv := newFakeServer(t, sse(completed("one")), sse(completed("two")))
	a := newTestAgent(srv)
	drain(t, a.Send(context.Background(), "q1"))

	prompt, ok := a.Rewind()
	if !ok || prompt != "q1" {
		t.Fatalf("Rewind() = %q, %v", prompt, ok)
	}
	drain(t, a.Send(context.Background(), prompt))

	if got := shape(inputItems(t, srv.request(t, 1))); got != "user" {
		t.Errorf("retry carries %s, want only the prompt", got)
	}
}

// After /undo the last prompt's turn is gone already; a retry must not remove the exchange before it.
func TestRewindAfterUndoKeepsOlderExchanges(t *testing.T) {
	srv := newFakeServer(t, sse(completed("one")), sse(completed("two")), sse(completed("again")))
	a := newTestAgent(srv)
	drain(t, a.Send(context.Background(), "q1"))
	drain(t, a.Send(context.Background(), "q2"))
	a.Undo()

	prompt, ok := a.Rewind()
	if !ok || prompt != "q2" {
		t.Fatalf("Rewind() = %q, %v", prompt, ok)
	}
	drain(t, a.Send(context.Background(), prompt))

	if got := shape(inputItems(t, srv.request(t, 2))); got != "user,assistant,user" {
		t.Errorf("retry carries %s, want the first exchange kept", got)
	}
}

func TestRewindWithNothingSent(t *testing.T) {
	if _, ok := newTestAgent(newFakeServer(t)).Rewind(); ok {
		t.Error("Rewind() = true before any prompt")
	}
}

func TestResetForgetsEverything(t *testing.T) {
	srv := newFakeServer(t, sse(completed("one")), sse(completed("two")))
	a := newTestAgent(srv)
	drain(t, a.Send(context.Background(), "q1"))
	if !a.HasConversation() {
		t.Fatal("HasConversation() = false after a turn")
	}

	a.Reset()
	drain(t, a.Send(context.Background(), "q2"))

	if a.HasConversation() == false || shape(inputItems(t, srv.request(t, 1))) != "user" {
		t.Errorf("reset did not clear the history: %s", shape(inputItems(t, srv.request(t, 1))))
	}
}

func TestSettersChangeTheNextRequest(t *testing.T) {
	srv := newFakeServer(t, sse(completed("one")))
	a := newTestAgent(srv)

	a.SetModel("other-model")
	a.SetReasoningEffort("high")
	drain(t, a.Send(context.Background(), "q"))

	req := srv.request(t, 0)
	if req["model"] != "other-model" {
		t.Errorf("model = %v", req["model"])
	}
	if r, _ := req["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Errorf("reasoning = %v", req["reasoning"])
	}
	if a.Settings().Model != "other-model" {
		t.Errorf("Settings().Model = %q", a.Settings().Model)
	}
}

func TestEstimateIgnoresEncryptedReasoning(t *testing.T) {
	text := item{kind: "message", param: responses.ResponseInputItemParamOfMessage(strings.Repeat("a", 400), responses.EasyInputMessageRoleUser)}
	blob := `{"type":"reasoning","id":"r","summary":[],"encrypted_content":"` + strings.Repeat("x", 40000) + `"}`
	reasoning := outputItems([]responses.ResponseOutputItemUnion{mustOutputItem(t, blob)})[0]

	if got := text.tokens(); got < 100 || got > 110 {
		t.Errorf("400 bytes of text estimated at %d tokens, want about 100", got)
	}
	if got := reasoning.tokens(); got > 50 {
		t.Errorf("reasoning estimated at %d tokens: its encrypted content must not count", got)
	}
}

func TestContextEstimateAddsTheParts(t *testing.T) {
	srv := newFakeServer(t, sse(completed("an answer")))
	a := New(srv.client(), testSettings("m"), strings.Repeat("i", 400), &tools.Registry{})
	before := a.ContextEstimate()
	drain(t, a.Send(context.Background(), "a question"))
	after := a.ContextEstimate()

	if before.Instructions < 100 || before.History != 0 || before.ToolCount != 0 || before.Tools != 0 {
		t.Errorf("before = %+v", before)
	}
	if after.History <= 0 || after.Total() != after.Instructions+after.Tools+after.History {
		t.Errorf("after = %+v", after)
	}
}

func TestCapOutputCutsOnACharacterBoundary(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"multi-byte text", strings.Repeat("€", maxOutputBytes)},
		{"invalid bytes early on", "ok\xff" + strings.Repeat("a", maxOutputBytes+10)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capOutput(tt.text)
			head, _, _ := strings.Cut(got, "\n[output truncated")
			if len(head) < maxOutputBytes-3 || len(head) > maxOutputBytes {
				t.Errorf("kept %d bytes, want about %d", len(head), maxOutputBytes)
			}
			if tt.name == "multi-byte text" && !utf8.ValidString(head) {
				t.Error("the cut split a character")
			}
		})
	}
}
