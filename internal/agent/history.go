package agent

import (
	"encoding/json"
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// interruptedOutput answers a call the user interrupted: it did not finish, or did not run. The model
// reads it, so it must say that the call may have done part of its work.
const interruptedOutput = "The user interrupted this call: it did not finish, or did not run."

// item is one entry of the conversation: the input item sent to the API and what kind it is. The kind is
// kept next to it because an item replayed from raw JSON (so unknown fields survive) cannot be asked.
type item struct {
	kind   string // "user", "assistant", or the API's own type: "reasoning", "function_call", "message", ...
	callID string // for "function_call" and "function_call_output"
	param  responses.ResponseInputItemUnionParam
}

const (
	kindUser       = "user"
	kindAssistant  = "assistant"
	kindCall       = "function_call"
	kindCallOutput = "function_call_output"
)

func userItem(text string) item {
	return item{kind: kindUser, param: responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleUser)}
}

func assistantItem(text string) item {
	return item{kind: kindAssistant, param: responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleAssistant)}
}

// outputItems turns response output items into input items for the next request. The raw JSON is
// replayed unchanged so reasoning items and fields this SDK version does not know survive.
func outputItems(output []responses.ResponseOutputItemUnion) []item {
	items := make([]item, 0, len(output))
	for _, o := range output {
		it := item{kind: o.Type, param: param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(o.RawJSON()))}
		if o.Type == kindCall {
			it.callID = o.AsFunctionCall().CallID
		}
		items = append(items, it)
	}
	return items
}

// callOutputItem answers one function call.
func callOutputItem(callID, output string) item {
	p := responses.ResponseInputItemParamOfFunctionCallOutput(output)
	p.OfFunctionCallOutput.CallID = openai.String(callID)
	return item{kind: kindCallOutput, callID: callID, param: p}
}

// answerOpenCalls adds an output for every function call that has none, so the history stays valid (the API
// rejects a call without an output) whatever stopped the turn.
func answerOpenCalls(items []item) []item {
	answered := map[string]bool{}
	for _, it := range items {
		if it.kind == kindCallOutput {
			answered[it.callID] = true
		}
	}
	for _, it := range slices.Clone(items) {
		if it.kind == kindCall && !answered[it.callID] {
			items = append(items, callOutputItem(it.callID, interruptedOutput))
		}
	}
	return items
}

// isToolWork reports the items that only matter while a turn runs and for the turn after it.
func isToolWork(kind string) bool {
	return kind == "reasoning" || kind == kindCall || kind == kindCallOutput
}

// history is the conversation as the next request starts from it. It changes in a few places only
// (save, and from Phase 3.8 undo and reset); a turn in progress works on a copy.
type history struct {
	items []item
}

// begin returns the items of a new turn: the history and the user's message. The history is not changed.
func (h *history) begin(prompt string) []item {
	return append(slices.Clone(h.items), userItem(prompt))
}

// save makes a finished turn the new history. The newest turn keeps everything, so a follow-up can refer to
// what its tools found; in every earlier turn only the messages stay, because the final reply says what the
// calls found and the reasoning, calls and outputs are the bulk of the history. Calls and outputs are dropped
// together, so no call is left without its output.
func (h *history) save(turn []item) {
	newest := 0
	for i, it := range turn {
		if it.kind == kindUser {
			newest = i
		}
	}
	kept := make([]item, 0, len(turn))
	for _, it := range turn[:newest] {
		if !isToolWork(it.kind) {
			kept = append(kept, it)
		}
	}
	h.items = append(kept, turn[newest:]...)
}

// params converts items to what the API takes.
func params(items []item) []responses.ResponseInputItemUnionParam {
	out := make([]responses.ResponseInputItemUnionParam, len(items))
	for i, it := range items {
		out[i] = it.param
	}
	return out
}
