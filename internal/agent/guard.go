package agent

import (
	"crypto/sha256"
	"fmt"

	"github.com/openai/openai-go/v3/responses"
)

const (
	// A turn is stopped when one tool step (the same calls with the same results) shows up more than
	// loopMaxRepeats times among its last loopWindow steps. A turn has no cap on its model calls, so this is what ends
	// a model that is stuck on one step.
	loopWindow     = 10
	loopMaxRepeats = 5

	// Before it is stopped, a model that repeats its latest step more than this many times in the window is told so,
	// in the instructions of its next request, and can correct itself. It still has the rounds up to the stop.
	loopWarnRepeats = 2
)

// stepSignature identifies one round of tool calls by what was asked and what came back, so a model that
// retries the same failing call is recognised while one that makes progress (different output) is not.
func stepSignature(calls []responses.ResponseFunctionToolCall, outputs []string) string {
	h := sha256.New()
	for i, call := range calls {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", call.Name, call.Arguments, outputs[i])
	}
	return string(h.Sum(nil))
}

// isLooping reports whether the recent steps repeat too much.
func isLooping(steps []string) bool {
	recent := steps[max(0, len(steps)-loopWindow):]
	counts := make(map[string]int, len(recent))
	for _, sig := range recent {
		counts[sig]++
		if counts[sig] > loopMaxRepeats {
			return true
		}
	}
	return false
}

// lastRepeats says how often the newest step shows up among the last loopWindow steps, itself included. It is 1 for a
// step that is new, so a model that changes course is no longer warned, although its old repeats are still in the window.
func lastRepeats(steps []string) int {
	if len(steps) == 0 {
		return 0
	}
	n := 0
	for _, sig := range steps[max(0, len(steps)-loopWindow):] {
		if sig == steps[len(steps)-1] {
			n++
		}
	}
	return n
}

// loopNote is what the model reads in its instructions while it repeats itself. It goes in the instructions, the
// channel the model trusts, and not in the tool result, which the prompt tells it to treat as data.
func loopNote(repeats int) string {
	return fmt.Sprintf("\n\n# Notice\n\nIn your last steps you made the same tool call with the same result %d times. "+
		"Repeating it will not change the result. Try a different approach, or stop and tell the user what is blocking you.", repeats)
}

// loopGuard watches the tool steps of one turn for a model that repeats itself, and says what to do about it.
type loopGuard struct {
	warnAt int      // the newest step may appear this often in the window before the model is told
	steps  []string // the signature of every step of the turn so far
	warned bool     // whether the user was told about the repeating that is going on
}

// loopVerdict is what a round of tool calls did to the guard.
type loopVerdict struct {
	stop   string // the reason the turn must stop, "" to go on
	note   string // for the instructions of the next request; "" when the model is not repeating itself
	notice string // for the user, once when the repeating begins; "" otherwise
}

// observe records a round of tool calls and their results.
func (g *loopGuard) observe(calls []responses.ResponseFunctionToolCall, outputs []string) loopVerdict {
	g.steps = append(g.steps, stepSignature(calls, outputs))
	if isLooping(g.steps) {
		return loopVerdict{stop: fmt.Sprintf("stopped: the model keeps repeating the same tool calls (more than %d of its last %d steps)", loopMaxRepeats, loopWindow)}
	}
	n := lastRepeats(g.steps)
	if n <= g.warnAt {
		g.warned = false
		return loopVerdict{}
	}
	v := loopVerdict{note: loopNote(n)}
	if !g.warned {
		g.warned = true
		v.notice = fmt.Sprintf("The model made the same tool call with the same result %d times; it was told to try something else", n)
	}
	return v
}
