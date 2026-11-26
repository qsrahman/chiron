package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzLoad appends anything to a file that holds a secret: loading and resolving must not crash, and no error may
// repeat the secret.
func FuzzLoad(f *testing.F) {
	for _, s := range []string{
		"}", `"model":"m"}`, `"max_turns":1}`, `"autoApprove":true}`, `"mcpServers":{"a":{"command":"c"}}}`,
		`"mcpServers":{"a":{"url":"u","auto_approve":"x"}}}`, `"mcpServers":{"a b":null}}`, `"streaming":"yes"}`, "} {}",
		`"max_context_tokens":1e99}`, `"api_key":5}`,
	} {
		f.Add(s)
	}
	const secret = "sk-SECRET-9f3a"
	f.Fuzz(func(t *testing.T, rest string) {
		if strings.Contains(rest, "SECRET") {
			return
		}
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"api_key":"` + secret + `",` + rest
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}

		file, err := Load(path, io.Discard)
		if err == nil {
			_, err = Resolve(file, path, "", Flags{})
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Errorf("the error repeats the key: %v", err)
		}
	})
}
