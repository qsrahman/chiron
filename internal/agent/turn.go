package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3/responses"
)

// turnState is the turn that is running: what it has sent and done so far.
type turnState struct {
	items   []item
	rounds  int  // rounds that got a response and ran its tools
	emptied bool // a response had nothing in it, and we asked again
}

// run executes a turn: ask the model, run the tool calls it makes, and repeat until it answers without calling
// a tool. What stays true:
//
//   - Every function call has its output before anything is sent or saved.
//   - The history changes only through history.save. A turn is saved whole, with its reply or with a closing
//     note, or not at all: once a round has run its tools (which may have had effects) a cancel, a failure, a
//     stop by a guard or the step cap keeps the turn with a note, so the model knows what was done; a failure in
//     the first round saves nothing, since there is no work to keep and a prompt with no reply would leave two
//     user messages in a row.
func (a *Agent) run(ctx context.Context, text string, events chan<- Event) error {
	t := &turnState{items: a.history.begin(text)}
	var steps []string // signatures of the tool steps so far, for loop detection

	for asked := 0; asked < a.settings.MaxTurns; {
		response, err := a.respond(ctx, t.items, events)
		if err != nil {
			if t.rounds > 0 {
				a.keepStopped(t, reasonOf(err))
			}
			return describeError(err)
		}

		problem := responseProblem(response)
		if problem == noText && !t.emptied {
			t.emptied = true // asking again is not one of the calls the cap counts, and happens once
			events <- Retry{Reason: "The model sent an empty reply; trying again"}
			continue
		}
		asked++
		events <- Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}
		if response.Usage.InputTokens > 0 {
			a.lastInput = response.Usage.InputTokens
		}

		calls := functionCalls(response.Output)
		if problem != "" || len(calls) == 0 {
			return a.finish(t, response, problem)
		}

		t.rounds++
		t.items = append(t.items, outputItems(response.Output)...)
		outputs := a.runCalls(ctx, calls, events)
		for i, call := range calls {
			t.items = append(t.items, callOutputItem(call.CallID, outputs[i]))
		}
		if err := ctx.Err(); err != nil {
			a.keepStopped(t, reasonOf(err))
			return err // cancelled while tools ran; the model is not asked again
		}

		steps = append(steps, stepSignature(calls, outputs))
		if isLooping(steps) {
			message := fmt.Sprintf("stopped: the model keeps repeating the same tool calls (more than %d of its last %d steps)", loopMaxRepeats, loopWindow)
			a.keepStopped(t, message)
			return errors.New(message)
		}
	}

	message := fmt.Sprintf("Stopped after %d model calls without a final answer", a.settings.MaxTurns)
	a.save(t, message)
	return errors.New(message)
}

// finish ends a turn on a response that calls no tool, or has a problem. A reply is kept if it is complete, or
// was cut off after text the user has already seen.
func (a *Agent) finish(t *turnState, response *responses.Response, problem string) error {
	text := response.OutputText()
	cutOff := response.Status == responses.ResponseStatusIncomplete
	switch {
	case text != "" && (problem == "" || cutOff):
		a.history.save(append(t.items, assistantItem(text)))
	case problem != "" && t.rounds > 0:
		a.keepStopped(t, problem)
	}
	if problem != "" {
		return errors.New(problem)
	}
	return nil
}

// keepStopped saves a turn that ended after `rounds` completed rounds without an answer, closed with a note, so
// the model still knows the work already done.
func (a *Agent) keepStopped(t *turnState, reason string) {
	a.save(t, fmt.Sprintf("Stopped after %d model calls: %s", t.rounds, reason))
}

// save keeps the turn closed with an assistant note, with every call answered.
func (a *Agent) save(t *turnState, note string) {
	items := answerOpenCalls(t.items)
	a.history.save(append(items, assistantItem("["+note+"]")))
}
