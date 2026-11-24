package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
		"reasoning_effort": "low",
		"streaming": false,
		"max_context_tokens": 32000,
		"max_output_tokens": 4000,
		"auto_compact": true,
		"mcpServers": {"x": {"command": "npx"}}
	}`, 0o600)

	var warn bytes.Buffer
	got, err := Load(path, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m1" || got.BaseURL != "http://localhost:11434/v1" || got.APIKey != "sk-from-env" ||
		got.ReasoningEffort != "low" ||
		got.MaxContextTokens != 32000 || got.MaxOutputTokens != 4000 || !got.AutoCompact {
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

// context_window was removed: the header counts against max_context_tokens. A config that still has it must say so.
func TestLoadRejectsTheRemovedContextWindowKey(t *testing.T) {
	path := writeConfig(t, `{"context_window": 128000}`, 0o600)

	if _, err := Load(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "context_window") {
		t.Errorf("error = %v, want one that names context_window", err)
	}
}

// max_turns was removed: a turn has no cap on its model calls. A config that still has it must say so.
func TestLoadRejectsTheRemovedMaxTurnsKey(t *testing.T) {
	path := writeConfig(t, `{"max_turns": 25}`, 0o600)

	if _, err := Load(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "max_turns") {
		t.Errorf("error = %v, want one that names max_turns", err)
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
		if _, err := Load(writeConfig(t, `{"max_context_tokens": "ten"}`, 0o600), &bytes.Buffer{}); err == nil {
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
	file := File{Model: "file-model", BaseURL: "file-url", APIKey: "file-key", ReasoningEffort: "low", Streaming: new(false)}
	tests := []struct {
		name   string
		file   File
		apiKey string
		flags  Flags
		want   Config
	}{
		{"defaults only", File{}, "", Flags{}, Config{Sessions: true, Model: DefaultModel, Streaming: true}},
		{"file wins over default", file, "", Flags{},
			Config{Sessions: true, Model: "file-model", BaseURL: "file-url", APIKey: "file-key", ReasoningEffort: "low"}},
		{"the key from the environment wins over the file", file, "env-key", Flags{},
			Config{Sessions: true, Model: "file-model", BaseURL: "file-url", APIKey: "env-key", ReasoningEffort: "low"}},
		{"flags win over the file", file, "", Flags{Model: "flag-model", BaseURL: "flag-url", ReasoningEffort: "high", Streaming: new(true)},
			Config{Sessions: true, Model: "flag-model", BaseURL: "flag-url", APIKey: "file-key", ReasoningEffort: "high", Streaming: true}},
		{"sessions can be turned off in the file", File{Sessions: new(false)}, "", Flags{},
			Config{Model: DefaultModel, Streaming: true}},
		{"default leaves the effort out", file, "", Flags{ReasoningEffort: "default"},
			Config{Sessions: true, Model: "file-model", BaseURL: "file-url", APIKey: "file-key"}},
		{"streaming defaults to true when only the effort is set", File{ReasoningEffort: "none"}, "", Flags{},
			Config{Sessions: true, Model: DefaultModel, ReasoningEffort: "none", Streaming: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.file, "config.json", tt.apiKey, tt.flags)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
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
		{"max_context_tokens", File{MaxContextTokens: -1}, Flags{}, "max_context_tokens in cfg.json must be positive"},
		{"max_output_tokens", File{MaxOutputTokens: -1}, Flags{}, "max_output_tokens in cfg.json must be positive"},
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
		if want := filepath.Join("/xdg", "chiron", "config.json"); got != want {
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
		if want := filepath.Join("/home/u", ".config", "chiron", "config.json"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestLoadMCPServers(t *testing.T) {
	path := writeConfig(t, `{"mcpServers": {
		"docs":    {"url": "https://example.com/mcp", "headers": {"X-Key": "${KEY}"}, "auto_approve": ["search"], "tools": ["search", "fetch"], "instructions": true},
		"toolbox": {"command": "npx", "args": ["-y", "pkg"], "env": {"TOKEN": "${TOKEN}"}, "enabled": false}
	}}`, 0o600)

	f, err := Load(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	docs, toolbox := f.MCPServers["docs"], f.MCPServers["toolbox"]
	if docs.URL != "https://example.com/mcp" || docs.Headers["X-Key"] != "${KEY}" || len(docs.AutoApprove.Tools) != 1 || docs.AutoApprove.All || !docs.IsEnabled() ||
		len(docs.Tools) != 2 || !docs.Instructions {
		t.Errorf("docs = %+v", docs)
	}
	if toolbox.Command != "npx" || len(toolbox.Args) != 2 || toolbox.Env["TOKEN"] != "${TOKEN}" || toolbox.IsEnabled() {
		t.Errorf("toolbox = %+v", toolbox)
	}
}

func TestLoadRejectsABadMCPServer(t *testing.T) {
	tests := []struct {
		name, servers, want string
	}{
		{"both", `{"a": {"command": "x", "url": "https://x"}}`, "either command or url"},
		{"neither", `{"a": {"enabled": true}}`, "a command or a url"},
		{"headers on a command", `{"a": {"command": "x", "headers": {"k": "v"}}}`, "headers go with url"},
		{"env on a url", `{"a": {"url": "https://x", "env": {"k": "v"}}}`, "args and env go with command"},
		{"name", `{"my server": {"command": "x"}}`, "not a usable name"},
		{"name with the separator characters", `{"a.b": {"command": "x"}}`, "not a usable name"},
		{"an unknown field", `{"a": {"command": "x", "instruction": true}}`, "instruction"},
		{"a typo", `{"a": {"comand": "x"}}`, "comand"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{"mcpServers": `+tt.servers+`}`, 0o600)
			_, err := Load(path, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestAutoApproveTakesATrueFalseOrAList(t *testing.T) {
	tests := []struct {
		name, value string
		wantAll     bool
		wantTools   []string
		covers      map[string]bool
	}{
		{"true", `true`, true, nil, map[string]bool{"anything": true}},
		{"false", `false`, false, nil, map[string]bool{"anything": false}},
		{"a list", `["a","b"]`, false, []string{"a", "b"}, map[string]bool{"a": true, "c": false}},
		{"left out", ``, false, nil, map[string]bool{"a": false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := ""
			if tt.value != "" {
				field = `, "auto_approve": ` + tt.value
			}
			f, err := Load(writeConfig(t, `{"mcpServers": {"s": {"command": "x"`+field+`}}}`, 0o600), &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			a := f.MCPServers["s"].AutoApprove
			if a.All != tt.wantAll || !reflect.DeepEqual(a.Tools, tt.wantTools) {
				t.Errorf("auto_approve = %+v", a)
			}
			for tool, want := range tt.covers {
				if a.Covers(tool) != want {
					t.Errorf("Covers(%q) = %v, want %v", tool, a.Covers(tool), want)
				}
			}
		})
	}
}

// The key was renamed from autoApprove: a config that still has the old one must say what to do, not only name it.
func TestTheOldAutoApproveKeyIsRefusedWithTheNewName(t *testing.T) {
	path := writeConfig(t, `{"mcpServers": {"s": {"command": "x", "autoApprove": true}}}`, 0o600)

	_, err := Load(path, &bytes.Buffer{})

	if err == nil || !strings.Contains(err.Error(), "autoApprove was renamed auto_approve") || !strings.Contains(err.Error(), path) {
		t.Errorf("error = %v", err)
	}
}

func TestAutoApproveRejectsAnythingElse(t *testing.T) {
	for _, value := range []string{`"yes"`, `1`, `{"a": true}`, `[1, 2]`} {
		_, err := Load(writeConfig(t, `{"mcpServers": {"s": {"command": "x", "auto_approve": `+value+`}}}`, 0o600), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "auto_approve must be true, false or a list of tool names") {
			t.Errorf("auto_approve %s: err = %v", value, err)
		}
	}
}

func TestDefaultPathAndStateDirFailTogetherWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	if p, err := DefaultPath(); err == nil || p != "" {
		t.Errorf("DefaultPath() = %q, %v: want no path and an error", p, err)
	}
	if p, err := StateDir(); err == nil || p != "" {
		t.Errorf("StateDir() = %q, %v: want no path and an error", p, err)
	}
}
