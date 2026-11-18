package agent

import (
	"encoding/json"

	"github.com/qsrahman/desk-agent/internal/tools"
)

// tokens estimates one history item. The opaque encrypted content of a reasoning item is left out: the API counts
// the few tokens of reasoning it holds, not its length.
func (it item) tokens() int {
	if it.kind != kindReasoning {
		return tools.EstimateTokens(it.param)
	}
	raw, err := json.Marshal(it.param)
	if err != nil {
		return 0
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return tools.EstimateTokens(it.param)
	}
	delete(fields, "encrypted_content")
	return tools.EstimateTokens(fields)
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
		Instructions: tools.EstimateTokens(map[string]string{"instructions": a.instructions}),
		ToolCount:    a.tools.Len(),
		LastInput:    a.lastInput,
	}
	if c.ToolCount > 0 {
		c.Tools = tools.EstimateTokens(a.tools.Specs())
	}
	for _, it := range a.history.items {
		c.History += it.tokens()
	}
	return c
}
