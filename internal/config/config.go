// Package config loads chiron settings from the config file and the command line.
//
// The environment has exactly one variable, OPENAI_API_KEY. A variable for the base URL would let a project's
// .envrc send the key and every prompt wherever it says, so none exists (and none for the model, which other
// programs also export under common names). The file is the user's own: no config is read from the working
// directory, and only --config names another one.
package config

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DefaultModel is used when neither the flags nor the config file name a model.
const DefaultModel = "gpt-5.4-mini"

// Efforts are the reasoning efforts the API takes.
var Efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// File is what config.json holds. Unknown keys are an error, so a typo is never silently ignored.
type File struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`

	// ReasoningEffort is sent to reasoning models. Unset sends nothing, because models that do not reason
	// reject the setting.
	ReasoningEffort string `json:"reasoning_effort"`
	// Streaming defaults to true; false sends one request and shows the whole reply at once, for servers
	// whose streaming is broken.
	Streaming *bool `json:"streaming"`
	// MaxOutputTokens caps what the model may generate for one reply, its reasoning included; a reply that hits the cap
	// is cut off and shown as incomplete. Unset sends no cap, so the model or server decides.
	MaxOutputTokens int `json:"max_output_tokens"`
	// MaxContextTokens is the budget for a whole request: instructions, tool definitions and history. Unset uses
	// 65,536. Keep it well below the model's real window, because the count is an estimate.
	MaxContextTokens int `json:"max_context_tokens"`
	// AutoCompact summarises the oldest turns before a turn that would pass 75% of the budget. Off by default:
	// a summary is an extra model call that rewrites the history; /compact does it on request.
	AutoCompact bool `json:"auto_compact"`
	// Sessions defaults to true: every conversation is saved after each turn, so it can be resumed. false turns
	// that off.
	Sessions *bool `json:"sessions"`

	// MCPServers are the MCP servers to start, by name.
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer is one entry of mcpServers: a program to run (Command, Args, Env) or a remote server (URL, Headers).
// ${NAME} in its strings is expanded when the server is started, not here, so that a variable that is not set
// fails that server only.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`

	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`

	// Enabled defaults to true; a disabled server is not started.
	Enabled *bool `json:"enabled"`
	// AutoApprove says which of the server's tools never ask before a call.
	AutoApprove AutoApprove `json:"autoApprove"`
	// Tools, when present, lists the only tools of the server that are offered; a name the server does not have is
	// an error, so that a typo cannot offer nothing without saying so. An empty list offers none.
	Tools []string `json:"tools"`
	// Instructions adds the server's own usage notes to the system prompt.
	Instructions bool `json:"instructions"`
}

// AutoApprove is written as a list of the tools (by the names the server gives them) whose calls never ask, or as
// true for every tool of the server, those it adds later included, or as false for none.
type AutoApprove struct {
	All   bool
	Tools []string
}

func (a *AutoApprove) UnmarshalJSON(data []byte) error {
	var all bool
	if err := json.Unmarshal(data, &all); err == nil {
		a.All = all
		return nil
	}
	var tools []string
	if err := json.Unmarshal(data, &tools); err != nil {
		return errors.New("autoApprove must be true, false or a list of tool names")
	}
	a.Tools = tools
	return nil
}

// Covers reports whether calls to the tool never ask.
func (a AutoApprove) Covers(tool string) bool { return a.All || slices.Contains(a.Tools, tool) }

// IsEnabled reports whether the server is to be started.
func (s MCPServer) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// serverName is what a server may be called: it ends up in the names of its tools.
var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateServers(servers map[string]MCPServer, path string) error {
	for name, s := range servers {
		if !serverName.MatchString(name) {
			return fmt.Errorf("mcpServers in %s: %q is not a usable name: use letters, digits, _ and -", path, name)
		}
		switch {
		case s.Command != "" && s.URL != "":
			return fmt.Errorf("mcpServers.%s in %s: give either command or url, not both", name, path)
		case s.Command == "" && s.URL == "":
			return fmt.Errorf("mcpServers.%s in %s: give a command or a url", name, path)
		case s.Command != "" && len(s.Headers) > 0, s.URL != "" && (len(s.Args) > 0 || len(s.Env) > 0):
			return fmt.Errorf("mcpServers.%s in %s: args and env go with command, headers go with url", name, path)
		}
	}
	return nil
}

// Flags are the settings given on the command line. The zero value of each means "not given".
type Flags struct {
	Model           string
	BaseURL         string
	ReasoningEffort string // "default" leaves the setting out even if the file sets one
	Streaming       *bool
}

// Config is the resolved configuration used to build the API client and the agent.
type Config struct {
	Model           string
	BaseURL         string
	APIKey          string
	ReasoningEffort string // "" sends none
	Streaming       bool

	MaxOutputTokens  int // 0 sends no cap
	MaxContextTokens int // 0 means the agent's default
	AutoCompact      bool
	Sessions         bool

	MCPServers map[string]MCPServer
}

// DefaultPath returns $XDG_CONFIG_HOME/chiron/config.json, or ~/.config/chiron/config.json.
// os.UserConfigDir is not used because it points at ~/Library/Application Support on macOS.
func DefaultPath() (string, error) {
	dir, err := xdgPath("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// StateDir is where sessions, the prompt history and server logs are kept: $XDG_STATE_HOME/chiron, or
// ~/.local/state/chiron.
func StateDir() (string, error) {
	return xdgPath("XDG_STATE_HOME", ".local/state")
}

func xdgPath(env, dir string) (string, error) {
	if path := os.Getenv(env); path != "" {
		return filepath.Join(path, "chiron"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, dir, "chiron"), nil
}

// Load reads the config file at path. A missing file returns an error that satisfies
// errors.Is(err, fs.ErrNotExist) so callers can treat the default path as optional. A key that is not a
// setting is an error that names the key and the file. "${NAME}" in model, base_url and api_key is replaced
// from the environment. A warning is written to warn when a literal api_key sits in a file other users can read.
func Load(path string, warn io.Writer) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("reading config: %w", err)
	}

	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("parsing %s: more than one JSON value", path)
	}

	if f.APIKey != "" && !strings.Contains(f.APIKey, "$") {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(warn, "warning: %s contains an api_key but is readable by other users; run: chmod 600 %s\n", path, path)
		}
	}

	if err := validateServers(f.MCPServers, path); err != nil {
		return File{}, err
	}

	f.Model = os.ExpandEnv(f.Model)
	f.BaseURL = os.ExpandEnv(f.BaseURL)
	f.APIKey = os.ExpandEnv(f.APIKey)
	return f, nil
}

// Resolve applies the precedence flags > file > default. The API key comes from apiKeyEnv (the value of
// OPENAI_API_KEY) or the file; there is no flag for it on purpose, since it would end up in shell history and
// process listings. path names the file in error messages.
func Resolve(file File, path string, apiKeyEnv string, flags Flags) (Config, error) {
	if err := validateEffort(file.ReasoningEffort, "reasoning_effort in "+path, false); err != nil {
		return Config{}, err
	}
	if err := validateEffort(flags.ReasoningEffort, "--reasoning-effort", true); err != nil {
		return Config{}, err
	}
	if file.MaxOutputTokens < 0 {
		return Config{}, fmt.Errorf("max_output_tokens in %s must be positive, not %d", path, file.MaxOutputTokens)
	}
	if file.MaxContextTokens < 0 {
		return Config{}, fmt.Errorf("max_context_tokens in %s must be positive, not %d", path, file.MaxContextTokens)
	}

	effort := cmp.Or(flags.ReasoningEffort, file.ReasoningEffort)
	if effort == "default" {
		effort = ""
	}

	streaming := true
	if file.Streaming != nil {
		streaming = *file.Streaming
	}
	if flags.Streaming != nil {
		streaming = *flags.Streaming
	}

	return Config{
		Model:            cmp.Or(flags.Model, file.Model, DefaultModel),
		BaseURL:          cmp.Or(flags.BaseURL, file.BaseURL),
		APIKey:           cmp.Or(apiKeyEnv, file.APIKey),
		ReasoningEffort:  effort,
		Streaming:        streaming,
		MCPServers:       file.MCPServers,
		MaxOutputTokens:  file.MaxOutputTokens,
		MaxContextTokens: file.MaxContextTokens,
		AutoCompact:      file.AutoCompact,
		Sessions:         file.Sessions == nil || *file.Sessions,
	}, nil
}

// validateEffort accepts an empty value, one of Efforts, and "default" where allowed.
func validateEffort(value, where string, allowDefault bool) error {
	if value == "" || slices.Contains(Efforts, value) || (allowDefault && value == "default") {
		return nil
	}
	valid := slices.Clone(Efforts)
	if allowDefault {
		valid = append(valid, "default")
	}
	return fmt.Errorf("%s: %q is not one of %s", where, value, strings.Join(valid, ", "))
}
