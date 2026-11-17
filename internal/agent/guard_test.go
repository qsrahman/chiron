package agent

import (
	"fmt"
	"testing"
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
