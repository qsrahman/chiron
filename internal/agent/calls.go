package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/tools"
)

const (
	// maxParallelCalls bounds the read-only calls that run at the same time.
	maxParallelCalls = 8
	// maxOutputBytes cuts a tool result: all of it goes into every later request.
	maxOutputBytes = 20 * 1024
)

// runCalls executes the calls and returns one output text per call, in call order.
// Each run of consecutive read-only calls executes concurrently (at most maxParallelCalls at a time); any
// other call runs alone, so mutations happen one at a time in the order the model gave them.
func (a *Agent) runCalls(ctx context.Context, calls []responses.ResponseFunctionToolCall, events chan<- Event) []string {
	outputs := make([]string, len(calls))
	slots := make(chan struct{}, maxParallelCalls)
	for start := 0; start < len(calls); {
		end := start + 1
		if a.isReadOnly(calls[start]) {
			for end < len(calls) && a.isReadOnly(calls[end]) {
				end++
			}
		}

		var wg sync.WaitGroup
		for i := start; i < end; i++ {
			wg.Go(func() {
				slots <- struct{}{}
				defer func() { <-slots }()
				outputs[i] = a.runCall(ctx, calls[i], events)
			})
		}
		wg.Wait()
		start = end
	}
	return outputs
}

func (a *Agent) isReadOnly(call responses.ResponseFunctionToolCall) bool {
	tool, ok := a.tools.Get(call.Name)
	return ok && tool.ReadOnly()
}

// callResult is how one call ended: the text the model reads, and how the front-end should show it.
type callResult struct {
	output  string
	outcome Outcome
}

func failure(text string) callResult { return callResult{text, CallFailed} }

// outcomeOf recovers how a call ended from the text the model read, for calls shown again after a resume.
func outcomeOf(output string) Outcome {
	switch {
	case output == interruptedOutput:
		return CallCancelled
	case output == deniedOutput:
		return CallDenied
	case strings.HasPrefix(output, "error: "):
		return CallFailed
	}
	return CallOK
}

func interrupted() callResult { return callResult{interruptedOutput, CallCancelled} }

// runCall runs one call and returns the text to give back to the model. Failures (unknown tool, bad
// arguments, a denial, tool errors) become that text instead of aborting the turn, so the model can recover. A
// call that was running, or had not run yet, when the turn was cancelled is answered as interrupted.
func (a *Agent) runCall(ctx context.Context, call responses.ResponseFunctionToolCall, events chan<- Event) string {
	events <- ToolStarted{CallID: call.CallID, Name: call.Name, Title: a.ToolTitle(call.Name), Arguments: call.Arguments}
	res := a.execute(ctx, call, events)
	events <- ToolFinished{CallID: call.CallID, Name: call.Name, Output: res.output, Outcome: res.outcome}
	return res.output
}

// ToolTitle is the name to show people for a tool, or "" if it has none or is not known.
func (a *Agent) ToolTitle(name string) string {
	if t, ok := a.tools.Get(name); ok {
		if titled, ok := t.(tools.Titled); ok {
			return titled.Title()
		}
	}
	return ""
}

func (a *Agent) execute(ctx context.Context, call responses.ResponseFunctionToolCall, events chan<- Event) callResult {
	tool, known := a.tools.Get(call.Name)
	switch {
	case !known:
		return failure(unknownToolText(call.Name, a.tools.Names()))
	case ctx.Err() != nil:
		return interrupted()
	}

	// A call that could not run is answered before the user is asked about it.
	if v, ok := tool.(tools.Validator); ok {
		if err := v.Validate(call.Arguments); err != nil {
			return failure("error: invalid arguments: " + err.Error())
		}
	}
	if !tool.ReadOnly() {
		switch decision, err := a.approve(ctx, tool, call, events); {
		case err != nil:
			return interrupted()
		case decision == Deny:
			return callResult{deniedOutput, CallDenied}
		}
	}

	result, err := tool.Run(ctx, call.Arguments)
	switch {
	case err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled):
		return interrupted()
	case err != nil:
		return failure("error: " + err.Error())
	case result == "":
		return callResult{"(no output)", CallOK}
	}
	return callResult{capOutput(result), CallOK}
}

// unknownToolText tells the model which tools exist, so it can correct the call.
func unknownToolText(name string, available []string) string {
	known := "none"
	if len(available) > 0 {
		known = strings.Join(available, ", ")
	}
	return fmt.Sprintf("error: unknown tool %q. Available tools: %s.", name, known)
}

// capOutput cuts a long tool result and says how much was left out.
func capOutput(text string) string {
	if len(text) <= maxOutputBytes {
		return text
	}
	cut := maxOutputBytes
	for !utf8.RuneStart(text[cut]) { // the cut may fall inside a multi-byte character
		cut--
	}
	return fmt.Sprintf("%s\n[output truncated: %d more characters]", text[:cut], utf8.RuneCountInString(text[cut:]))
}
