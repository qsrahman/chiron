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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/agent"
	"github.com/qsrahman/chiron/internal/config"
	"github.com/qsrahman/chiron/internal/session"
	"github.com/qsrahman/chiron/internal/tools"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "chiron") {
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
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // sessions and logs must never reach the real ones
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

// toolCallStream is a response that calls the mutating tool "write", and then a plain answer.
const toolCallStream = `event: response.completed
data: {"type":"response.completed","sequence_number":1,"response":{"id":"r","object":"response","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"write","arguments":"{\"name\":\"a.txt\"}","status":"completed"}]}}

`

// Nobody can answer an approval prompt with -p: the call is denied and the model is told, unless --yes allows it.
func TestRunPromptDeniesCallsThatNeedApprovalUnlessYes(t *testing.T) {
	type writeArgs struct {
		Name string `json:"name"`
	}
	for _, yes := range []bool{false, true} {
		t.Run(map[bool]string{false: "no flag", true: "--yes"}[yes], func(t *testing.T) {
			isolate(t)
			var mu sync.Mutex
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, string(body))
				first := len(requests) == 1
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if first {
					_, _ = io.WriteString(w, toolCallStream)
					return
				}
				_, _ = io.WriteString(w, streamedAnswer)
			}))
			t.Cleanup(srv.Close)

			ran := false
			write := tools.MustNew("write", "write a file", false, func(context.Context, writeArgs) (string, error) {
				ran = true
				return "written", nil
			})
			registry, err := tools.NewRegistry(write)
			if err != nil {
				t.Fatal(err)
			}
			a := agent.New(newClient(config.Config{BaseURL: srv.URL}), agent.Settings{Model: "m", Streaming: true, AutoApprove: yes}, "", registry)

			var out, notices bytes.Buffer
			if err := runPrompt(context.Background(), a, "go", &out, &notices); err != nil {
				t.Fatal(err)
			}

			if ran != yes {
				t.Errorf("the tool ran = %v, want %v", ran, yes)
			}
			if got := strings.Contains(notices.String(), "denied"); got == yes {
				t.Errorf("notices = %q", notices.String())
			}
			wantInSecondRequest := "the user denied this call"
			if yes {
				wantInSecondRequest = "written"
			}
			if len(requests) != 2 || !strings.Contains(requests[1], wantInSecondRequest) {
				t.Errorf("the second request should carry %q: %v", wantInSecondRequest, requests)
			}
		})
	}
}

func sessionFiles(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_STATE_HOME"), "chiron", "sessions", "*.json"))
	return files
}

// seedSession saves a finished conversation for the current directory, as the chat UI would have.
func seedSession(t *testing.T, prompt string) string {
	t.Helper()
	dir, _ := os.Getwd()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real // sessions are kept by the real path of the directory
	}
	stateDir, err := config.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	id := session.NewID(time.Now())
	_, err = session.NewStore(stateDir).Save(session.File{ID: id, Dir: dir, Model: "m", Created: time.Now(), Updated: time.Now(), Items: []session.Item{
		{Kind: "user", Raw: json.RawMessage(`{"content":` + strconv.Quote(prompt) + `,"role":"user"}`)},
		{Kind: "assistant", Raw: json.RawMessage(`{"content":"noted","role":"assistant"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAPromptRunResumesASessionButIsNotSaved(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)
	seedSession(t, "first question")

	runOK(t, "-p", "second question", "--resume", "--base-url", srv.URL)

	got, _ := json.Marshal(requests()[0]["input"])
	if !strings.Contains(string(got), "first question") || !strings.Contains(string(got), "second question") {
		t.Errorf("the continued request = %s", got)
	}
	files := sessionFiles(t)
	if len(files) != 1 {
		t.Fatalf("session files = %v", files)
	}
	if data, _ := os.ReadFile(files[0]); strings.Contains(string(data), "second question") {
		t.Error("a -p run added its turn to the session")
	}
}

func TestResumeByNameAndByPrefix(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)
	id := seedSession(t, "remember kiwi")

	runOK(t, "-p", "again", "--resume", id[:8], "--base-url", srv.URL)

	if got, _ := json.Marshal(requests()[0]["input"]); !strings.Contains(string(got), "remember kiwi") {
		t.Errorf("request = %s", got)
	}
}

func TestPromptRunsWriteNothing(t *testing.T) {
	isolate(t)
	srv, _ := recordingServer(t)
	runOK(t, "-p", "hi", "--base-url", srv.URL)
	runOK(t, "-p", "hi", "--no-save", "--base-url", srv.URL)

	cfg := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(cfg, []byte(`{"base_url": "`+srv.URL+`", "sessions": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runOK(t, "-p", "hi", "--config", cfg)

	if files := sessionFiles(t); len(files) != 0 {
		t.Errorf("files written: %v", files)
	}
}

func TestResumeProblemsAreReported(t *testing.T) {
	isolate(t)
	srv, _ := recordingServer(t)
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"nothing to resume", []string{"--resume"}, 1, "no saved session"},
		{"unknown name", []string{"--resume", "nope"}, 1, "no session"},
		{"not an id", []string{"--resume", "../x"}, 1, "not a session id"},
		{"the = form", []string{"--resume=nope"}, 1, "no session"},
		{"a stray argument", []string{"stray"}, 2, "unexpected argument"},
		{"a second argument", []string{"--resume", "a", "b"}, 2, "unexpected argument"},
		{"the old flag", []string{"--continue"}, 2, "not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := append([]string{"-p", "hi", "--base-url", srv.URL}, tt.args...)
			if code := run(context.Background(), args, &out, &errOut); code != tt.code || !strings.Contains(errOut.String(), tt.want) {
				t.Errorf("code = %d, stderr = %q", code, errOut.String())
			}
		})
	}
}

func TestEndpointKeepsOnlySchemeAndHost(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:11434/v1":            "http://localhost:11434",
		"https://user:secret@example.com/v1?k": "https://example.com",
		"":                                     "https://api.openai.com",
	} {
		if got := endpointOf(in); got != want {
			t.Errorf("endpointOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASymlinkedDirectoryFindsTheSessionsOfTheRealOne(t *testing.T) {
	isolate(t)
	srv, requests := recordingServer(t)
	seedSession(t, "seen through a link")
	here, _ := os.Getwd()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(here, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)

	runOK(t, "-p", "again", "--resume", "--base-url", srv.URL)

	if got, _ := json.Marshal(requests()[0]["input"]); !strings.Contains(string(got), "seen through a link") {
		t.Errorf("--resume from a symlink missed the session: %s", got)
	}
}

func TestBareResumeWarnsAboutADamagedFileNewerThanTheSession(t *testing.T) {
	isolate(t)
	srv, _ := recordingServer(t)
	seedSession(t, "kept")
	bad := filepath.Join(os.Getenv("XDG_STATE_HOME"), "chiron", "sessions", "zz-damaged.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{"-p", "hi", "--resume", "--base-url", srv.URL}
	if code := run(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "warning") || !strings.Contains(errOut.String(), "zz-damaged") {
		t.Errorf("stderr = %q", errOut.String())
	}
}
