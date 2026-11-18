package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary doubles as a stdio MCP server: a test starts it as a command with one of these modes in its
// environment.
const (
	modeVar   = "DESK_AGENT_TEST_MCP_MODE"
	dieLogVar = "DESK_AGENT_TEST_DIE_LOG"
)

func TestMain(m *testing.M) {
	switch os.Getenv(modeVar) {
	case "":
		os.Exit(m.Run())
	case "serve":
		fmt.Fprintln(os.Stderr, "test server starting")
		if err := newTestServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			os.Exit(1)
		}
	case "hang": // never answers the handshake
		time.Sleep(time.Minute)
	case "exit": // dies at once
		fmt.Fprintln(os.Stderr, "test server giving up")
		os.Exit(3)
	}
}

type textArgs struct {
	Text string `json:"text" jsonschema:"what to echo"`
}

type nameArgs struct {
	Name string `json:"name"`
}

// newTestServer has the tools the tests call.
func newTestServer() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Instructions: "Use echo for greetings."})
	text := func(s string) *sdk.CallToolResult {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
	}
	sdk.AddTool(srv, &sdk.Tool{Name: "echo", Description: "Repeat the text."},
		func(_ context.Context, _ *sdk.CallToolRequest, a textArgs) (*sdk.CallToolResult, any, error) {
			return text("echo: " + a.Text), nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "env", Description: "Read an environment variable."},
		func(_ context.Context, _ *sdk.CallToolRequest, a nameArgs) (*sdk.CallToolResult, any, error) {
			return text(os.Getenv(a.Name)), nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "pid", Description: "The process id of the server."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return text(strconv.Itoa(os.Getpid())), nil, nil
		})
	// die records that it ran, if told where, and ends the process in the middle of the call.
	sdk.AddTool(srv, &sdk.Tool{Name: "die", Description: "Ends the server."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			if path := os.Getenv(dieLogVar); path != "" {
				if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
					fmt.Fprintln(f, "died")
					f.Close()
				}
			}
			os.Exit(0)
			return nil, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "fail", Description: "Always reports an error."},
		func(context.Context, *sdk.CallToolRequest, textArgs) (*sdk.CallToolResult, any, error) {
			r := text("it broke")
			r.IsError = true
			return r, nil, nil
		})
	return srv
}

// connected returns a started server (in memory) with the test tools.
func connected(t *testing.T, name string) *server {
	t.Helper()
	clientSide, serverSide := sdk.NewInMemoryTransports()
	session, err := newTestServer().Connect(context.Background(), serverSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	s := &server{name: name, source: "in memory"}
	s.attach(context.Background(), clientSide, "test")
	if s.err != nil {
		t.Fatal(s.err)
	}
	t.Cleanup(s.stop)
	return s
}

// selfCommand is the configuration that starts this test binary as a server in the given mode.
func selfCommand(mode string, env map[string]string) map[string]string {
	out := map[string]string{modeVar: mode}
	for k, v := range env {
		out[k] = v
	}
	return out
}
