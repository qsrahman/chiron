package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/desk-agent/internal/config"
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

// isolate keeps tests away from the developer's real config and credentials.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS"} {
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

// Both modes need a key for the OpenAI API; without a prompt this also proves the TUI is never started.
func TestRequiresAPIKeyForOpenAI(t *testing.T) {
	for _, args := range [][]string{{"-p", "hi"}, {}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolate(t)
			var out, errOut bytes.Buffer
			if code := run(context.Background(), args, &out, &errOut); code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if !strings.Contains(errOut.String(), "OPENAI_API_KEY") {
				t.Errorf("stderr = %q, want a hint about OPENAI_API_KEY", errOut.String())
			}
		})
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

const jsonAnswer = `{"id":"r","object":"response","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"4","annotations":[]}]}]}`

// recordingServer answers every request (as a stream, or as JSON to a request that did not ask for one) and
// records the request bodies.
func recordingServer(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		if req["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamedAnswer)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonAnswer)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), requests...)
	}
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := run(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, errOut.String())
	}
	return out.String()
}

func TestSettingsFlagsReachTheRequest(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)

	runOK(t, "-p", "hi", "--base-url", srv.URL, "--reasoning-effort", "high")
	runOK(t, "-p", "hi", "--base-url", srv.URL)

	got := requests()
	reasoning, _ := got[0]["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Errorf("with the flag: reasoning = %v", got[0]["reasoning"])
	}
	if _, present := got[1]["reasoning"]; present {
		t.Errorf("without the flag no reasoning setting may be sent: %v", got[1]["reasoning"])
	}
}

func TestNoStreamingSendsOneRequestAndPrintsTheAnswer(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)

	out := runOK(t, "-p", "hi", "--base-url", srv.URL, "--no-streaming")

	if out != "4\n" {
		t.Errorf("stdout = %q", out)
	}
	if requests()[0]["stream"] == true {
		t.Error("--no-streaming must not ask for a stream")
	}
}

func TestConfigFileSettingsReachTheRequest(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	content := `{"base_url": "` + srv.URL + `", "api_key": "k", "reasoning_effort": "low", "streaming": false}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// The flag wins over the file: streaming is turned back on.
	runOK(t, "-p", "hi", "--config", cfgPath)
	runOK(t, "-p", "hi", "--config", cfgPath, "--streaming")

	got := requests()
	if reasoning, _ := got[0]["reasoning"].(map[string]any); reasoning["effort"] != "low" || got[0]["stream"] == true {
		t.Errorf("file settings not applied: %v", got[0])
	}
	if got[1]["stream"] != true {
		t.Errorf("--streaming must win over streaming: false in the file: %v", got[1])
	}
}

func TestUnknownConfigKeyStopsWithAMessageNamingIt(t *testing.T) {
	isolate(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"api_key": "k", "max_turn": 3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-p", "hi", "--config", cfgPath}, &out, &errOut)

	if code != 1 || !strings.Contains(errOut.String(), "max_turn") {
		t.Errorf("exit code = %d, stderr = %q; want 1 and a message naming max_turn", code, errOut.String())
	}
}

func TestBadSettingValuesAreReported(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{
		{"-p", "hi", "--base-url", "http://x", "--reasoning-effort", "extreme"},
		{"-p", "hi", "--base-url", "http://x", "--max-turns", "-1"},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, &out, &errOut); code != 1 || errOut.Len() == 0 {
			t.Errorf("%v: exit code = %d, stderr = %q", args, code, errOut.String())
		}
	}
}

// A project's environment must not be able to send the key and the prompts somewhere else.
func TestEnvironmentBaseURLCannotRedirectTheKey(t *testing.T) {
	t.Run("a configured base_url wins", func(t *testing.T) {
		isolate(t)
		hostile, hostileRequests := recordingServer(t)
		trusted, trustedRequests := recordingServer(t)
		t.Setenv("OPENAI_BASE_URL", hostile.URL)

		runOK(t, "-p", "hi", "--base-url", trusted.URL)

		if len(hostileRequests()) != 0 || len(trustedRequests()) != 1 {
			t.Errorf("hostile server saw %d requests, trusted %d; want 0 and 1", len(hostileRequests()), len(trustedRequests()))
		}
	})

	t.Run("without one the OpenAI address is used", func(t *testing.T) {
		isolate(t)
		hostile, hostileRequests := recordingServer(t)
		t.Setenv("OPENAI_BASE_URL", hostile.URL)
		t.Setenv("OPENAI_API_KEY", "sk-test")

		// No request leaves the test: the transport only records where it was going.
		var target *url.URL
		var authorization string
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			target, authorization = r.URL, r.Header.Get("Authorization")
			return nil, errors.New("stop here")
		})
		client := newClient(config.Config{APIKey: "sk-test"}, option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0))
		_, _ = client.Responses.New(context.Background(), responses.ResponseNewParams{Model: "m"})

		if len(hostileRequests()) != 0 {
			t.Fatalf("OPENAI_BASE_URL redirected the request to %s", hostile.URL)
		}
		if target == nil || target.Host != "api.openai.com" || !strings.HasPrefix(target.Path, "/v1/") {
			t.Errorf("the request went to %v, want https://api.openai.com/v1/...", target)
		}
		if authorization != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want the configured key", authorization)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
