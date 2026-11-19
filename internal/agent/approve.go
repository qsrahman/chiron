package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/tools"
)

// deniedOutput is what the model reads when the user refuses a call. The system prompt tells it not to repeat a
// denied call, and the two go together.
const deniedOutput = "error: the user denied this call; do not retry it or work around it"

// grant is what "allow for session" remembers: a tool, and the key its prompt gave (empty for the whole tool).
type grant struct{ tool, key string }

// approve decides whether a call to a tool that is not read-only may run, asking the user unless a grant or
// Settings.AutoApprove already answers. It returns the context's error if the turn is cancelled while waiting.
func (a *Agent) approve(ctx context.Context, tool tools.Tool, call responses.ResponseFunctionToolCall, events chan<- Event) (Decision, error) {
	p := promptFor(tool, call)
	g := grant{call.Name, p.GrantKey}
	if a.settings.AutoApprove || isTrusted(tool) || a.grants[g] {
		return Allow, nil
	}

	reply := make(chan Decision, 1)
	events <- ApprovalRequested{CallID: call.CallID, Tool: call.Name, Summary: p.Summary, Details: p.Details, GrantKey: p.GrantKey, Source: p.Source, Reply: reply}
	select {
	case d := <-reply:
		if d == AllowSession {
			a.grants[g] = true
		}
		return d, nil
	case <-ctx.Done():
		return Deny, ctx.Err()
	}
}

func isTrusted(tool tools.Tool) bool {
	t, ok := tool.(tools.Trusted)
	return ok && t.Trusted()
}

// promptFor asks the tool to describe the call, or shows the arguments as they are.
func promptFor(tool tools.Tool, call responses.ResponseFunctionToolCall) tools.Prompt {
	if d, ok := tool.(tools.Describer); ok {
		return d.Describe(call.Arguments)
	}
	return tools.Prompt{Summary: "Call " + call.Name, Details: tools.FormatArguments(call.Arguments)}
}

// ToolList says how each tool is treated: which run without asking, which the user allowed, which ask first.
type ToolList struct {
	Free    []string // read-only tools, and tools the configuration allows
	Allowed []string // allowed for the session: the tool, or "tool: key"
	Ask     []string // tools whose calls ask, unless allowed by key
}

// ToolList describes the tools for /tools.
func (a *Agent) ToolList() ToolList {
	var l ToolList
	for _, name := range a.tools.Names() {
		tool, _ := a.tools.Get(name)
		switch {
		case tool.ReadOnly() || isTrusted(tool):
			l.Free = append(l.Free, name)
		case !a.grants[grant{name, ""}]:
			l.Ask = append(l.Ask, name)
		}
	}
	for g := range a.grants {
		if g.key == "" {
			l.Allowed = append(l.Allowed, g.tool)
		} else {
			l.Allowed = append(l.Allowed, fmt.Sprintf("%s: %s", g.tool, g.key))
		}
	}
	slices.SortFunc(l.Allowed, strings.Compare)
	return l
}
