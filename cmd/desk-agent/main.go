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
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/desk-agent/internal/agent"
	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/tools"
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
		err = start(ctx, cfg, *prompt, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "desk-agent:", err)
		return 1
	}
	return 0
}

// start builds the agent for the current directory and runs either one prompt or the chat UI.
func start(ctx context.Context, cfg config.Config, prompt string, stdout io.Writer) error {
	// Local servers often need no key, so only insist on one for the OpenAI API.
	if cfg.APIKey == "" && cfg.BaseURL == "" {
		return errors.New("no API key: set OPENAI_API_KEY or api_key in the config file")
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding working directory: %w", err)
	}
	instructions, err := agent.BuildInstructions(ctx, workDir, time.Now())
	if err != nil {
		return err
	}
	a := agent.New(newClient(cfg), cfg.Model, instructions, &tools.Registry{})

	if prompt != "" {
		return runPrompt(ctx, a, prompt, stdout)
	}
	return tui.Run(a, tui.Info{
		Version:       version,
		WorkDir:       workDir,
		Branch:        agent.GitBranch(ctx, workDir),
		Model:         cfg.Model,
		ContextWindow: cfg.ContextWindow,
	})
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
