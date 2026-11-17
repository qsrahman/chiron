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
	return path
}

func TestLoad(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-from-env")
	path := writeConfig(t, `{
		"model": "m1",
		"base_url": "http://localhost:11434/v1",
		"api_key": "${TEST_KEY}",
		"mcpServers": {"x": {"command": "npx"}},
		"future_field": true
	}`, 0o600)

	var warn bytes.Buffer
	got, err := Load(path, &warn)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Model: "m1", BaseURL: "http://localhost:11434/v1", APIKey: "sk-from-env"}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %s", warn.String())
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"), &bytes.Buffer{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := writeConfig(t, `{not json`, 0o600)
	if _, err := Load(path, &bytes.Buffer{}); err == nil {
		t.Fatal("expected a parse error")
	}
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
			path := writeConfig(t, tt.content, tt.perm)
			// WriteFile is subject to the umask, so set the mode explicitly.
			if err := os.Chmod(path, tt.perm); err != nil {
				t.Fatal(err)
			}
			var warn bytes.Buffer
			if _, err := Load(path, &warn); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(warn.String(), "chmod 600"); got != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output %q)", got, tt.wantWarn, warn.String())
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	file := Config{Model: "file-model", BaseURL: "file-url", APIKey: "file-key"}
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	tests := []struct {
		name      string
		file      Config
		env       map[string]string
		flagModel string
		flagURL   string
		want      Config
	}{
		{"defaults only", Config{}, nil, "", "", Config{Model: DefaultModel}},
		{"file wins over default", file, nil, "", "", file},
		{
			"env wins over file",
			file,
			map[string]string{"DESK_AGENT_MODEL": "env-model", "OPENAI_BASE_URL": "env-url", "OPENAI_API_KEY": "env-key"},
			"", "",
			Config{Model: "env-model", BaseURL: "env-url", APIKey: "env-key"},
		},
		{
			"flags win over env",
			file,
			map[string]string{"DESK_AGENT_MODEL": "env-model", "OPENAI_BASE_URL": "env-url"},
			"flag-model", "flag-url",
			Config{Model: "flag-model", BaseURL: "flag-url", APIKey: "file-key"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.file, env(tt.env), tt.flagModel, tt.flagURL)
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
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
