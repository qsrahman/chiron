package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qsrahman/chiron/internal/session"
	"github.com/qsrahman/chiron/internal/tools"
)

func step(content, status string) Todo { return Todo{Content: content, Status: status} }

type todoArgs struct {
	Todos []Todo `json:"todos"`
}

func todoCall(id string, items ...Todo) string {
	return functionCall(id, "todo", todoArgs{items})
}

func instructionsOf(t *testing.T, srv *fakeServer, i int) string {
	t.Helper()
	s, _ := srv.request(t, i)["instructions"].(string)
	return s
}

// agentWithTodo is an agent with the todo tool and one other tool, which is what offers it.
func agentWithTodo(t *testing.T, srv *fakeServer, settings Settings) *Agent {
	t.Helper()
	a := New(srv.client(), settings, "base instructions", &tools.Registry{})
	reg, err := a.RegistryFor(fakeTool("other", true, 0, &recorder{}))
	if err != nil {
		t.Fatal(err)
	}
	a.SetTools(reg, "base instructions")
	return a
}

func TestTheTodoToolIsOfferedOnlyWithAnotherTool(t *testing.T) {
	a := newTestAgent(newFakeServer(t))

	none, err := a.RegistryFor()
	if err != nil || none.Len() != 0 {
		t.Fatalf("no other tool: %v, %d tools: a chat without tools must stay a plain chat", err, none.Len())
	}
	reg, err := a.RegistryFor(fakeTool("other", true, 0, &recorder{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(reg.Names(), ","); got != "other,todo" {
		t.Errorf("tools = %s", got)
	}
}

// The caller's slice may have room to spare, and appending to it would write the todo tool into the caller's array.
func TestRegistryForLeavesTheCallersSliceAlone(t *testing.T) {
	a := newTestAgent(newFakeServer(t))
	backing := make([]tools.Tool, 1, 4)
	backing[0] = fakeTool("other", true, 0, &recorder{})

	if _, err := a.RegistryFor(backing...); err != nil {
		t.Fatal(err)
	}

	if backing[:2][1] != nil {
		t.Error("the todo tool was written into the caller's slice")
	}
}

func TestTheTodoToolRunsWithoutAskingAndIsListedAsFree(t *testing.T) {
	srv := newFakeServer(t, callsThenAnswer(todoCall("c1", step("fix", "in_progress")))...)
	a := agentWithTodo(t, srv, Settings{Model: "m", Streaming: true}) // approvals are on

	events := drain(t, a.Send(context.Background(), "go"))

	for _, e := range events {
		if _, asked := e.(ApprovalRequested); asked {
			t.Fatal("the todo tool asked for approval")
		}
	}
	if err := lastErrorOrNil(events); err != nil {
		t.Fatal(err)
	}
	free := strings.Join(a.ToolList().Free, ",")
	if !strings.Contains(free, "todo") || strings.Contains(strings.Join(a.ToolList().Ask, ","), "todo") {
		t.Errorf("free = %s, ask = %v", free, a.ToolList().Ask)
	}
}

func lastErrorOrNil(events []Event) error {
	for _, e := range events {
		if err, ok := e.(Error); ok {
			return err.Err
		}
	}
	return nil
}

func TestAChangeOfTheListIsReportedOnceAndAnUnchangedOneNotAtAll(t *testing.T) {
	list := []Todo{step("read", "completed"), step("fix", "in_progress")}
	srv := newFakeServer(t,
		sse(completedWith(todoCall("c1", list...))), sse(textDelta("one"), completed("one")),
		sse(completedWith(todoCall("c2", list...))), sse(textDelta("two"), completed("two")),
	)
	a := agentWithTodo(t, srv, testSettings("m"))

	var updates []TodoUpdated
	for _, text := range []string{"first", "second"} {
		for _, e := range drain(t, a.Send(context.Background(), text)) {
			if u, ok := e.(TodoUpdated); ok {
				updates = append(updates, u)
			}
		}
	}

	if len(updates) != 1 || len(updates[0].Items) != 2 || updates[0].Items[1].Content != "fix" {
		t.Errorf("updates = %+v: the second call sent the same list and changed nothing", updates)
	}
	if got := a.Todos(); len(got) != 2 {
		t.Errorf("todos = %+v", got)
	}
}

func TestOpenStepsAreInTheInstructionsOfEveryLaterRequest(t *testing.T) {
	srv := newFakeServer(t,
		sse(completedWith(todoCall("c1", step("fix", "in_progress"), step("test", "pending")))), // request 0 answers it
		sse(textDelta("one"), completed("one")),                                                 // request 1
		sse(completedWith(todoCall("c2", step("fix", "completed"), step("test", "completed")))), // request 2
		sse(textDelta("two"), completed("two")),                                                 // request 3
		sse(textDelta("three"), completed("three")),                                             // request 4
	)
	a := agentWithTodo(t, srv, testSettings("m"))
	drain(t, a.Send(context.Background(), "first"))
	drain(t, a.Send(context.Background(), "second"))
	drain(t, a.Send(context.Background(), "third"))

	for i, want := range []bool{false, true, true, false, false} {
		got := strings.Contains(instructionsOf(t, srv, i), "# Your todo list")
		if got != want {
			t.Errorf("request %d: the list is in the instructions = %v, want %v", i, got, want)
		}
	}
	if in := instructionsOf(t, srv, 1); !strings.Contains(in, "[>] fix") || !strings.Contains(in, "[ ] test") || !strings.HasPrefix(in, "base instructions") {
		t.Errorf("instructions = %q", in)
	}
}

func TestTheBudgetCountsTheListThatEveryRequestCarries(t *testing.T) {
	a := agentWithTodo(t, newFakeServer(t), testSettings("m"))
	before := a.fixedTokens()

	if err := a.todos.Set([]Todo{step(strings.Repeat("a long step ", 10), "in_progress")}); err != nil {
		t.Fatal(err)
	}

	if after := a.fixedTokens(); after <= before || a.ContextEstimate().Instructions <= 0 {
		t.Errorf("fixed tokens %d -> %d: the list is part of every request", before, after)
	}
}

func TestARefusedListGoesBackToTheModelAsTextAndChangesNothing(t *testing.T) {
	srv := newFakeServer(t, callsThenAnswer(todoCall("c1", step("a", "in_progress"), step("b", "in_progress")))...)
	a := agentWithTodo(t, srv, testSettings("m"))

	events := drain(t, a.Send(context.Background(), "go"))

	var finished ToolFinished
	for _, e := range events {
		switch e := e.(type) {
		case ToolFinished:
			finished = e
		case TodoUpdated:
			t.Errorf("a refused list was reported: %+v", e)
		}
	}
	if finished.Outcome != CallFailed || !strings.Contains(finished.Output, "only one todo can be in_progress at a time") {
		t.Errorf("finished = %+v", finished)
	}
	if len(a.Todos()) != 0 {
		t.Errorf("todos = %+v", a.Todos())
	}
}

// The loop says that the arguments are invalid; the tool must not say it again.
func TestAListThatCannotBeReadSaysSoOnce(t *testing.T) {
	for name, arguments := range map[string]string{"not an object": `[1]`, "no list": `{}`} {
		t.Run(name, func(t *testing.T) {
			call := functionCall("c1", "todo", json.RawMessage(arguments))
			srv := newFakeServer(t, callsThenAnswer(call)...)
			a := agentWithTodo(t, srv, testSettings("m"))

			f := finished(drain(t, a.Send(context.Background(), "go")), "c1")

			if got := strings.Count(f.Output, "invalid arguments"); f.Outcome != CallFailed || got != 1 {
				t.Errorf("%d times in %q (outcome %v)", got, f.Output, f.Outcome)
			}
		})
	}
}

func TestResetForgetsTheList(t *testing.T) {
	a := agentWithTodo(t, newFakeServer(t), testSettings("m"))
	_ = a.todos.Set([]Todo{step("fix", "pending")})

	a.Reset()

	if len(a.Todos()) != 0 || strings.Contains(a.currentInstructions(), "todo list") {
		t.Errorf("todos = %+v", a.Todos())
	}
}

func TestTheListIsSavedWithTheSessionAndComesBackWithIt(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newFakeServer(t, callsThenAnswer(todoCall("c1", step("read", "completed"), step("fix", "in_progress")))...)
	a := saved(t, srv, store)
	reg, _ := a.RegistryFor(fakeTool("other", true, 0, &recorder{}))
	a.SetTools(reg, "")
	drain(t, a.Send(context.Background(), "go"))

	f, err := store.Load(a.SessionID())
	if err != nil || len(f.Todos) != 2 || f.Todos[1] != (session.Todo{Content: "fix", Status: "in_progress"}) {
		t.Fatalf("the file holds %+v, %v", f.Todos, err)
	}
	b := saved(t, srv, store)
	if err := b.Resume(f, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := b.Todos(); len(got) != 2 || got[1].Status != "in_progress" {
		t.Errorf("resumed todos = %+v", got)
	}
}

func TestResumingASessionWithoutAListClearsTheOneInMemory(t *testing.T) {
	store := session.NewStore(t.TempDir())
	srv := newScriptedServer(t, chatReply("one"))
	old := saved(t, srv, store)
	drain(t, old.Send(context.Background(), "no list here"))

	a := saved(t, srv, store)
	_ = a.todos.Set([]Todo{step("left over", "pending")})
	if err := a.ResumeSession(old.SessionID()); err != nil {
		t.Fatal(err)
	}

	if len(a.Todos()) != 0 {
		t.Errorf("the list of another conversation is still there: %+v", a.Todos())
	}
}
