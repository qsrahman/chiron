// Package config loads desk-agent settings from the config file and the command line.
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

	// ContextWindow is the model's context size in tokens. Optional: it only lets the UI show how full the
	// context is. Models differ and unknown ones (local servers) cannot be asked, so it is never guessed.
	ContextWindow int `json:"context_window"`

	// ReasoningEffort is sent to reasoning models. Unset sends nothing, because models that do not reason
	// reject the setting.
	ReasoningEffort string `json:"reasoning_effort"`
	// MaxTurns is the most model calls for one prompt. Unset uses the agent's default.
	MaxTurns int `json:"max_turns"`
	// Streaming defaults to true; false sends one request and shows the whole reply at once, for servers
	// whose streaming is broken.
	Streaming *bool `json:"streaming"`

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
	// AutoApprove lists the server's tools (by the names the server gives them) whose calls never ask.
	AutoApprove []string `json:"autoApprove"`
	// Tools, when present, lists the only tools of the server that are offered; a name the server does not have is
	// an error, so that a typo cannot offer nothing without saying so. An empty list offers none.
	Tools []string `json:"tools"`
	// Instructions adds the server's own usage notes to the system prompt.
	Instructions bool `json:"instructions"`
}

// IsEnabled reports whether the server is to be started.
func (s MCPServer) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// serverName is what a server may be called: it ends up in the names of its tools.
var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateServers(servers map[string]MCPServer, path string) error {
	for name, s := range servers {
		switch {
		case !serverName.MatchString(name):
			return fmt.Errorf("mcpServers in %s: %q is not a usable name: use letters, digits, _ and -", path, name)
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
	MaxTurns        int
	Streaming       *bool
}

// Config is the resolved configuration used to build the API client and the agent.
type Config struct {
	Model           string
	BaseURL         string
	APIKey          string
	ContextWindow   int
	ReasoningEffort string // "" sends none
	MaxTurns        int    // 0 means the agent's default
	Streaming       bool
	MCPServers      map[string]MCPServer
}

// DefaultPath returns $XDG_CONFIG_HOME/desk-agent/config.json, or ~/.config/desk-agent/config.json.
// os.UserConfigDir is not used because it points at ~/Library/Application Support on macOS.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "desk-agent", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, ".config", "desk-agent", "config.json"), nil
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
	if file.MaxTurns < 0 {
		return Config{}, fmt.Errorf("max_turns in %s must be positive, not %d", path, file.MaxTurns)
	}
	if flags.MaxTurns < 0 {
		return Config{}, fmt.Errorf("--max-turns must be positive, not %d", flags.MaxTurns)
	}
	if file.ContextWindow < 0 {
		return Config{}, fmt.Errorf("context_window in %s must not be negative, not %d", path, file.ContextWindow)
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
		Model:           cmp.Or(flags.Model, file.Model, DefaultModel),
		BaseURL:         cmp.Or(flags.BaseURL, file.BaseURL),
		APIKey:          cmp.Or(apiKeyEnv, file.APIKey),
		ContextWindow:   file.ContextWindow,
		ReasoningEffort: effort,
		MaxTurns:        cmp.Or(flags.MaxTurns, file.MaxTurns),
		Streaming:       streaming,
		MCPServers:      file.MCPServers,
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
