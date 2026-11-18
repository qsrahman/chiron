package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openai/openai-go/v3/responses"
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

// runCall runs one call and returns the text to give back to the model. Failures (unknown tool, bad
// arguments, tool errors) become that text instead of aborting the turn, so the model can recover. A call that
// was running, or had not run yet, when the turn was cancelled is answered as interrupted.
func (a *Agent) runCall(ctx context.Context, call responses.ResponseFunctionToolCall, events chan<- Event) string {
	events <- ToolStarted{CallID: call.CallID, Name: call.Name, Arguments: call.Arguments}

	output, failed, cancelled := "", false, false
	tool, known := a.tools.Get(call.Name)
	switch {
	case !known:
		output, failed = unknownToolText(call.Name, a.tools.Names()), true
	case ctx.Err() != nil:
		output, failed, cancelled = interruptedOutput, true, true
	default:
		result, err := tool.Run(ctx, call.Arguments)
		switch {
		case err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled):
			output, failed, cancelled = interruptedOutput, true, true
		case err != nil:
			output, failed = "error: "+err.Error(), true
		case result == "":
			output = "(no output)"
		default:
			output = capOutput(result)
		}
	}

	events <- ToolFinished{CallID: call.CallID, Name: call.Name, Output: output, Failed: failed, Cancelled: cancelled}
	return output
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
	kept := text[:maxOutputBytes]
	for !utf8.ValidString(kept) { // the cut may fall inside a multi-byte character
		kept = kept[:len(kept)-1]
	}
	return fmt.Sprintf("%s\n[output truncated: %d more characters]", kept, utf8.RuneCountInString(text[len(kept):]))
}
