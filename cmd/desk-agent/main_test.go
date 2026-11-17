package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "desk-agent") {
		t.Errorf("stdout = %q, want it to contain the program name", out.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--nope"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRunWithoutPromptExplainsStatus(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), nil, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "-p") {
		t.Errorf("stderr = %q, want a hint about -p", errOut.String())
	}
}

// isolate keeps tests away from the developer's real config and credentials.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "DESK_AGENT_MODEL"} {
		t.Setenv(k, "")
	}
}

const streamedAnswer = `event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"4","sequence_number":1}

event: response.completed
data: {"type":"response.completed","sequence_number":2,"response":{"id":"r","object":"response","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"4","annotations":[]}]}]}}

`

func TestRunPromptStreamsAnswer(t *testing.T) {
	isolate(t)
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"model":"flag-model"`) {
			gotModel = "flag-model"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamedAnswer)
	}))
	t.Cleanup(srv.Close)

	var out, errOut bytes.Buffer
	args := []string{"-p", "2+2?", "--model", "flag-model", "--base-url", srv.URL}
	if code := run(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, errOut.String())
	}
	if out.String() != "4\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "4\n")
	}
	if gotModel != "flag-model" {
		t.Error("the --model flag did not reach the request")
	}
}

func TestRunPromptUsesConfigFile(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer file-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamedAnswer)
	}))
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	content := `{"base_url": "` + srv.URL + `", "api_key": "file-key"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-p", "hi", "--config", cfgPath}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, errOut.String())
	}
	if out.String() != "4\n" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRunPromptRequiresAPIKeyForOpenAI(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-p", "hi"}, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "OPENAI_API_KEY") {
		t.Errorf("stderr = %q, want a hint about OPENAI_API_KEY", errOut.String())
	}
}

func TestExplicitMissingConfigIsAnError(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	args := []string{"-p", "hi", "--config", filepath.Join(t.TempDir(), "missing.json")}
	if code := run(context.Background(), args, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}
