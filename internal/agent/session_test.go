package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qsrahman/desk-agent/internal/session"
)

const endpoint = "http://localhost:11434"

// saved is an agent that saves into a temporary store.
func saved(t *testing.T, srv *fakeServer, store *session.Store) *Agent {
	t.Helper()
	a := newTestAgent(srv)
	a.EnableSessions(store, "/work", endpoint)
	return a
}

func chatReply(text string) reply { return reply{body: sse(textDelta(text), completed(text))} }

func TestATurnIsSavedAndResumedByAnotherAgent(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	a := saved(t, srv, store)
	drain(t, a.Send(context.Background(), "first question"))
	id := a.SessionID()
	if id == "" {
		t.Fatal("no session was named")
	}

	f, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	b := saved(t, srv, store)
	if err := b.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	drain(t, b.Send(context.Background(), "second question"))

	// The resumed agent sent the earlier messages (and the reasoning item, same model and server).
	if got := shape(inputItems(t, srv.request(t, 1))); got != "user,reasoning,assistant,user" && got != "user,assistant,user" {
		t.Errorf("request = %s", got)
	}
	if b.SessionID() != id {
		t.Errorf("the resumed conversation went on in %q, not %q", b.SessionID(), id)
	}
	if again, _ := store.Load(id); len(again.Items) <= len(f.Items) {
		t.Error("the second turn was not added to the same file")
	}
}

func TestAResumedToolTurnIsValidAndShownAgain(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "big", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, bigTool(10))
	a.EnableSessions(store, "/work", endpoint)
	drain(t, a.Send(context.Background(), "go"))

	f, _ := store.Load(a.SessionID())
	b := newTestAgent(srv)
	if err := b.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := kinds(b.history.items); got != kinds(a.history.items) {
		t.Errorf("resumed %s, saved %s", got, kinds(a.history.items))
	}
	var tool *Message
	msgs := b.Messages()
	for i := range msgs {
		if msgs[i].Role == "tool" {
			tool = &msgs[i]
		}
	}
	if tool == nil || tool.Name != "big" || tool.Output != strings.Repeat("x", 10) || !strings.Contains(tool.Arguments, `"a"`) {
		t.Errorf("messages = %+v", msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "go" || msgs[len(msgs)-1].Text != "done" {
		t.Errorf("messages = %+v", msgs)
	}
	if prompt, ok := b.Rewind(); !ok || prompt != "go" {
		t.Errorf("/retry after a resume = %q, %v", prompt, ok)
	}
}

func fileWithReasoning(model, ep string) session.File {
	return session.File{ID: "r-1", Model: model, Endpoint: ep, Items: []session.Item{
		{Kind: kindUser, Raw: []byte(`{"content":"q","role":"user"}`)},
		{Kind: kindReasoning, Raw: []byte(reasoningItem)},
		{Kind: "message", Raw: []byte(messageItem("a"))},
	}}
}

func TestReasoningIsDroppedOnlyWhenTheModelOrServerDiffers(t *testing.T) {
	tests := []struct {
		name, model, endpoint, want string
	}{
		{"same", "m", endpoint, "user,reasoning,message"},
		{"other model", "other", endpoint, "user,message"},
		{"other server", "m", "https://api.openai.com", "user,message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAgent(newFakeServer(t))
			if err := a.Resume(fileWithReasoning(tt.model, tt.endpoint), endpoint); err != nil {
				t.Fatal(err)
			}
			if got := kinds(a.history.items); got != tt.want {
				t.Errorf("items = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAFileWithAResultButNoCallIsRefused(t *testing.T) {
	f := session.File{ID: "x-1", Items: []session.Item{
		{Kind: kindUser, Raw: []byte(`{"content":"q","role":"user"}`)},
		{Kind: kindCallOutput, CallID: "c9", Raw: []byte(`{"output":"x"}`)},
	}}
	if err := newTestAgent(newFakeServer(t)).Resume(f, endpoint); err == nil || !strings.Contains(err.Error(), "no call") {
		t.Errorf("error = %v", err)
	}
}

func TestACallWithoutAResultIsAnsweredOnResume(t *testing.T) {
	f := session.File{ID: "x-1", Items: []session.Item{
		{Kind: kindUser, Raw: []byte(`{"content":"q","role":"user"}`)},
		{Kind: kindCall, CallID: "c1", Raw: []byte(functionCall("c1", "big", nameArgs{"a"}))},
	}}
	a := newTestAgent(newFakeServer(t))
	if err := a.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := kinds(a.history.items); got != "user,function_call,function_call_output" {
		t.Errorf("items = %s", got)
	}
}

func TestAnItemOfAnUnknownKindIsRefused(t *testing.T) {
	f := session.File{ID: "x-1", Items: []session.Item{{Kind: "weird", Raw: []byte(`{}`)}}}
	if err := newTestAgent(newFakeServer(t)).Resume(f, endpoint); err == nil {
		t.Error("an unknown kind must be refused")
	}
}

func TestNothingIsWrittenWithoutSessionsOrWithoutAConversation(t *testing.T) {
	state := t.TempDir()
	store := session.NewStore(state)
	srv := newScriptedServer(t, chatReply("one"), reply{status: 500, json: true, body: `{"error":{"message":"boom"}}`})

	off := newTestAgent(srv) // sessions never enabled
	drain(t, off.Send(context.Background(), "hello"))

	on := saved(t, srv, store)
	drain(t, on.Send(context.Background(), "this fails before anything is saved")) // the first round fails
	on.Reset()

	if _, err := os.Stat(filepath.Join(state, "sessions")); !os.IsNotExist(err) {
		t.Errorf("something was written: %v", err)
	}
}

func TestResetStartsANewFileAndKeepsTheOld(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	a := saved(t, srv, store)
	drain(t, a.Send(context.Background(), "first"))
	first := a.SessionID()

	a.Reset()
	if a.SessionID() != "" {
		t.Error("a reset conversation has no file yet")
	}
	drain(t, a.Send(context.Background(), "second"))

	list, _ := store.List()
	if len(list) != 2 || a.SessionID() == first {
		t.Errorf("sessions = %d, id = %s (first %s)", len(list), a.SessionID(), first)
	}
	old, _ := store.Load(first)
	if len(old.Items) == 0 {
		t.Error("the old conversation was lost")
	}
}

func TestUndoIsSaved(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	a := saved(t, srv, store)
	drain(t, a.Send(context.Background(), "first"))
	drain(t, a.Send(context.Background(), "second"))
	a.Undo()

	f, _ := store.Load(a.SessionID())
	b := newTestAgent(srv)
	if err := b.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := len(b.Messages()); got != 2 {
		t.Errorf("a resumed session has %d messages, want the 2 of the first exchange", got)
	}
}

func TestACompactedConversationIsSavedWithItsSummary(t *testing.T) {
	store := session.NewStore(t.TempDir())
	a, _ := talk(t, 3, summaryReply(goodSummary))
	a.EnableSessions(store, "/work", endpoint)
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	f, err := store.Load(a.SessionID())
	if err != nil || f.Summary == "" {
		t.Fatalf("summary not saved: %+v, %v", f, err)
	}
	b := newTestAgent(newFakeServer(t))
	if err := b.Resume(f, endpoint); err != nil || b.Summary() != a.Summary() || !b.HasConversation() {
		t.Errorf("resume: %v, summary %q", err, b.Summary())
	}
}

func TestAFailedSaveIsReportedAndTheTurnStillSucceeds(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil { // a file where the directory must go
		t.Fatal(err)
	}
	srv := newScriptedServer(t, chatReply("one"))
	a := saved(t, srv, session.NewStore(blocker))

	events := drain(t, a.Send(context.Background(), "hello"))

	if !hasNotice(events, "Could not save the session") {
		t.Errorf("events = %v", events)
	}
	if _, ok := lastEvent(events).(Done); !ok {
		t.Errorf("last event = %#v", lastEvent(events))
	}
}

func TestListSessionsDescribesThem(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	a := saved(t, srv, store)
	drain(t, a.Send(context.Background(), "what is up"))
	drain(t, a.Send(context.Background(), "and now"))

	list, err := a.ListSessions(false)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if s := list[0]; s.Turns != 2 || s.Prompt != "what is up" || !s.Current || s.Dir != "/work" {
		t.Errorf("info = %+v", s)
	}
	if _, err := newTestAgent(srv).ListSessions(false); err != ErrSessionsOff {
		t.Errorf("error = %v", err)
	}
}

func TestListSessionsIsPerDirectoryUnlessAll(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	here := saved(t, srv, store)
	elsewhere := newTestAgent(srv)
	elsewhere.EnableSessions(store, "/other", endpoint)
	drain(t, here.Send(context.Background(), "here"))
	drain(t, elsewhere.Send(context.Background(), "there"))

	if list, _ := here.ListSessions(false); len(list) != 1 || list[0].Prompt != "here" {
		t.Errorf("this directory: %+v", list)
	}
	if list, _ := here.ListSessions(true); len(list) != 2 {
		t.Errorf("all: %+v", list)
	}
}

func TestTheListKeepsTheFirstPromptAndTheTurnsAfterASummary(t *testing.T) {
	store := session.NewStore(t.TempDir())
	a, _ := talk(t, 4, summaryReply(goodSummary))
	a.EnableSessions(store, "/work", endpoint)
	if err := a.persist(); err != nil { // as after the first turn of a conversation that is saved from its start
		t.Fatal(err)
	}
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	list, _ := a.ListSessions(false)
	if len(list) != 1 || list[0].Turns != 4 || !strings.HasPrefix(list[0].Prompt, "question 1") {
		t.Errorf("list = %+v, want 4 turns and the first question", list)
	}

	f, _ := store.Load(a.SessionID())
	b := newTestAgent(newFakeServer(t))
	b.EnableSessions(store, "/work", endpoint)
	if err := b.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := b.persist(); err != nil {
		t.Fatal(err)
	}
	if again, _ := b.ListSessions(false); again[0].Turns != 4 || !strings.HasPrefix(again[0].Prompt, "question 1") {
		t.Errorf("after a resume and a save: %+v", again[0])
	}

	b.Reset()
	if b.history.replaced != 0 || b.saving.title != "" {
		t.Error("a reset conversation starts with no summarized turns and no title")
	}
}

func TestSwitchingModelDropsReasoningButKeepsTheRest(t *testing.T) {
	a := newTestAgent(newFakeServer(t))
	if err := a.Resume(fileWithReasoning("m", endpoint), endpoint); err != nil {
		t.Fatal(err)
	}
	a.SetModel("m") // the same model: nothing to drop
	if got := kinds(a.history.items); got != "user,reasoning,message" {
		t.Fatalf("same model: %s", got)
	}
	a.SetModel("other")
	if got := kinds(a.history.items); got != "user,message" {
		t.Errorf("other model: %s, want the reasoning gone", got)
	}
}
