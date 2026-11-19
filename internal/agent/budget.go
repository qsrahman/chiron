package agent

import (
	"fmt"
	"unicode/utf8"

	"github.com/qsrahman/chiron/internal/tools"
)

const (
	// defaultMaxContextTokens is the budget for a whole request (instructions, tool definitions and history).
	// It sits well below the window of most models, because the estimate is rough.
	defaultMaxContextTokens = 64 * 1024

	// A tool result longer than shortenOver characters is cut to its first keepHead and last keepTail characters.
	shortenOver = 8000
	keepHead    = 4000
	keepTail    = 1000

	// droppedOutput replaces a result when shortening is not enough.
	droppedOutput = "[output dropped to fit the context]"
)

// shorten keeps the start and the end of a long text, which is what the model needs to read, and says how much
// is hidden.
func shorten(text string) string {
	if utf8.RuneCountInString(text) <= shortenOver {
		return text
	}
	runes := []rune(text)
	hidden := len(runes) - keepHead - keepTail
	return string(runes[:keepHead]) +
		fmt.Sprintf("\n[... %d characters hidden; call the tool again if you need them ...]\n", hidden) +
		string(runes[len(runes)-keepTail:])
}

// outputText is the text of a function call output item.
func outputText(it item) (string, bool) {
	if it.kind != kindCallOutput || it.param.OfFunctionCallOutput == nil {
		return "", false
	}
	return it.param.OfFunctionCallOutput.Output.OfString.Value, true
}

// dropOutputs makes the results of items[:upto] smaller, oldest first, until about `needed` tokens are freed:
// first by shortening the long ones, then, if placeholders is true, by replacing results with a placeholder. It
// replaces items and never edits one, because they are shared with the history. It returns the tokens freed.
func dropOutputs(items []item, upto, needed int, placeholders bool) int {
	freed := 0
	replace := func(text func(string) string) {
		for i := 0; i < upto && freed < needed; i++ {
			old, ok := outputText(items[i])
			if !ok {
				continue
			}
			replacement := text(old)
			if replacement == old {
				continue
			}
			next := callOutputItem(items[i].callID, replacement)
			freed += items[i].tokens() - next.tokens()
			items[i] = next
		}
	}
	replace(shorten)
	if placeholders {
		replace(func(string) string { return droppedOutput })
	}
	return freed
}

// maxContext is the budget for a request.
func (a *Agent) maxContext() int {
	if a.settings.MaxContextTokens > 0 {
		return a.settings.MaxContextTokens
	}
	return defaultMaxContextTokens
}

// fixedTokens is what every request carries whatever the history: the instructions and the tool definitions.
func (a *Agent) fixedTokens() int { return a.instructionTokens() + a.toolTokens() }

func (a *Agent) instructionTokens() int {
	return tools.EstimateTokens(map[string]string{"instructions": a.instructions})
}

func (a *Agent) toolTokens() int {
	if a.tools.Len() == 0 {
		return 0
	}
	return tools.EstimateTokens(a.tools.Specs())
}

// requestTokens estimates the whole request for these items, summary included.
func (a *Agent) requestTokens(items []item) int {
	n := a.fixedTokens()
	if s := a.history.summary; s != "" {
		n += tools.EstimateTokens(summaryMessage(s))
	}
	for _, it := range items {
		n += it.tokens()
	}
	return n
}

// fit makes the next request of a turn fit the budget after a round of tools. `sent` is how many items the last
// response answered, so the server's count of that request is exact for them and only what came after is
// estimated. Results of earlier rounds go first, so the work goes on; the latest ones are what the model has to
// read next. It returns a note for the user for each step it took, and an error when even that is not enough.
func (a *Agent) fit(t *turnState, sent int, serverInput int64, notice func(string)) error {
	budget := a.maxContext()
	used := a.requestTokens(t.items)
	if serverInput > 0 {
		used = int(serverInput)
		for _, it := range t.items[sent:] {
			used += it.tokens()
		}
	}
	if used <= budget {
		return nil
	}
	if freed := dropOutputs(t.items, sent, used-budget, true); freed > 0 {
		notice("Context full: older tool results shortened or dropped")
		used -= freed
	}
	if used <= budget {
		return nil
	}
	if freed := dropOutputs(t.items, len(t.items), used-budget, false); freed > 0 {
		notice("Context full: this round's tool results shortened")
		used -= freed
	}
	if used <= budget {
		return nil
	}
	// The calls have run and may have had effects: keep the turn, with its results dropped, or every later request
	// would carry them.
	dropOutputs(t.items, len(t.items), used, true)
	return fmt.Errorf("this turn grew past the context budget (about %d of %d tokens); ask for less, or raise max_context_tokens", used, budget)
}
