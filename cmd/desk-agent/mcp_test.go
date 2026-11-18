package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary doubles as a stdio MCP server when this variable says so.
const serveVar = "DESK_AGENT_TEST_SERVE_MCP"

func TestMain(m *testing.M) {
	if os.Getenv(serveVar) == "" {
		os.Exit(m.Run())
	}
	type args struct {
		Text string `json:"text"`
	}
	srv := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "echo", Description: "Repeat the text."},
		func(_ context.Context, _ *sdk.CallToolRequest, a args) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + a.Text}}}, nil, nil
		})
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

// modelServer plays a model that calls local__echo once and then answers, and records the requests.
func modelServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	call := `event: response.completed
data: {"type":"response.completed","sequence_number":1,"response":{"id":"r","object":"response","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"local__echo","arguments":"{\"text\":\"hi\"}","status":"completed"}]}}

`
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(body))
		n := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 && strings.Contains(string(body), "local__echo") {
			_, _ = io.WriteString(w, call)
			return
		}
		_, _ = io.WriteString(w, streamedAnswer)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

// mcpConfig writes a config file for a model server and the given mcpServers JSON, and returns its path.
func mcpConfig(t *testing.T, baseURL, servers string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	content := fmt.Sprintf(`{"base_url": %q, "mcpServers": %s}`, baseURL, servers)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func localServer(extra string) string {
	return fmt.Sprintf(`{"local": {"command": %q, "env": {%q: "1"}%s}}`, os.Args[0], serveVar, extra)
}

func TestPromptWaitsForMCPServersAndUsesTheirTools(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	model, requests := modelServer(t)
	cfg := mcpConfig(t, model.URL, localServer(`, "autoApprove": ["echo"]`))

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-p", "go", "--config", cfg}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
	}

	got := requests()
	if len(got) != 2 || !strings.Contains(got[0], `"local__echo"`) {
		t.Fatalf("the first request must offer the server's tool (the servers are waited for): %v", got)
	}
	if !strings.Contains(got[1], "echo: hi") {
		t.Errorf("the second request should carry the tool's result: %s", got[1])
	}
	if out.String() != "4\n" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestACallToAnMCPToolThatIsNotTrustedIsDeniedWithoutYes(t *testing.T) {
	for _, yes := range []bool{false, true} {
		t.Run(map[bool]string{false: "no flag", true: "--yes"}[yes], func(t *testing.T) {
			isolate(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			model, requests := modelServer(t)
			args := []string{"-p", "go", "--config", mcpConfig(t, model.URL, localServer(""))}
			if yes {
				args = append(args, "--yes")
			}

			var out, errOut bytes.Buffer
			if code := run(context.Background(), args, &out, &errOut); code != 0 {
				t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
			}

			second := requests()[1]
			if yes != strings.Contains(second, "echo: hi") || yes == strings.Contains(second, "the user denied this call") {
				t.Errorf("--yes = %v: second request %s", yes, second)
			}
		})
	}
}

func TestNoMCPStartsNoServers(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	model, requests := modelServer(t)
	cfg := mcpConfig(t, model.URL, localServer(""))

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-p", "go", "--config", cfg, "--no-mcp"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
	}

	if got := requests(); len(got) != 1 || strings.Contains(got[0], "local__echo") {
		t.Errorf("--no-mcp still offered the server's tools: %v", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "desk-agent", "mcp")); len(entries) != 0 {
		t.Error("--no-mcp still started a server (it left a log)")
	}
}

func TestAServerThatFailsIsReportedAndTheRunGoesOn(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	model, _ := modelServer(t)
	servers := `{"broken": {"command": "/nonexistent/for-test", "env": {"K": "${DESK_AGENT_TEST_UNSET}"}}, "fine": {"command": "` + os.Args[0] + `", "env": {"` + serveVar + `": "1"}}}`

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-p", "go", "--config", mcpConfig(t, model.URL, servers)}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
	}

	if !strings.Contains(errOut.String(), "mcp broken: failed") || !strings.Contains(errOut.String(), "${DESK_AGENT_TEST_UNSET}") {
		t.Errorf("stderr = %q, want the failed server and the variable it needs", errOut.String())
	}
	if strings.Contains(errOut.String(), "mcp fine") {
		t.Errorf("stderr mentions a server that started: %q", errOut.String())
	}
}
