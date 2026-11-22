//go:build integration

package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/chiron/internal/config"
	"github.com/qsrahman/chiron/internal/tools"
)

// TestRealAPIReplay talks to the real API (costs a few tokens). It checks what the fake server
// cannot: that the model name is valid, that include=reasoning.encrypted_content is accepted, and
// that a second turn replaying the first turn's output items works with store=false.
//
//	go test -tags=integration -run TestRealAPI -v -count=1 ./internal/agent
//
// Settings come from the default config file and the environment, exactly like the CLI.
func TestRealAPIReplay(t *testing.T) {
	a := realAgent(t, &tools.Registry{})
	ask := func(prompt string) string { return askAgent(t, a, prompt) }

	first := ask("Remember this code word: PINEAPPLE-42. Reply with just OK.")
	t.Logf("turn 1 answer: %q (history items: %d)", first, len(a.history.items))

	second := ask("What was the code word? Reply with just the word.")
	t.Logf("turn 2 answer: %q (history items: %d)", second, len(a.history.items))
	if !strings.Contains(second, "PINEAPPLE-42") {
		t.Errorf("second turn did not recall the code word, so history replay may be broken: %q", second)
	}
}

// TestRealAPIToolCall checks the tool loop against the real model: it must call the tool, use the
// result, and still remember both on the next turn (replaying function_call items and any reasoning).
func TestRealAPIToolCall(t *testing.T) {
	var calls atomic.Int32
	lookup := tools.MustNew("lookup_code", "Returns the secret access code. Takes no arguments.", true,
		func(context.Context, struct{}) (string, error) {
			calls.Add(1)
			return "ZEBRA-7", nil
		})
	reg, err := tools.NewRegistry(lookup)
	if err != nil {
		t.Fatal(err)
	}
	a := realAgent(t, reg)

	first := askAgent(t, a, "Call the lookup_code tool and tell me the code it returns.")
	t.Logf("turn 1 answer: %q (tool calls: %d, history items: %d)", first, calls.Load(), len(a.history.items))
	if calls.Load() == 0 || !strings.Contains(first, "ZEBRA-7") {
		t.Errorf("the model did not call the tool and report its result: calls=%d answer=%q", calls.Load(), first)
	}

	second := askAgent(t, a, "What was the code again? Do not call any tool.")
	t.Logf("turn 2 answer: %q", second)
	if !strings.Contains(second, "ZEBRA-7") {
		t.Errorf("second turn forgot the tool result: %q", second)
	}
}

// realAgent builds an agent from the default config file and the environment, or skips the test.
func realAgent(t *testing.T, registry *tools.Registry) *Agent {
	t.Helper()
	var file config.File
	path, _ := config.DefaultPath()
	if path != "" {
		var err error
		if file, err = config.Load(path, os.Stderr); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	cfg, err := config.Resolve(file, path, os.Getenv("OPENAI_API_KEY"), config.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey == "" && cfg.BaseURL == "" {
		t.Skip("no API key: set OPENAI_API_KEY or api_key in the config file")
	}

	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	t.Logf("model=%s base_url=%q", cfg.Model, cfg.BaseURL)
	// Use the same system prompt as the CLI so the check covers it too.
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := BuildInstructions(context.Background(), workDir, time.Now(), registry.Len() > 0)
	if err != nil {
		t.Fatal(err)
	}
	return New(openai.NewClient(opts...), Settings{Model: cfg.Model, ReasoningEffort: cfg.ReasoningEffort, MaxTurns: cfg.MaxTurns, Streaming: cfg.Streaming}, instructions, registry)
}

// askAgent sends one prompt and returns the streamed answer.
func askAgent(t *testing.T, a *Agent, prompt string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var text strings.Builder
	for event := range a.Send(ctx, prompt) {
		switch e := event.(type) {
		case TextDelta:
			text.WriteString(e.Text)
		case ToolStarted:
			t.Logf("tool call: %s %s", e.Name, e.Arguments)
		case Usage:
			t.Logf("usage: %d in, %d out", e.InputTokens, e.OutputTokens)
		case Error:
			t.Fatalf("turn failed: %v", e.Err)
		}
	}
	return text.String()
}

// TestRealAPICompact checks that a summary written by the real model keeps what later questions need: facts from the
// turns it replaced are still answered, and the history after it is a valid one to send.
func TestRealAPICompact(t *testing.T) {
	a := realAgent(t, &tools.Registry{})
	facts := []string{
		"My project is called Heron and its config file is /etc/heron/heron.toml. Reply with just OK.",
		"The service listens on port 7731. Reply with just OK.",
		"Never use the word 'synergy' in anything you write for me. Reply with just OK.",
		"The database is Postgres 16 and the migration tool is dbmate. Reply with just OK.",
		"Our deadline is the 14th of March. Reply with just OK.",
	}
	// Each turn carries some filler, as real ones do: a summary of a few short lines is rightly refused as longer
	// than what it replaces.
	filler := " For context: " + strings.Repeat("this is background that does not matter for later questions; ", 12)
	for _, f := range facts {
		askAgent(t, a, f+filler)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a.settings.MaxContextTokens = 400 // keeps the newest turn or two
	c, err := a.Compact(ctx, "")
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	t.Logf("%s\n%s", c, a.Summary())

	got := askAgent(t, a, "What port does the service listen on, and what is the config file path? Answer briefly.")
	t.Logf("answer: %q", got)
	if !strings.Contains(got, "7731") || !strings.Contains(got, "heron.toml") {
		t.Errorf("the summary lost facts from the replaced turns: %q", got)
	}
}

// TestRealAPITodo measures when the real model uses the todo tool: it should keep a list for a task of several steps,
// and leave it alone for a question or a single call. Prompt wording is judged by rates over several runs, not by one
// answer. The tool is offered next to an echo tool, as it is in the program.
func TestRealAPITodo(t *testing.T) {
	const runs = 6
	scenarios := []struct {
		name   string
		prompt string
		wantAt func(used int) bool
		say    string
	}{
		{"several steps", "Do these steps in order, each one using the result of the one before: 1. echo the word a. 2. echo the result of step 1 followed by b. 3. echo the result of step 2 followed by c. 4. echo the result of step 3 followed by d. 5. tell me the final result.",
			func(used int) bool { return used >= 1 }, "must use the list at least once"},
		{"a question", "What is the capital of France? Answer with one word.",
			func(used int) bool { return used <= 1 }, "must almost never use the list"},
		{"one call", "Call the echo tool with the text hi and tell me what it returned.",
			func(used int) bool { return used <= 1 }, "must almost never use the list"},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			used, failures := 0, 0
			for i := 0; i < runs; i++ {
				echo := tools.MustNew("echo", "Returns the text it is given.", true, func(_ context.Context, a struct {
					Text string `json:"text" jsonschema:"the text to echo"`
				}) (string, error) {
					return a.Text, nil
				})
				probe := realAgent(t, &tools.Registry{})
				reg, err := probe.RegistryFor(echo)
				if err != nil {
					t.Fatal(err)
				}
				probe.SetTools(reg, probe.instructions)
				instructions, err := BuildInstructions(context.Background(), t.TempDir(), time.Now(), true)
				if err != nil {
					t.Fatal(err)
				}
				if os.Getenv("TODO_PROMPT") == "off" { // to compare the wording with no wording at all
					instructions = removeLine(instructions, "keep a checklist with the `todo` tool")
				}
				probe.SetTools(reg, instructions)

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				called, bad := false, false
				for event := range probe.Send(ctx, sc.prompt) {
					switch e := event.(type) {
					case ToolStarted:
						called = called || e.Name == "todo"
					case ToolFinished:
						if e.Name == "todo" && e.Outcome != CallOK {
							bad = true
							t.Logf("a todo call was refused: %s", e.Output)
						}
					case Error:
						t.Logf("turn failed: %v", e.Err)
					}
				}
				cancel()
				if called {
					used++
				}
				if bad {
					failures++
				}
			}
			t.Logf("%s: the todo tool was used in %d of %d runs (%d refused lists)", sc.name, used, runs, failures)
			if !sc.wantAt(used) {
				t.Errorf("%s: %s, but it was used in %d of %d runs", sc.name, sc.say, used, runs)
			}
		})
	}
}

// removeLine drops the lines of text that contain part.
func removeLine(text, part string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, part) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TestRealAPILoopNotice measures what the notice to a model that repeats itself does: a tool that always gives the same
// answer, and a prompt that asks to retry. With the notice the model should end the turn by itself more often than
// without it, where only the stop ends it. The counts are printed; it fails only if the notice makes things worse.
func TestRealAPILoopNotice(t *testing.T) {
	const runs = 6
	scenarios := []struct{ name, tool, answer, prompt string }{
		{"a status that never changes", "check_status", "status: pending",
			"Call check_status until it reports done, then tell me. Do not stop before it says done."},
		{"a file that stays locked", "read_config", "error: the file is locked by another process",
			"Read the configuration with read_config and tell me what it says. If it fails, try again."},
	}
	measure := func(t *testing.T, sc struct{ name, tool, answer, prompt string }, warnAt int) (finished, stopped, calls int) {
		for i := 0; i < runs; i++ {
			tool := tools.MustNew(sc.tool, "Takes no arguments.", true, func(context.Context, struct{}) (string, error) {
				calls++
				return sc.answer, nil
			})
			probe := realAgent(t, &tools.Registry{})
			reg, err := probe.RegistryFor(tool)
			if err != nil {
				t.Fatal(err)
			}
			instructions, err := BuildInstructions(context.Background(), t.TempDir(), time.Now(), true)
			if err != nil {
				t.Fatal(err)
			}
			probe.SetTools(reg, instructions)
			probe.loopWarnAt = warnAt

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			done, loop := false, false
			for event := range probe.Send(ctx, sc.prompt) {
				if e, ok := event.(Error); ok {
					loop = loop || strings.Contains(e.Err.Error(), "repeating")
				}
				if _, ok := event.(Done); ok {
					done = true
				}
			}
			cancel()
			switch {
			case done:
				finished++
			case loop:
				stopped++
			}
		}
		return finished, stopped, calls
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			withFinished, withStopped, withCalls := measure(t, sc, loopWarnRepeats)
			withoutFinished, withoutStopped, withoutCalls := measure(t, sc, 1000)
			t.Logf("with the notice:    %d of %d turns ended by themselves, %d were stopped, %d tool calls", withFinished, runs, withStopped, withCalls)
			t.Logf("without the notice: %d of %d turns ended by themselves, %d were stopped, %d tool calls", withoutFinished, runs, withoutStopped, withoutCalls)
			if withFinished < withoutFinished {
				t.Errorf("the notice made it worse: %d finished with it, %d without", withFinished, withoutFinished)
			}
		})
	}
}
