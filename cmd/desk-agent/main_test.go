package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "desk-agent") {
		t.Errorf("stdout = %q, want it to contain the program name", out.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
