package mcp

import (
	"strings"
	"testing"
)

func lookupIn(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { v, ok := env[name]; return v, ok }
}

func TestExpand(t *testing.T) {
	env := map[string]string{"A": "alpha", "B": "${A}", "EMPTY": ""}
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"x-${A}-y", "x-alpha-y"},
		{"${A}${A}", "alphaalpha"},
		{"${EMPTY}end", "end"},
		{"$A is not expanded", "$A is not expanded"},
		{"${B}", "${A}"}, // the text that was put in is not scanned again
		{"a $ b", "a $ b"},
		{"${not closed", "${not closed"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := expand(tt.in, lookupIn(env))
			if err != nil || got != tt.want {
				t.Errorf("expand(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestExpandNamesAnUnsetVariableAndNothingElse(t *testing.T) {
	_, err := expand("token=${SECRET_TOKEN} and ${A}", lookupIn(map[string]string{"A": "visible-value"}))

	if err == nil || !strings.Contains(err.Error(), "${SECRET_TOKEN}") {
		t.Fatalf("err = %v, want it to name the variable", err)
	}
	if strings.Contains(err.Error(), "visible-value") {
		t.Error("the error repeats a value")
	}
}
