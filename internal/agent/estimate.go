package agent

import "encoding/json"

// estimateTokens counts about one token per four bytes of the JSON of v. That is tokenizer-agnostic, so it suits
// any backend, and counting UTF-8 bytes keeps non-Latin text from being undercounted.
func estimateTokens(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(raw)/4 + 1
}

// tokens estimates one history item. The opaque encrypted content of a reasoning item is left out: the API counts
// the few tokens of reasoning it holds, not its length.
func (it item) tokens() int {
	raw, err := json.Marshal(it.param)
	if err != nil {
		return 0
	}
	if it.kind == "reasoning" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil {
			delete(fields, "encrypted_content")
			raw, _ = json.Marshal(fields)
		}
	}
	return len(raw)/4 + 1
}

// ContextEstimate is the size of what the next request would carry, in estimated tokens.
type ContextEstimate struct {
	Instructions int
	Tools        int
	ToolCount    int
	History      int
	LastInput    int64 // what the server counted for the last request, 0 if not known
}

// Total is the estimate for the whole request, before the new prompt.
func (c ContextEstimate) Total() int { return c.Instructions + c.Tools + c.History }

// ContextEstimate estimates the instructions, the definitions of the tools and the history, which together are
// what every request carries.
func (a *Agent) ContextEstimate() ContextEstimate {
	c := ContextEstimate{
		Instructions: estimateTokens(map[string]string{"instructions": a.instructions}),
		ToolCount:    a.tools.Len(),
		LastInput:    a.lastInput,
	}
	if c.ToolCount > 0 {
		c.Tools = estimateTokens(a.tools.Specs())
	}
	for _, it := range a.history.items {
		c.History += it.tokens()
	}
	return c
}
