package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/tools"
)

// A panic in a tool would end the whole program, with the terminal still in the state of the interface. It fails the
// call instead, and the model can go on.
func TestAToolThatPanicsFailsItsCallAndTheTurnGoesOn(t *testing.T) {
	for _, readOnly := range []bool{true, false} { // the two ways calls are run: in parallel, and alone
		name := "alone"
		if readOnly {
			name = "in parallel"
		}
		t.Run(name, func(t *testing.T) {
			boom := tools.MustNew("boom", "panics", readOnly, func(context.Context, nameArgs) (string, error) {
				panic(fmt.Errorf("index %d out of range", 3)) // a runtime error, like a bug in a tool
			})
			srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "boom", nameArgs{"a"}))...)
			a := newAgentWithTools(t, srv, boom)

			events := drain(t, a.Send(context.Background(), "go"))

			var finished ToolFinished
			var done bool
			for _, e := range events {
				switch e := e.(type) {
				case ToolFinished:
					finished = e
				case Done:
					done = true
				}
			}
			if finished.Outcome != CallFailed || !strings.Contains(finished.Output, "error: the tool boom panicked: index 3 out of range") {
				t.Errorf("finished = %+v", finished)
			}
			if !done || lastErrorOrNil(events) != nil {
				t.Errorf("the turn did not go on to the model's answer: done %v, error %v", done, lastErrorOrNil(events))
			}
		})
	}
}

// specBomb is a tool whose definition panics once it is armed, which is a panic in the turn itself and not in a call.
type specBomb struct {
	tools.Tool
	armed *bool
}

func (b specBomb) Spec() responses.ToolUnionParam {
	if *b.armed {
		panic("the definition exploded")
	}
	return b.Tool.Spec()
}

func (b specBomb) Run(ctx context.Context, arguments string) (string, error) {
	*b.armed = true // from now on every request panics
	return b.Tool.Run(ctx, arguments)
}

func TestAPanicInTheTurnEndsItWithAnErrorAndSavesNothingBeforeAnyToolRan(t *testing.T) {
	armed := true
	srv := newFakeServer(t, sse(textDelta("hi"), completed("hi")))
	a := newAgentWithTools(t, srv, specBomb{fakeTool("read", true, 0, &recorder{}), &armed})

	events := drain(t, a.Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "internal error: the definition exploded") {
		t.Errorf("error = %v", err)
	}
	if a.HasConversation() {
		t.Error("a turn that did nothing was kept")
	}
}

func TestAPanicInTheTurnAfterToolsRanKeepsTheTurnWithANote(t *testing.T) {
	armed := false
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "read", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, specBomb{fakeTool("read", true, 0, &recorder{}), &armed})

	events := drain(t, a.Send(context.Background(), "go"))

	if err := lastError(t, events); !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("error = %v", err)
	}
	// The tool ran and may have had effects, so the model must know: the turn is kept, closed with a note.
	if note := lastNote(a); note != "[Stopped after 1 model calls: an internal error]" {
		t.Errorf("closing note = %q", note)
	}
	if got := kinds(a.history.items); !strings.Contains(got, "function_call_output") {
		t.Errorf("the call has no output in the kept turn: %s", got)
	}
}
