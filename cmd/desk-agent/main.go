package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/desk-agent/internal/agent"
	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/tui"
)

// version is overridden at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	// Ctrl-C cancels the running turn instead of killing the process mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run is separate from main so tests can call it without exiting the process.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("desk-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print version and exit")
	prompt := flags.String("p", "", "send `prompt` non-interactively, print the answer and exit")
	model := flags.String("model", "", "model name (default from config, DESK_AGENT_MODEL or built-in)")
	baseURL := flags.String("base-url", "", "API base URL for OpenAI-compatible servers")
	configPath := flags.String("config", "", "config file (default ~/.config/desk-agent/config.json)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "desk-agent", version)
		return 0
	}

	cfg, err := loadConfig(*configPath, *model, *baseURL, stderr)
	if err == nil {
		var a *agent.Agent
		if a, err = newAgent(cfg); err == nil {
			if *prompt != "" {
				err = runPrompt(ctx, a, *prompt, stdout)
			} else {
				err = tui.Run(a, cfg.Model)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "desk-agent:", err)
		return 1
	}
	return 0
}

// loadConfig reads the config file and applies environment and flag overrides.
// A missing file is fine for the default path but an error for an explicit --config.
func loadConfig(path, flagModel, flagBaseURL string, warn io.Writer) (config.Config, error) {
	explicit := path != ""
	if !explicit {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			return config.Config{}, err
		}
	}
	file, err := config.Load(path, warn)
	if err != nil && (explicit || !errors.Is(err, fs.ErrNotExist)) {
		return config.Config{}, err
	}
	return config.Resolve(file, os.Getenv, flagModel, flagBaseURL), nil
}

// newAgent builds the API client and an agent with an empty conversation.
func newAgent(cfg config.Config) (*agent.Agent, error) {
	// Local servers often need no key, so only insist on one for the OpenAI API.
	if cfg.APIKey == "" && cfg.BaseURL == "" {
		return nil, errors.New("no API key: set OPENAI_API_KEY or api_key in the config file")
	}
	return agent.New(newClient(cfg), cfg.Model, agent.DefaultInstructions), nil
}

// runPrompt sends one prompt and streams the answer to w.
func runPrompt(ctx context.Context, a *agent.Agent, prompt string, w io.Writer) error {
	var failure error
	for event := range a.Send(ctx, prompt) {
		switch e := event.(type) {
		case agent.TextDelta:
			fmt.Fprint(w, e.Text)
		case agent.Error:
			failure = e.Err
		case agent.Done:
			fmt.Fprintln(w)
		}
	}
	return failure
}

func newClient(cfg config.Config) openai.Client {
	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return openai.NewClient(opts...)
}
