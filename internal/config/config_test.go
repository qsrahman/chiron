package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil { // WriteFile is subject to the umask
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-from-env")
	path := writeConfig(t, `{
		"model": "m1",
		"base_url": "http://localhost:11434/v1",
		"api_key": "${TEST_KEY}",
		"context_window": 128000,
		"reasoning_effort": "low",
		"max_turns": 10,
		"streaming": false,
		"mcpServers": {"x": {"command": "npx"}}
	}`, 0o600)

	var warn bytes.Buffer
	got, err := Load(path, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m1" || got.BaseURL != "http://localhost:11434/v1" || got.APIKey != "sk-from-env" ||
		got.ContextWindow != 128000 || got.ReasoningEffort != "low" || got.MaxTurns != 10 {
		t.Errorf("Load() = %+v", got)
	}
	if got.Streaming == nil || *got.Streaming {
		t.Errorf("streaming = %v, want false", got.Streaming)
	}
	if len(got.MCPServers) != 1 {
		t.Errorf("mcpServers should be accepted: %v", got.MCPServers)
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %s", warn.String())
	}
}

func TestLoadRejectsAKeyThatIsNotASetting(t *testing.T) {
	path := writeConfig(t, `{"model": "m", "max_turn": 5}`, 0o600)

	_, err := Load(path, &bytes.Buffer{})

	if err == nil {
		t.Fatal("a typo must be an error, not silently ignored")
	}
	for _, want := range []string{"max_turn", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "nope.json"), &bytes.Buffer{})
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := Load(writeConfig(t, `{not json`, 0o600), &bytes.Buffer{}); err == nil {
			t.Fatal("expected a parse error")
		}
	})
	t.Run("two objects", func(t *testing.T) {
		if _, err := Load(writeConfig(t, `{} {}`, 0o600), &bytes.Buffer{}); err == nil {
			t.Fatal("trailing data must be an error")
		}
	})
	t.Run("wrong type", func(t *testing.T) {
		if _, err := Load(writeConfig(t, `{"max_turns": "ten"}`, 0o600), &bytes.Buffer{}); err == nil {
			t.Fatal("a string for a number must be an error")
		}
	})
}

func TestLoadWarnsAboutReadableLiteralKey(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		perm     os.FileMode
		wantWarn bool
	}{
		{"literal key, world readable", `{"api_key": "sk-literal"}`, 0o644, true},
		{"literal key, private", `{"api_key": "sk-literal"}`, 0o600, false},
		{"env reference, world readable", `{"api_key": "${KEY}"}`, 0o644, false},
		{"no key, world readable", `{"model": "m"}`, 0o644, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warn bytes.Buffer
			if _, err := Load(writeConfig(t, tt.content, tt.perm), &warn); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(warn.String(), "chmod 600"); got != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output %q)", got, tt.wantWarn, warn.String())
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	file := File{Model: "file-model", BaseURL: "file-url", APIKey: "file-key", ReasoningEffort: "low", MaxTurns: 7, Streaming: new(false)}
	tests := []struct {
		name   string
		file   File
		apiKey string
		flags  Flags
		want   Config
	}{
		{"defaults only", File{}, "", Flags{}, Config{Model: DefaultModel, Streaming: true}},
		{"file wins over default", file, "", Flags{},
			Config{Model: "file-model", BaseURL: "file-url", APIKey: "file-key", ReasoningEffort: "low", MaxTurns: 7}},
		{"the key from the environment wins over the file", file, "env-key", Flags{},
			Config{Model: "file-model", BaseURL: "file-url", APIKey: "env-key", ReasoningEffort: "low", MaxTurns: 7}},
		{"flags win over the file", file, "", Flags{Model: "flag-model", BaseURL: "flag-url", ReasoningEffort: "high", MaxTurns: 3, Streaming: new(true)},
			Config{Model: "flag-model", BaseURL: "flag-url", APIKey: "file-key", ReasoningEffort: "high", MaxTurns: 3, Streaming: true}},
		{"default leaves the effort out", file, "", Flags{ReasoningEffort: "default"},
			Config{Model: "file-model", BaseURL: "file-url", APIKey: "file-key", MaxTurns: 7}},
		{"streaming defaults to true when only the effort is set", File{ReasoningEffort: "none"}, "", Flags{},
			Config{Model: DefaultModel, ReasoningEffort: "none", Streaming: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.file, "config.json", tt.apiKey, tt.flags)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveRejectsBadValues(t *testing.T) {
	tests := []struct {
		name  string
		file  File
		flags Flags
		want  string
	}{
		{"file effort", File{ReasoningEffort: "extreme"}, Flags{}, `reasoning_effort in cfg.json: "extreme" is not one of none, minimal, low, medium, high, xhigh, max`},
		{"flag effort", File{}, Flags{ReasoningEffort: "extreme"}, `--reasoning-effort: "extreme" is not one of`},
		{"default is not valid in the file", File{ReasoningEffort: "default"}, Flags{}, `"default" is not one of`},
		{"file max_turns", File{MaxTurns: -1}, Flags{}, "max_turns in cfg.json must be positive"},
		{"flag max-turns", File{}, Flags{MaxTurns: -2}, "--max-turns must be positive"},
		{"context window", File{ContextWindow: -5}, Flags{}, "context_window in cfg.json must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.file, "cfg.json", "", tt.flags)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDefaultPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/xdg", "desk-agent", "config.json"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		got, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/home/u", ".config", "desk-agent", "config.json"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
