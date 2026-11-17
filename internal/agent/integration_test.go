//go:build integration

package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/desk-agent/internal/config"
)

// TestRealAPIReplay talks to the real API (costs a few tokens). It checks what the fake server
// cannot: that the model name is valid, that include=reasoning.encrypted_content is accepted, and
// that a second turn replaying the first turn's output items works with store=false.
//
//	OPENAI_API_KEY=... go test -tags=integration -run TestRealAPIReplay -v ./internal/agent
//
// Settings come from the default config file and the environment, exactly like the CLI.
func TestRealAPIReplay(t *testing.T) {
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
	a := New(openai.NewClient(opts...), cfg.Model, DefaultInstructions)
	t.Logf("model=%s base_url=%q", cfg.Model, cfg.BaseURL)

	ask := func(prompt string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var text strings.Builder
		for event := range a.Send(ctx, prompt) {
			switch e := event.(type) {
			case TextDelta:
				text.WriteString(e.Text)
			case Error:
				t.Fatalf("turn failed: %v", e.Err)
			}
		}
		return text.String()
	}

	first := ask("Remember this code word: PINEAPPLE-42. Reply with just OK.")
	t.Logf("turn 1 answer: %q (history items: %d)", first, len(a.history))

	second := ask("What was the code word? Reply with just the word.")
	t.Logf("turn 2 answer: %q (history items: %d)", second, len(a.history))
	if !strings.Contains(second, "PINEAPPLE-42") {
		t.Errorf("second turn did not recall the code word, so history replay may be broken: %q", second)
	}
}
