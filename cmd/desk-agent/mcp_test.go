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

	"github.com/qsrahman/desk-agent/internal/agent"
	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/tools"
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
	srv := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Instructions: "Greet politely."})
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

func TestServerNotesReachThePromptOnlyWhenAskedFor(t *testing.T) {
	for _, asked := range []bool{false, true} {
		t.Run(map[bool]string{false: "not asked", true: "instructions: true"}[asked], func(t *testing.T) {
			isolate(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			model, requests := modelServer(t)
			extra := ""
			if asked {
				extra = `, "instructions": true`
			}

			var out, errOut bytes.Buffer
			if code := run(context.Background(), []string{"-p", "go", "--config", mcpConfig(t, model.URL, localServer(extra))}, &out, &errOut); code != 0 {
				t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
			}

			first := requests()[0]
			if got := strings.Contains(first, "Greet politely."); got != asked {
				t.Errorf("the notes are in the prompt = %v, want %v", got, asked)
			}
			if asked && !strings.Contains(first, "written by the servers themselves") {
				t.Error("the notes are not framed as coming from the server")
			}
		})
	}
}

func TestTheAllowListHidesToolsAndATypoFailsTheServer(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	t.Run("a typo", func(t *testing.T) {
		model, requests := modelServer(t)
		var out, errOut bytes.Buffer
		cfg := mcpConfig(t, model.URL, localServer(`, "tools": ["ecoh"]`))
		if code := run(context.Background(), []string{"-p", "go", "--config", cfg}, &out, &errOut); code != 0 {
			t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), `"ecoh"`) || strings.Contains(requests()[0], "local__echo") {
			t.Errorf("stderr %q; first request %s", errOut.String(), requests()[0])
		}
	})
	t.Run("a name that exists", func(t *testing.T) {
		model, requests := modelServer(t)
		var out, errOut bytes.Buffer
		cfg := mcpConfig(t, model.URL, localServer(`, "tools": ["echo"], "autoApprove": ["echo"]`))
		if code := run(context.Background(), []string{"-p", "go", "--config", cfg}, &out, &errOut); code != 0 {
			t.Fatalf("exit code %d, stderr: %s", code, errOut.String())
		}
		if !strings.Contains(requests()[0], "local__echo") {
			t.Error("the allowed tool was not offered")
		}
	})
}

// A server that could not start joins when /reconnect is run after the cause is fixed, and the agent gets its tools.
func TestReconnectGivesTheAgentTheToolsOfAServerThatJoined(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const needed = "DESK_AGENT_TEST_SWITCH"
	cfg := config.Config{
		BaseURL: "http://127.0.0.1:1",
		MCPServers: map[string]config.MCPServer{
			"late": {Command: os.Args[0], Env: map[string]string{serveVar: "${" + needed + "}"}},
		},
	}
	a := agent.New(newClient(cfg), agent.Settings{Model: "m"}, "", &tools.Registry{})
	hub := startMCP(context.Background(), cfg, false)
	t.Cleanup(hub.Close)
	c := &mcpControl{ctx: context.Background(), hub: hub, agent: a, workDir: t.TempDir()}

	first := c.connect()
	if first.Status != "mcp 0/1" || !strings.Contains(first.Servers[0][1], "failed") || a.ToolList().Ask != nil {
		t.Fatalf("first = %+v, tools %+v: the server should have failed", first, a.ToolList())
	}

	t.Setenv(needed, "1") // the cause is fixed
	second := c.Reconnect("")

	if second.Status != "mcp 1/1" || second.Servers[0][1] != "connected, 1 tool" {
		t.Errorf("second = %+v", second)
	}
	if list := a.ToolList(); len(list.Ask) != 1 || list.Ask[0] != "late__echo" {
		t.Errorf("the agent's tools after /reconnect: %+v", list)
	}

	if third := c.Reconnect(""); len(third.Notes) != 1 || !strings.Contains(third.Notes[0], "Nothing to reconnect") {
		t.Errorf("with every server running: %+v", third)
	}
	if bad := c.Reconnect("nope"); len(bad.Notes) != 1 || !strings.Contains(bad.Notes[0], `"nope"`) {
		t.Errorf("an unknown name: %+v", bad)
	}
	if infos := c.List(); len(infos) != 1 || infos[0].Name != "late" || infos[0].Tools != 1 || infos[0].Err != "" || infos[0].LogPath == "" {
		t.Errorf("List = %+v", infos)
	}
}
