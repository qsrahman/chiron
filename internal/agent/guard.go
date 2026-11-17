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
