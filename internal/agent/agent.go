// Package agent runs conversations with the OpenAI Responses API and reports progress as events.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/desk-agent/internal/tools"
)

// defaultIdleTimeout is how long a response stream may stay silent before the request is abandoned. It is
// generous because reasoning models can think for a while between events.
const defaultIdleTimeout = 2 * time.Minute

// errStreamIdle is the cancellation cause used when the idle timeout fires.
var errStreamIdle = errors.New("model stopped sending data")

// maxSteps bounds the model calls in one turn, so a model that keeps calling tools cannot run (and bill) forever.
const maxSteps = 25

// Agent holds one conversation. It is not safe for concurrent use: wait for the
// channel returned by Send to close before calling Send again.
type Agent struct {
	client       openai.Client
	model        string
	instructions string
	tools        *tools.Registry
	idleTimeout  time.Duration
	history      []responses.ResponseInputItemUnionParam
}

// New returns an agent with an empty history. registry holds the tools the model may call; use
// &tools.Registry{} for none.
func New(client openai.Client, model, instructions string, registry *tools.Registry) *Agent {
	return &Agent{client: client, model: model, instructions: instructions, tools: registry, idleTimeout: defaultIdleTimeout}
}

// Send starts a turn for the user's text and streams events on the returned channel.
//
// The channel is closed after a final Done or Error event. The caller must read it until
// it is closed; cancel ctx to stop a turn early. History only changes when the turn
// succeeds, so a failed or cancelled turn can simply be retried.
func (a *Agent) Send(ctx context.Context, text string) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		if err := a.turn(ctx, text, events); err != nil {
			events <- Error{Err: err}
			return
		}
		events <- Done{}
	}()
	return events
}

// turn runs the model, executes the tool calls it asks for, and repeats until it answers without
// calling a tool. History is only replaced once the whole turn has succeeded.
func (a *Agent) turn(ctx context.Context, text string, events chan<- Event) error {
	user := responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleUser)
	input := append(slices.Clone(a.history), user)
	var steps []string // signatures of the tool steps so far, for loop detection

	for range maxSteps {
		response, err := a.respond(ctx, input, events)
		if err != nil {
			return err
		}
		events <- Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}
		input = append(input, outputAsInput(response.Output)...)

		calls := functionCalls(response.Output)
		if len(calls) == 0 {
			a.history = input
			return nil
		}
		outputs := a.runCalls(ctx, calls, events)
		input = append(input, functionOutputs(calls, outputs)...)
		if err := ctx.Err(); err != nil {
			return err // cancelled while tools ran; the model is not asked again
		}

		steps = append(steps, stepSignature(calls, outputs))
		if isLooping(steps) {
			return fmt.Errorf("stopped: the model keeps repeating the same tool calls (more than %d of its last %d steps)", loopMaxRepeats, loopWindow)
		}
	}
	return fmt.Errorf("stopped after %d model calls without a final answer", maxSteps)
}

// respond makes one streaming request and returns the completed response.
func (a *Agent) respond(ctx context.Context, input []responses.ResponseInputItemUnionParam, events chan<- Event) (*responses.Response, error) {
	params := responses.ResponseNewParams{
		Model: a.model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		// We keep the history ourselves (see PLAN.md), so nothing is stored server-side. Reasoning
		// items can only be replayed in that mode when they carry their encrypted content.
		Store:   openai.Bool(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	}
	if a.instructions != "" {
		params.Instructions = openai.String(a.instructions)
	}
	if specs := a.tools.Specs(); len(specs) > 0 {
		params.Tools = specs
	}

	// Abandon the request if the stream goes silent. The timer restarts on every event.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	idle := time.AfterFunc(a.idleTimeout, func() { cancel(errStreamIdle) })
	defer idle.Stop()

	stream := a.client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	// The completed event carries the whole response, so no accumulator is needed.
	var completed *responses.Response
	for stream.Next() {
		idle.Reset(a.idleTimeout)
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			events <- TextDelta{Text: event.Delta}
		case "response.completed":
			r := event.AsResponseCompleted().Response
			completed = &r
		case "response.failed":
			return nil, fmt.Errorf("response failed: %s", event.AsResponseFailed().Response.Error.Message)
		case "response.incomplete":
			return nil, fmt.Errorf("response incomplete: %s", event.AsResponseIncomplete().Response.IncompleteDetails.Reason)
		case "error":
			return nil, fmt.Errorf("api error: %s", event.AsError().Message)
		}
	}
	if err := stream.Err(); err != nil {
		if errors.Is(context.Cause(ctx), errStreamIdle) {
			return nil, fmt.Errorf("%w for %s", errStreamIdle, a.idleTimeout)
		}
		return nil, fmt.Errorf("streaming response: %w", err)
	}
	if completed == nil {
		return nil, errors.New("stream ended without a completed response")
	}
	return completed, nil
}

func functionCalls(output []responses.ResponseOutputItemUnion) []responses.ResponseFunctionToolCall {
	var calls []responses.ResponseFunctionToolCall
	for _, item := range output {
		if item.Type == "function_call" {
			calls = append(calls, item.AsFunctionCall())
		}
	}
	return calls
}

// runCalls executes the calls and returns one output text per call, in call order.
// Each run of consecutive read-only calls executes concurrently; any other call runs alone, so
// mutations happen one at a time in the order the model gave them.
func (a *Agent) runCalls(ctx context.Context, calls []responses.ResponseFunctionToolCall, events chan<- Event) []string {
	outputs := make([]string, len(calls))
	for start := 0; start < len(calls); {
		end := start + 1
		if a.isReadOnly(calls[start]) {
			for end < len(calls) && a.isReadOnly(calls[end]) {
				end++
			}
		}

		var wg sync.WaitGroup
		for i := start; i < end; i++ {
			wg.Go(func() { outputs[i] = a.runCall(ctx, calls[i], events) })
		}
		wg.Wait()
		start = end
	}
	return outputs
}

// functionOutputs turns the call outputs into the function_call_output items the model expects next.
func functionOutputs(calls []responses.ResponseFunctionToolCall, outputs []string) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, len(calls))
	for i, call := range calls {
		item := responses.ResponseInputItemParamOfFunctionCallOutput(outputs[i])
		item.OfFunctionCallOutput.CallID = openai.String(call.CallID)
		items[i] = item
	}
	return items
}

func (a *Agent) isReadOnly(call responses.ResponseFunctionToolCall) bool {
	tool, ok := a.tools.Get(call.Name)
	return ok && tool.ReadOnly()
}

// runCall runs one call and returns the text to give back to the model. Failures (unknown tool,
// bad arguments, tool errors) become that text instead of aborting the turn, so the model can recover.
func (a *Agent) runCall(ctx context.Context, call responses.ResponseFunctionToolCall, events chan<- Event) string {
	events <- ToolStarted{CallID: call.CallID, Name: call.Name, Arguments: call.Arguments}

	output, failed := "", false
	if tool, ok := a.tools.Get(call.Name); !ok {
		output, failed = fmt.Sprintf("error: unknown tool %q", call.Name), true
	} else if result, err := tool.Run(ctx, call.Arguments); err != nil {
		output, failed = "error: "+err.Error(), true
	} else if result == "" {
		output = "(no output)"
	} else {
		output = result
	}

	events <- ToolFinished{CallID: call.CallID, Name: call.Name, Output: output, Failed: failed}
	return output
}

// outputAsInput turns response output items into input items for the next request. The raw JSON
// is replayed unchanged so reasoning items and fields this SDK version does not know survive.
func outputAsInput(output []responses.ResponseOutputItemUnion) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(output))
	for _, item := range output {
		items = append(items, param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(item.RawJSON())))
	}
	return items
}
