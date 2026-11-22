package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/tools"
)

// turnState is the turn that is running: what it has sent and done so far.
type turnState struct {
	prompt  string // what the user asked, to begin the turn again after a summary
	items   []item
	rounds  int  // rounds that got a response and ran its tools
	emptied bool // a response had nothing in it, and we asked again
	refused bool // the server refused a request as too long, and we shortened it once
	guard   loopGuard
}

// run executes a turn: ask the model, run the tool calls it makes, and repeat until it answers without calling
// a tool. What stays true:
//
//   - Every function call has its output before anything is sent or saved.
//   - The history changes only through history.save. A turn is saved whole, with its reply or with a closing
//     note, or not at all: once a round has run its tools (which may have had effects) a cancel, a failure, a
//     stop by a guard keeps the turn with a note, so the model knows what was done; a failure in
//     the first round saves nothing, since there is no work to keep and a prompt with no reply would leave two
//     user messages in a row.
//
// run holds what belongs to the turn as a whole: the budget check, the summary before it, the protection against a
// panic and the notice to a model that repeats itself, which only lives as long as the turn. A round is round.
func (a *Agent) run(ctx context.Context, text string, events chan<- Event) (err error) {
	// A panic outside the tools (they have their own protection) must not end the program either. History changes only
	// through save, so the turn is kept as for any failure: with a note if tools ran, and not at all if none did.
	var t *turnState
	defer func() {
		if r := recover(); r != nil {
			if t != nil && t.rounds > 0 {
				a.keepStopped(t, "an internal error")
			}
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	if budget, fixed := a.maxContext(), a.fixedTokens(); budget <= fixed {
		return fmt.Errorf("max_context_tokens (%d) is not more than the instructions and tool definitions alone (about %d tokens): raise it", budget, fixed)
	}
	a.compactIfDue(ctx, text, func(s string) { events <- Notice{Text: s} })
	t = &turnState{prompt: text, items: a.history.begin(text), guard: loopGuard{warnAt: a.loopWarnAt}}
	a.loopNote = ""
	defer func() { a.loopNote = "" }()

	// A turn has no cap on its model calls. A model stuck on one step is told, then stopped, by the loop guard; the
	// context budget bounds the size of every request, and the user can cancel.
	for {
		if over, err := a.round(ctx, t, events); over {
			return err
		}
	}
}

// round asks the model once and runs the tool calls it makes. It reports whether the turn is over, and how: a nil
// error is a finished turn. When it is not over, another round follows.
func (a *Agent) round(ctx context.Context, t *turnState, events chan<- Event) (over bool, err error) {
	response, err := a.respond(ctx, t.items, events)
	if err != nil {
		if isContextOverflow(err) && !t.refused {
			t.refused = true
			if a.shrink(ctx, t, t.prompt, events) {
				return false, nil
			}
		}
		if t.rounds > 0 {
			a.keepStopped(t, reasonOf(err))
		}
		return true, describeError(err)
	}

	problem := responseProblem(response)
	if problem == noText && !t.emptied {
		t.emptied = true // and happens once
		events <- Retry{Reason: "The model sent an empty reply; trying again"}
		return false, nil
	}
	events <- Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}
	if response.Usage.InputTokens > 0 {
		a.lastInput = response.Usage.InputTokens
	}

	calls := functionCalls(response.Output)
	if problem != "" || len(calls) == 0 {
		return true, a.finish(t, response, problem)
	}

	t.rounds++
	sent := len(t.items) // what the response answered
	t.items = append(t.items, outputItems(response.Output)...)
	outputs := a.runCallsReportingTodos(ctx, calls, events)
	for i, call := range calls {
		t.items = append(t.items, callOutputItem(call.CallID, outputs[i]))
	}
	if err := ctx.Err(); err != nil {
		a.keepStopped(t, reasonOf(err))
		return true, err // cancelled while tools ran; the model is not asked again
	}

	if err := a.fit(t, sent, response.Usage.InputTokens, func(s string) { events <- Notice{Text: s} }); err != nil {
		a.keepStopped(t, "its results passed the context budget")
		return true, err
	}

	verdict := t.guard.observe(calls, outputs)
	if verdict.stop != "" {
		a.keepStopped(t, verdict.stop)
		return true, errors.New(verdict.stop)
	}
	a.loopNote = verdict.note
	if verdict.notice != "" {
		events <- Notice{Text: verdict.notice}
	}
	return false, nil
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

// shrink makes the turn's request shorter after the server refused it as too long, and reports whether it did, so
// the same request can be sent again. Before the first round that means summarising, whatever auto_compact says;
// later, shortening the results of the rounds before.
func (a *Agent) shrink(ctx context.Context, t *turnState, prompt string, events chan<- Event) bool {
	if t.rounds == 0 {
		events <- Notice{Text: "Summarizing earlier messages…"}
		c, err := a.compact(ctx, "")
		if err != nil {
			return false
		}
		events <- Notice{Text: c.String()}
		t.items = a.history.begin(prompt)
	} else {
		results := 0
		for _, it := range t.items {
			if text, ok := outputText(it); ok {
				results += tools.EstimateText(text)
			}
		}
		// Half of them: shortened first, dropped only if that is not enough.
		if dropOutputs(t.items, len(t.items), results/2+1, true) == 0 {
			return false
		}
	}
	events <- Notice{Text: "The request was too long for the model; trying again, shorter"}
	return true
}
