package agent

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/openai/openai-go/v3/option"
)

// Models asks the server for the names of its models and returns them sorted. A name that could not be typed after
// /model, or shown safely (one with a space or a control character), is left out. It only reads the client, so unlike
// the other commands' methods it may run while a turn does.
func (a *Agent) Models(ctx context.Context) ([]string, error) {
	// No retries: the user is waiting, perhaps only for a name to be completed.
	pager := a.client.Models.ListAutoPaging(ctx, option.WithMaxRetries(0))
	var names []string
	for pager.Next() {
		name := pager.Current().ID
		if name != "" && !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
			names = append(names, name)
		}
	}
	if err := pager.Err(); err != nil {
		return nil, describeError(err)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
