// Package agent runs conversations with the OpenAI Responses API and reports progress as events.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// DefaultInstructions is the system prompt used when the caller does not supply one.
const DefaultInstructions = "You are desk-agent, an AI coding assistant running in the user's terminal. Be concise and accurate."

// Agent holds one conversation. It is not safe for concurrent use: wait for the
// channel returned by Send to close before calling Send again.
type Agent struct {
	client       openai.Client
	model        string
	instructions string
	history      []responses.ResponseInputItemUnionParam
}

// New returns an agent with an empty history.
func New(client openai.Client, model, instructions string) *Agent {
	return &Agent{client: client, model: model, instructions: instructions}
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

func (a *Agent) turn(ctx context.Context, text string, events chan<- Event) error {
	user := responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleUser)
	input := append(slices.Clone(a.history), user)

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

	stream := a.client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	// The completed event carries the whole response, so no accumulator is needed.
	var completed *responses.Response
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			events <- TextDelta{Text: event.Delta}
		case "response.completed":
			r := event.AsResponseCompleted().Response
			completed = &r
		case "response.failed":
			return fmt.Errorf("response failed: %s", event.AsResponseFailed().Response.Error.Message)
		case "response.incomplete":
			return fmt.Errorf("response incomplete: %s", event.AsResponseIncomplete().Response.IncompleteDetails.Reason)
		case "error":
			return fmt.Errorf("api error: %s", event.AsError().Message)
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("streaming response: %w", err)
	}
	if completed == nil {
		return errors.New("stream ended without a completed response")
	}

	a.history = append(input, outputAsInput(completed.Output)...)
	return nil
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
