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

	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/tools"
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
	t.Logf("turn 1 answer: %q (history items: %d)", first, len(a.history))

	second := ask("What was the code word? Reply with just the word.")
	t.Logf("turn 2 answer: %q (history items: %d)", second, len(a.history))
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
	t.Logf("turn 1 answer: %q (tool calls: %d, history items: %d)", first, calls.Load(), len(a.history))
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
	var file config.Config
	if path, err := config.DefaultPath(); err == nil {
		if file, err = config.Load(path, os.Stderr); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	cfg := config.Resolve(file, os.Getenv, "", "")
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
	return New(openai.NewClient(opts...), cfg.Model, DefaultInstructions, registry)
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
		case Error:
			t.Fatalf("turn failed: %v", e.Err)
		}
	}
	return text.String()
}
