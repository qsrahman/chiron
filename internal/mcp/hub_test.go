package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/qsrahman/desk-agent/internal/config"
)

// stdio configures a server that is this test binary in the given mode.
func stdio(mode string, extra map[string]string) config.MCPServer {
	return config.MCPServer{Command: os.Args[0], Env: selfCommand(mode, extra)}
}

func startHub(t *testing.T, cfgs map[string]config.MCPServer, o Options) *Hub {
	t.Helper()
	if o.LogDir == "" {
		o.LogDir = filepath.Join(t.TempDir(), "logs")
	}
	h := Start(context.Background(), cfgs, o)
	t.Cleanup(h.Close)
	h.Wait()
	return h
}

func statusOf(h *Hub, name string) Status {
	for _, s := range h.Status() {
		if s.Name == name {
			return s
		}
	}
	return Status{Name: name, Err: errAny}
}

func callTool(t *testing.T, h *Hub, name string, arguments string) (string, error) {
	t.Helper()
	list, _ := h.Tools(nil)
	for _, tl := range list {
		if tl.Name() == name {
			return tl.Run(context.Background(), arguments)
		}
	}
	t.Fatalf("no tool %q in %v", name, list)
	return "", nil
}

func TestAStdioServerStartsInheritsTheEnvironmentAndLogsPrivately(t *testing.T) {
	t.Setenv("DESK_AGENT_PROBE", "from-the-parent")
	h := startHub(t, map[string]config.MCPServer{"local": stdio("serve", map[string]string{"FROM_CONFIG": "from-the-config"})}, Options{})

	st := statusOf(h, "local")
	if st.Err != nil || st.Tools != 3 {
		t.Fatalf("status = %+v", st)
	}
	if out, err := callTool(t, h, "local__echo", `{"text":"hi"}`); err != nil || out != "echo: hi" {
		t.Errorf("echo = %q, %v", out, err)
	}
	for variable, want := range map[string]string{"DESK_AGENT_PROBE": "from-the-parent", "FROM_CONFIG": "from-the-config"} {
		if out, err := callTool(t, h, "local__env", `{"name":"`+variable+`"}`); err != nil || out != want {
			t.Errorf("the server sees %s = %q (%v), want %q", variable, out, err, want)
		}
	}

	info, err := os.Stat(st.LogPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log %q: %v, mode %v", st.LogPath, err, info)
	}
	if dir, _ := os.Stat(filepath.Dir(st.LogPath)); dir.Mode().Perm() != 0o700 {
		t.Errorf("the log directory is %v, want 0700", dir.Mode().Perm())
	}
	data, _ := os.ReadFile(st.LogPath)
	if !strings.Contains(string(data), "test server starting") {
		t.Errorf("the server's error output is not in its log: %q", data)
	}
}

func TestEveryFailureIsReportedAndNoneStopsTheOthers(t *testing.T) {
	const secret = "s3cr3t-value"
	h := Start(context.Background(), map[string]config.MCPServer{
		"good":    stdio("serve", nil),
		"exits":   stdio("exit", map[string]string{"TOKEN": secret}),
		"missing": {Command: "/nonexistent/program-for-test"},
		"unset":   {Command: os.Args[0], Args: []string{"${DESK_AGENT_NOT_SET}"}},
		"slow":    stdio("hang", nil),
		"off":     {Command: os.Args[0], Enabled: new(false)},
	}, Options{Timeout: 1500 * time.Millisecond, LogDir: filepath.Join(t.TempDir(), "logs")})
	t.Cleanup(h.Close)
	started := time.Now()
	h.Wait()

	if took := time.Since(started); took > 6*time.Second {
		t.Errorf("startup took %v: one slow server must not hold the others for long", took)
	}
	if st := statusOf(h, "good"); st.Err != nil || st.Tools != 3 {
		t.Errorf("good = %+v", st)
	}
	for _, name := range []string{"exits", "missing"} {
		if statusOf(h, name).Err == nil {
			t.Errorf("%s should have failed", name)
		}
	}
	if err := statusOf(h, "unset").Err; err == nil || !strings.Contains(err.Error(), "${DESK_AGENT_NOT_SET}") || !strings.Contains(err.Error(), "args[0]") {
		t.Errorf("unset: %v, want the variable and where it is used", err)
	}
	if err := statusOf(h, "slow").Err; err == nil || !strings.Contains(err.Error(), "did not start within 1.5s") {
		t.Errorf("slow: %v", err)
	}
	if len(h.Status()) != 5 {
		t.Errorf("%d servers reported, want 5 (a disabled one is not started)", len(h.Status()))
	}

	for _, st := range h.Status() {
		if st.Err != nil && strings.Contains(st.Err.Error(), secret) {
			t.Errorf("%s: the error repeats a value from its environment", st.Name)
		}
		if st.LogPath != "" {
			if data, _ := os.ReadFile(st.LogPath); strings.Contains(string(data), secret) {
				t.Errorf("%s: the log holds a value from its environment", st.Name)
			}
		}
	}
	if data, _ := os.ReadFile(statusOf(h, "exits").LogPath); !strings.Contains(string(data), "giving up") {
		t.Errorf("a server that died keeps its last words in its log: %q", data)
	}
}

func TestAServerStaysUsableAfterStartup(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{"local": stdio("serve", nil)}, Options{Timeout: 5 * time.Second})

	time.Sleep(50 * time.Millisecond) // the startup context ended when the last server answered

	if out, err := callTool(t, h, "local__echo", `{"text":"later"}`); err != nil || out != "echo: later" {
		t.Errorf("a call after startup = %q, %v", out, err)
	}
}

func TestCloseStopsServersThatAreStillStarting(t *testing.T) {
	h := Start(context.Background(), map[string]config.MCPServer{"slow": stdio("hang", nil)}, Options{Timeout: time.Minute, LogDir: filepath.Join(t.TempDir(), "logs")})

	done := make(chan struct{})
	go func() { h.Close(); close(done) }()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Close waited for a server that never answers")
	}
}

// recording wraps a handler and remembers the headers of every request.
type recording struct {
	next http.Handler
	mu   sync.Mutex
	seen []http.Header
}

func (r *recording) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.seen = append(r.seen, req.Header.Clone())
	r.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

func (r *recording) headers() []http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]http.Header(nil), r.seen...)
}

func TestARemoteServerGetsTheConfiguredHeaders(t *testing.T) {
	handler := &recording{next: sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return newTestServer() }, nil)}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	lookup := lookupIn(map[string]string{"API_KEY": "key-123"})

	h := startHub(t, map[string]config.MCPServer{"web": {URL: ts.URL, Headers: map[string]string{"X-Api-Key": "${API_KEY}"}}}, Options{Lookup: lookup})

	if st := statusOf(h, "web"); st.Err != nil || st.Tools != 3 || st.Source != strings.TrimPrefix(ts.URL, "http://") {
		t.Fatalf("status = %+v", st)
	}
	if out, err := callTool(t, h, "web__echo", `{"text":"remote"}`); err != nil || out != "echo: remote" {
		t.Errorf("echo = %q, %v", out, err)
	}
	seen := handler.headers()
	if len(seen) == 0 {
		t.Fatal("the server saw no request")
	}
	for i, header := range seen {
		if header.Get("X-Api-Key") != "key-123" {
			t.Errorf("request %d had X-Api-Key %q", i, header.Get("X-Api-Key"))
		}
	}
}

func TestARedirectToAnotherOriginIsRefusedAndCarriesNoHeaders(t *testing.T) {
	other := &recording{next: http.NotFoundHandler()}
	elsewhere := httptest.NewServer(other)
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)
	const secret = "key-must-not-travel"

	h := startHub(t, map[string]config.MCPServer{"web": {URL: redirecting.URL, Headers: map[string]string{"X-Api-Key": secret}}}, Options{Timeout: 5 * time.Second})

	err := statusOf(h, "web").Err
	if err == nil {
		t.Fatal("a redirect to another origin should fail the server")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error repeats the header value: %v", err)
	}
	for _, header := range other.headers() {
		if header.Get("X-Api-Key") != "" {
			t.Error("the header followed the redirect to another origin")
		}
	}
	if len(other.headers()) != 0 {
		t.Errorf("the other origin received %d requests; the redirect should be refused", len(other.headers()))
	}
}

func TestAServerWithABadURLFailsWithoutRepeatingIt(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{
		"plain": {URL: "http://example.com/mcp?token=abc123"},
		"creds": {URL: "https://user:pw-9@example.com/mcp"},
	}, Options{})

	for _, name := range []string{"plain", "creds"} {
		err := statusOf(h, name).Err
		if err == nil {
			t.Errorf("%s should fail", name)
			continue
		}
		for _, leaked := range []string{"abc123", "pw-9", "example.com"} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("%s: the error repeats %q: %v", name, leaked, err)
			}
		}
	}
}

func TestCheckURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com/mcp", false},
		{"http://localhost:3000/mcp", false},
		{"http://127.0.0.1:8080", false},
		{"http://[::1]:9", false},
		{"http://app.localhost/mcp", false},
		{"http://example.com/mcp", true},
		{"http://192.168.1.10/mcp", true},
		{"ftp://example.com", true},
		{"https://user:pw@example.com", true},
		{"https://", true},
		{"not a url", true},
	}
	for _, tt := range tests {
		if _, err := checkURL(tt.url); (err != nil) != tt.wantErr {
			t.Errorf("checkURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}

// The guard adds the headers to requests for the configured host and to no other, whatever sent the request.
func TestOriginGuardAddsHeadersOnlyForItsHost(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		got[req.URL.Host] = req.Header.Get("X-Api-Key")
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
	})
	guard := originGuard{base: base, host: "mcp.example.com", headers: map[string]string{"X-Api-Key": "k"}}

	for _, host := range []string{"mcp.example.com", "other.example.org"} {
		req, _ := http.NewRequest(http.MethodPost, "https://"+host+"/x", nil)
		if _, err := guard.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("X-Api-Key") != "" {
			t.Error("the guard changed the caller's request instead of a copy")
		}
	}

	if got["mcp.example.com"] != "k" || got["other.example.org"] != "" {
		t.Errorf("headers seen per host: %v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
