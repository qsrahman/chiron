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

// openAIBaseURL is used when no base_url is configured. It is always passed to the client explicitly: the SDK
// would otherwise read OPENAI_BASE_URL itself, which would let a project's environment redirect the key and
// every prompt.
const openAIBaseURL = "https://api.openai.com/v1/"

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
	configPath := flags.String("config", "", "config file (default ~/.config/desk-agent/config.json)")

	var given config.Flags
	flags.StringVar(&given.Model, "model", "", "model name (default from the config file, or built in)")
	flags.StringVar(&given.BaseURL, "base-url", "", "API base URL for OpenAI-compatible servers")
	flags.StringVar(&given.ReasoningEffort, "reasoning-effort", "", "reasoning effort: none, minimal, low, medium, high, xhigh, max, or default to send none")
	flags.IntVar(&given.MaxTurns, "max-turns", 0, "most model calls for one prompt (default 25)")
	// Two flags make a three-state setting: not given, on, off.
	noMCP := flags.Bool("no-mcp", false, "do not start the MCP servers of the config file")
	yes := flags.Bool("yes", false, "allow every tool call without asking (in -p mode, calls that would ask are denied without it)")
	flags.BoolFunc("streaming", "stream the reply as it arrives (the default)", func(string) error { given.Streaming = new(true); return nil })
	flags.BoolFunc("no-streaming", "send one request and show the whole reply at once", func(string) error { given.Streaming = new(false); return nil })

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "desk-agent", version)
		return 0
	}

	cfg, err := loadConfig(*configPath, given, stderr)
	if err == nil {
		err = start(ctx, cfg, runOptions{prompt: *prompt, autoApprove: *yes, noMCP: *noMCP}, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "desk-agent:", err)
		return 1
	}
	return 0
}

// runOptions are the choices of one run that are not settings of the agent.
type runOptions struct {
	prompt      string // one prompt to send and print, instead of the chat UI
	autoApprove bool   // --yes
	noMCP       bool   // --no-mcp
}

// start builds the agent for the current directory and runs either one prompt or the chat UI.
func start(ctx context.Context, cfg config.Config, opts runOptions, stdout, stderr io.Writer) error {
	// Local servers often need no key, so only insist on one for the OpenAI API.
	if cfg.APIKey == "" && cfg.BaseURL == "" {
		return errors.New("no API key: set OPENAI_API_KEY or api_key in the config file")
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding working directory: %w", err)
	}
	registry := &tools.Registry{}
	instructions, err := agent.BuildInstructions(ctx, workDir, time.Now(), registry.Len() > 0)
	if err != nil {
		return err
	}
	a := agent.New(newClient(cfg), agent.Settings{
		Model:           cfg.Model,
		ReasoningEffort: cfg.ReasoningEffort,
		MaxTurns:        cfg.MaxTurns,
		Streaming:       cfg.Streaming,
		AutoApprove:     opts.autoApprove,
	}, instructions, registry)

	// The servers start in the background, and the agent gets their tools once they have answered.
	hub := startMCP(ctx, cfg, opts.noMCP)
	if hub != nil {
		defer hub.Close()
	}

	if opts.prompt != "" {
		if hub != nil {
			reportMCP(connectMCP(ctx, hub, a, workDir), stderr)
		}
		return runPrompt(ctx, a, opts.prompt, stdout, stderr)
	}
	info := tui.Info{
		Version:       version,
		WorkDir:       workDir,
		Branch:        agent.GitBranch(ctx, workDir),
		ContextWindow: cfg.ContextWindow,
	}
	if hub != nil {
		info.Startup = func() tui.Startup { return connectMCP(ctx, hub, a, workDir) }
	}
	return tui.Run(a, info)
}

// loadConfig reads the config file and applies the key from the environment and the flags.
// A missing file is fine for the default path but an error for an explicit --config.
func loadConfig(path string, given config.Flags, warn io.Writer) (config.Config, error) {
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
	return config.Resolve(file, path, os.Getenv("OPENAI_API_KEY"), given)
}

// runPrompt sends one prompt and streams the answer to w. After a retry the text of the failed attempt has
// already been written, so the answer can start over; the notice goes to stderr to say so. Nobody can answer an
// approval request here, so a call that needs one is denied (the model is told) unless --yes allowed everything.
func runPrompt(ctx context.Context, a *agent.Agent, prompt string, w, notices io.Writer) error {
	var failure error
	for event := range a.Send(ctx, prompt) {
		switch e := event.(type) {
		case agent.TextDelta:
			fmt.Fprint(w, e.Text)
		case agent.Retry:
			fmt.Fprintf(notices, "\n[%s]\n", e.Reason)
		case agent.ApprovalRequested:
			fmt.Fprintf(notices, "\n[denied: %q; run with --yes to allow tool calls]\n", e.Summary)
			e.Reply <- agent.Deny
		case agent.Error:
			failure = e.Err
		case agent.Done:
			fmt.Fprintln(w)
		}
	}
	return failure
}

// newClient builds the API client. The base URL and the key are always given explicitly, so that what the
// SDK reads from the environment (OPENAI_BASE_URL among others) cannot decide where they are sent.
//
// extra options are applied last; tests use them to replace the HTTP transport.
func newClient(cfg config.Config, extra ...option.RequestOption) openai.Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = openAIBaseURL
	}
	opts := []option.RequestOption{option.WithBaseURL(baseURL)}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return openai.NewClient(append(opts, extra...)...)
}
