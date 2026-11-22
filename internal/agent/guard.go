package agent

import (
	"crypto/sha256"
	"fmt"

	"github.com/openai/openai-go/v3/responses"
)

const (
	// A turn is stopped when one tool step (the same calls with the same results) shows up more than
	// loopMaxRepeats times among its last loopWindow steps. The step limit alone would let a stuck
	// model burn 25 requests first.
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
