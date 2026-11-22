package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func TestIsLooping(t *testing.T) {
	repeat := func(sig string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = sig
		}
		return out
	}
	distinct := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprint("step", i)
		}
		return out
	}

	tests := []struct {
		name  string
		steps []string
		want  bool
	}{
		{"no steps", nil, false},
		{"at the limit", repeat("a", loopMaxRepeats), false},
		{"one over the limit", repeat("a", loopMaxRepeats+1), true},
		{"all different", distinct(30), false},
		{"repeats spread over the window", append(append(repeat("a", loopMaxRepeats), distinct(3)...), "a"), true},
		// The 6 old repeats have scrolled out of the window, so only 2 count.
		{"old repeats are forgotten", append(append(repeat("a", 6), distinct(loopWindow-2)...), "a", "a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLooping(tt.steps); got != tt.want {
				t.Errorf("isLooping() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLastRepeatsCountsTheNewestStepInTheWindow(t *testing.T) {
	tests := []struct {
		name  string
		steps []string
		want  int
	}{
		{"no steps", nil, 0},
		{"a new step", []string{"a", "a", "a", "b"}, 1},
		{"three in a row", []string{"a", "a", "a"}, 3},
		{"spread out", []string{"a", "b", "a", "c", "a"}, 3},
		{"old ones left the window", append(append([]string{"a", "a", "a", "a", "a"}, make([]string, loopWindow-1)...), "a"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastRepeats(tt.steps); got != tt.want {
				t.Errorf("lastRepeats() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTheGuardWarnsOnceStopsAtTheLimitAndForgivesAChangeOfCourse(t *testing.T) {
	g := loopGuard{warnAt: loopWarnRepeats}
	same := []responses.ResponseFunctionToolCall{{CallID: "c", Name: "read", Arguments: `{"a":1}`}}
	other := []responses.ResponseFunctionToolCall{{CallID: "d", Name: "read", Arguments: `{"a":2}`}}
	out := []string{"result"}

	for i := 1; i <= loopWarnRepeats; i++ {
		if v := g.observe(same, out); v != (loopVerdict{}) {
			t.Fatalf("step %d: verdict %+v, want none while the model has repeated itself %d times or less", i, v, loopWarnRepeats)
		}
	}
	first := g.observe(same, out) // the step after which the model is told
	if first.note == "" || first.notice == "" || first.stop != "" {
		t.Fatalf("verdict %+v: the first repeat over the limit must give a note and a notice", first)
	}
	if again := g.observe(same, out); again.note == "" || again.notice != "" {
		t.Errorf("verdict %+v: the note goes on, the user is told once", again)
	}
	if v := g.observe(other, out); v != (loopVerdict{}) {
		t.Errorf("verdict %+v: a different step must clear the note", v)
	}
	for i := 0; i < loopWindow; i++ { // enough new steps for the old repeats to leave the window
		g.observe([]responses.ResponseFunctionToolCall{{CallID: "n", Name: "read", Arguments: fmt.Sprint(`{"n":`, i, `}`)}}, out)
	}
	for i := 0; i < loopWarnRepeats; i++ {
		g.observe(same, out)
	}
	if v := g.observe(same, out); v.notice == "" {
		t.Errorf("verdict %+v: repeating again after a change of course is news to the user again", v)
	}
	var stop loopVerdict
	for stop.stop == "" {
		stop = g.observe(same, out)
	}
	if !strings.Contains(stop.stop, "keeps repeating the same tool calls") {
		t.Errorf("stop = %q", stop.stop)
	}
}
