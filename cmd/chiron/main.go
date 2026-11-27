package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/chiron/internal/agent"
	"github.com/qsrahman/chiron/internal/config"
	"github.com/qsrahman/chiron/internal/session"
	"github.com/qsrahman/chiron/internal/tools"
	"github.com/qsrahman/chiron/internal/tui"
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
	flags := flag.NewFlagSet("chiron", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print version and exit")
	prompt := flags.String("p", "", "send `prompt` non-interactively, print the answer and exit")
	configPath := flags.String("config", "", "config file (default ~/.config/chiron/config.json)")

	var given config.Flags
	flags.StringVar(&given.Model, "model", "", "model name (default from the config file, or built in)")
	flags.StringVar(&given.BaseURL, "base-url", "", "API base URL for OpenAI-compatible servers")
	flags.StringVar(&given.ReasoningEffort, "reasoning-effort", "", "reasoning effort: none, minimal, low, medium, high, xhigh, max, or default to send none")
	// Two flags make a three-state setting: not given, on, off.
	noMCP := flags.Bool("no-mcp", false, "do not start the MCP servers of the config file")
	noSave := flags.Bool("no-save", false, "do not save this conversation")
	var resume resumeFlag
	flags.Var(&resume, "resume", "resume the latest saved conversation of this directory, or the one named ID (--resume ID or --resume=ID)")
	yes := flags.Bool("yes", false, "allow every tool call without asking (in -p mode, calls that would ask are denied without it)")
	flags.BoolFunc("streaming", "stream the reply as it arrives (the default)", func(string) error { given.Streaming = new(true); return nil })
	flags.BoolFunc("no-streaming", "send one request and show the whole reply at once", func(string) error { given.Streaming = new(false); return nil })

	if !parseFlags(flags, args, &resume, stderr) {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "chiron", version)
		return 0
	}

	cfg, err := loadConfig(*configPath, given, stderr)
	if err == nil {
		err = start(ctx, cfg, runOptions{prompt: *prompt, autoApprove: *yes, noMCP: *noMCP, noSave: *noSave, resume: resume}, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "chiron:", err)
		return 1
	}
	return 0
}

// resumeFlag is --resume with an optional ID: bare it means the latest session of this directory. The flag package
// gives a bare flag no value and reads "--resume ID" as a flag followed by an argument, which parseFlags joins up.
type resumeFlag struct {
	set bool
	id  string // empty: the latest session
}

func (r *resumeFlag) IsBoolFlag() bool { return true }
func (r *resumeFlag) String() string   { return r.id }

func (r *resumeFlag) Set(value string) error {
	r.set = true
	if value != "true" { // what the flag package passes for a bare flag; an ID never looks like it
		r.id = value
	}
	return nil
}

// parseFlags parses args and takes the word after a bare --resume as its ID. The flag package stops at the first
// word that is no flag, so the rest is parsed again. It reports false, after saying why, when the line is wrong.
func parseFlags(flags *flag.FlagSet, args []string, resume *resumeFlag, stderr io.Writer) bool {
	for {
		if err := flags.Parse(args); err != nil {
			return false
		}
		rest := flags.Args()
		switch {
		case len(rest) == 0:
			return true
		case resume.set && resume.id == "":
			resume.id, args = rest[0], rest[1:]
		default:
			fmt.Fprintf(stderr, "chiron: unexpected argument %q\n", rest[0])
			return false
		}
	}
}

// runOptions are the choices of one run that are not settings of the agent.
type runOptions struct {
	prompt      string // one prompt to send and print, instead of the chat UI
	autoApprove bool   // --yes
	noMCP       bool   // --no-mcp
	noSave      bool   // --no-save
	resume      resumeFlag
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
		Streaming:       cfg.Streaming,
		AutoApprove:     opts.autoApprove,

		MaxOutputTokens:  cfg.MaxOutputTokens,
		MaxContextTokens: cfg.MaxContextTokens,
		AutoCompact:      cfg.AutoCompact,
	}, instructions, registry)

	if err := openSessions(a, cfg, opts, workDir, stderr); err != nil {
		return err
	}

	// The servers start in the background, and the agent gets their tools once they have answered.
	hub := startMCP(ctx, cfg, opts.noMCP)
	var mcpc *mcpControl
	if hub != nil {
		defer hub.Close()
		mcpc = &mcpControl{ctx: ctx, hub: hub, agent: a, workDir: workDir}
	}

	if opts.prompt != "" {
		if mcpc != nil {
			reportMCP(mcpc.connect(), stderr)
		}
		return runPrompt(ctx, a, opts.prompt, stdout, stderr)
	}
	info := tui.Info{
		WorkDir: workDir,
		Branch:  agent.GitBranch(ctx, workDir),
	}
	// The prompts you type are kept with the sessions, and by the same switch.
	if stateDir, err := config.StateDir(); err == nil && cfg.Sessions && !opts.noSave {
		info.HistoryPath = filepath.Join(stateDir, "history")
	}
	if mcpc != nil {
		info.Startup, info.Servers = mcpc.connect, mcpc
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
		case agent.Notice:
			fmt.Fprintf(notices, "\n[%s]\n", e.Text)
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
	baseURL := cmp.Or(cfg.BaseURL, openAIBaseURL)
	opts := []option.RequestOption{option.WithBaseURL(baseURL), option.WithMiddleware(agent.ReadableErrors)}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return openai.NewClient(append(opts, extra...)...)
}

// openSessions turns on saving, and resumes a saved conversation when asked to. Resuming works even when saving is
// off. Without a home directory there is nowhere to keep sessions: that is an error only for a resume.
func openSessions(a *agent.Agent, cfg config.Config, opts runOptions, workDir string, stderr io.Writer) error {
	stateDir, err := config.StateDir()
	if err != nil {
		if opts.resume.set {
			return err
		}
		return nil
	}

	store := session.NewStore(stateDir)
	endpoint := endpointOf(cfg.BaseURL)

	// A directory is known by where it really is, so reaching it through a symlink finds the same sessions.
	if real, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = real
	}

	// A one-shot -p run is a script's call, not a conversation: it can resume a session but is never saved.
	if cfg.Sessions && !opts.noSave && opts.prompt == "" {
		a.EnableSessions(store, workDir, endpoint)
	}

	if !opts.resume.set {
		return nil
	}

	var f session.File
	if opts.resume.id == "" {
		var damaged []string
		f, damaged, err = store.Latest(workDir)
		for _, d := range damaged { // one of them may have been the session meant
			fmt.Fprintln(stderr, "chiron: warning:", d)
		}
	} else {
		f, err = store.Load(opts.resume.id)
	}
	if err != nil {
		return err
	}
	return a.Resume(f, endpoint)
}

// endpointOf is the scheme and host of the server, which says whose reasoning items a saved session holds. The
// path and anything else in the URL is left out of the file.
func endpointOf(baseURL string) string {
	u, err := url.Parse(cmp.Or(baseURL, openAIBaseURL))
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
