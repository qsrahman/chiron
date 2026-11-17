// Package config loads desk-agent settings from the config file, the environment and flags.
package config

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultModel is used when neither flags, environment nor the config file name a model.
const DefaultModel = "gpt-5.4-mini"

// Config is the resolved configuration used to build the API client and the agent.
type Config struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`

	// ContextWindow is the model's context size in tokens. Optional: it only lets the UI show how full the
	// context is. Models differ and unknown ones (local servers) cannot be asked, so it is never guessed.
	ContextWindow int `json:"context_window"`
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
// errors.Is(err, fs.ErrNotExist) so callers can treat the default path as optional.
// Unknown keys are ignored, and "${NAME}" in string values is replaced from the environment.
// A warning is written to warn when a literal api_key sits in a file other users can read.
func Load(path string, warn io.Writer) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	if c.APIKey != "" && !strings.Contains(c.APIKey, "$") {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(warn, "warning: %s contains an api_key but is readable by other users; run: chmod 600 %s\n", path, path)
		}
	}

	c.Model = os.ExpandEnv(c.Model)
	c.BaseURL = os.ExpandEnv(c.BaseURL)
	c.APIKey = os.ExpandEnv(c.APIKey)
	return c, nil
}

// Resolve applies the precedence flags > environment > file > default.
// There is no api key flag on purpose: it would end up in shell history and process listings.
func Resolve(file Config, getenv func(string) string, flagModel, flagBaseURL string) Config {
	return Config{
		Model:   cmp.Or(flagModel, getenv("DESK_AGENT_MODEL"), file.Model, DefaultModel),
		BaseURL: cmp.Or(flagBaseURL, getenv("OPENAI_BASE_URL"), file.BaseURL),
		APIKey:  cmp.Or(getenv("OPENAI_API_KEY"), file.APIKey),

		ContextWindow: file.ContextWindow,
	}
}
