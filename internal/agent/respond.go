package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// streamRetries is how often a request is sent again when its stream breaks. The SDK retries a request that
// fails to start, but not one that breaks halfway, and a turn of many rounds should not be lost to one dropped
// connection. Tools only run after a complete response, so a retry repeats a request, never an action.
const streamRetries = 2

// defaultIdleTimeout is how long a response stream may stay silent before the request is abandoned. It is
// generous because reasoning models can think for a while between events.
const defaultIdleTimeout = 2 * time.Minute

// errStreamIdle is the cancellation cause used when the idle timeout fires.
var errStreamIdle = errors.New("model stopped sending data")

// noText is what a response with neither text nor a call is reported as. Some compatible models now and then
// answer with nothing; asking again once usually gets a reply.
const noText = "No text in response"

// streamCut is a stream that stopped before its final response, with no reason from the server. A second try can
// get past it.
type streamCut struct{ reason string }

func (c *streamCut) Error() string { return c.reason }

// responseProblem describes what is wrong with a response, or returns "". A response that calls tools has no
// text, and that is fine: only the response that ends the turn needs some.
func responseProblem(r *responses.Response) string {
	switch r.Status {
	case responses.ResponseStatusIncomplete:
		reason := r.IncompleteDetails.Reason
		if reason == "" {
			reason = "unknown"
		}
		return "Response incomplete: " + reason
	case responses.ResponseStatusFailed:
		if r.Error.Message == "" {
			return "Response failed: unknown error"
		}
		return fmt.Sprintf("Response failed: %s: %s", r.Error.Code, r.Error.Message)
	case "", responses.ResponseStatusCompleted:
		// The status is optional: some compatible endpoints leave it out.
	default:
		return fmt.Sprintf("Response not completed: %s", r.Status)
	}
	if r.OutputText() != "" || len(functionCalls(r.Output)) > 0 {
		return ""
	}
	var refusal strings.Builder
	for _, o := range r.Output {
		for _, c := range o.Content {
			if c.Type == "refusal" {
				refusal.WriteString(c.Refusal)
			}
		}
	}
	if refusal.Len() > 0 {
		return "Refused: " + refusal.String()
	}
	return noText
}

func functionCalls(output []responses.ResponseOutputItemUnion) []responses.ResponseFunctionToolCall {
	var calls []responses.ResponseFunctionToolCall
	for _, o := range output {
		if o.Type == kindCall {
			calls = append(calls, o.AsFunctionCall())
		}
	}
	return calls
}

// request builds the API request for the turn's items.
func (a *Agent) request(items []item) responses.ResponseNewParams {
	p := responses.ResponseNewParams{
		Model: a.settings.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: a.input(items)},
		// We keep the history ourselves (see PLAN.md), so nothing is stored server-side. Reasoning
		// items can only be replayed in that mode when they carry their encrypted content.
		Store:   openai.Bool(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	}
	if a.instructions != "" {
		p.Instructions = openai.String(a.instructions)
	}
	if specs := a.tools.Specs(); len(specs) > 0 {
		p.Tools = specs
	}
	// Non-reasoning models reject a reasoning setting, so it is left out unless the user set one.
	if effort := a.settings.ReasoningEffort; effort != "" {
		p.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(effort)}
	}
	return p
}

// input is what the request carries as its input: the summary, if there is one, then the items.
func (a *Agent) input(items []item) []responses.ResponseInputItemUnionParam {
	in := params(items)
	if a.history.summary != "" {
		in = append([]responses.ResponseInputItemUnionParam{summaryMessage(a.history.summary)}, in...)
	}
	return in
}

// respond makes one request and returns the model's final response, whatever its status; the caller judges
// it with responseProblem. Text is sent as events before the status is looked at, so a partial reply is never
// hidden. A stream that breaks is sent again up to streamRetries times, with a Retry event each time.
func (a *Agent) respond(ctx context.Context, items []item, events chan<- Event) (*responses.Response, error) {
	p := a.request(items)
	if !a.settings.Streaming {
		return a.respondOnce(ctx, p, events)
	}
	for attempt := 1; ; attempt++ {
		response, err := a.streamOnce(ctx, p, events)
		var cut *streamCut
		if !errors.As(err, &cut) || attempt > streamRetries {
			return response, err
		}
		events <- Retry{Reason: fmt.Sprintf("%s; trying again (%d of %d)", cut.reason, attempt, streamRetries)}
	}
}

// respondOnce is respond without streaming: one request, the whole text at once.
func (a *Agent) respondOnce(ctx context.Context, p responses.ResponseNewParams, events chan<- Event) (*responses.Response, error) {
	response, err := a.client.Responses.New(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	if text := response.OutputText(); text != "" {
		events <- TextDelta{Text: text}
	}
	return response, nil
}

// streamOnce streams one request. It returns a *streamCut if the stream stopped without a final response and
// without saying why; any other error would get the same answer again.
func (a *Agent) streamOnce(parent context.Context, p responses.ResponseNewParams, events chan<- Event) (*responses.Response, error) {
	// Abandon the request if the stream goes silent. The timer restarts on every event.
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	idle := time.AfterFunc(a.idleTimeout, func() { cancel(errStreamIdle) })
	defer idle.Stop()

	stream := a.client.Responses.NewStreaming(ctx, p)
	defer stream.Close()

	var (
		final      *responses.Response
		done       []responses.ResponseOutputItemUnion // finished items, for servers that leave them out of the final event
		serverSaid string                              // the reason of an "error" event
		started    bool
	)
	for stream.Next() {
		started = true
		idle.Reset(a.idleTimeout)
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			events <- TextDelta{Text: event.Delta}
		case "response.output_item.done":
			done = append(done, event.AsResponseOutputItemDone().Item)
		case "response.completed", "response.incomplete", "response.failed":
			r := event.Response
			final = &r
		case "error":
			serverSaid = "Stream error: " + event.Message
			if event.Code != "" {
				serverSaid = fmt.Sprintf("Stream error: %s: %s", event.Code, event.Message)
			}
		}
	}

	if err := stream.Err(); err != nil {
		var api *openai.Error
		var streamErr *ssestream.StreamError
		switch {
		case errors.Is(context.Cause(ctx), errStreamIdle):
			return nil, fmt.Errorf("%w for %s", errStreamIdle, a.idleTimeout)
		case parent.Err() != nil, errors.As(err, &api), errors.As(err, &streamErr), !started:
			// Cancelled, refused by the API, an error event, or a request that never started (which the
			// SDK already retried): the same request would fail the same way.
			return nil, fmt.Errorf("streaming response: %w", err)
		}
		return nil, &streamCut{reason: "Stream cut off: " + strings.TrimRight(err.Error(), ".")}
	}
	if final == nil {
		if serverSaid != "" {
			return nil, errors.New(serverSaid) // the server said why; the same request would get the same answer
		}
		return nil, &streamCut{reason: "Stream ended without a final response"}
	}
	if len(final.Output) == 0 && len(done) > 0 {
		final.Output = done
	}
	return final, nil
}
