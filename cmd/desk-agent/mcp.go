package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/qsrahman/desk-agent/internal/agent"
	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/mcp"
	"github.com/qsrahman/desk-agent/internal/tools"
	"github.com/qsrahman/desk-agent/internal/tui"
)

// startMCP launches the configured servers, and returns at once. It returns nil when there is nothing to start: no
// enabled server, or --no-mcp.
func startMCP(ctx context.Context, cfg config.Config, disabled bool) *mcp.Hub {
	if disabled {
		return nil
	}
	enabled := false
	for _, s := range cfg.MCPServers {
		enabled = enabled || s.IsEnabled()
	}
	if !enabled {
		return nil
	}
	var logDir string
	if dir, err := mcp.StateDir(); err == nil {
		logDir = filepath.Join(dir, "mcp")
	}
	return mcp.Start(ctx, cfg.MCPServers, mcp.Options{LogDir: logDir, Version: version})
}

// connectMCP waits for the servers, hands the agent their tools and the instructions that go with them, and says how
// each server started. The agent must be idle: nothing is sent before this returns.
func connectMCP(ctx context.Context, hub *mcp.Hub, a *agent.Agent, workDir string) tui.Startup {
	hub.Wait()
	statuses := hub.Status()
	list, notes := hub.Tools(nil) // there are no built-in tools yet to clash with

	startup := tui.Startup{Notes: notes}
	connected := 0
	for _, st := range statuses {
		startup.Servers = append(startup.Servers, [2]string{st.Name, describeStatus(st)})
		if st.Err == nil {
			connected++
		}
	}
	startup.Status = fmt.Sprintf("mcp %d/%d", connected, len(statuses))

	registry, err := tools.NewRegistry(list...)
	if err != nil {
		startup.Notes = append(startup.Notes, "MCP tools are not available: "+err.Error())
		return startup
	}
	instructions, err := agent.BuildInstructions(ctx, workDir, time.Now(), registry.Len() > 0)
	if err != nil {
		startup.Notes = append(startup.Notes, "MCP tools are not available: "+err.Error())
		return startup
	}
	a.SetTools(registry, instructions)
	return startup
}

func describeStatus(st mcp.Status) string {
	switch {
	case st.Err != nil:
		return "failed: " + st.Err.Error()
	case st.Tools == 0:
		return "connected, no tools"
	case st.Tools == 1:
		return "connected, 1 tool"
	}
	return fmt.Sprintf("connected, %d tools", st.Tools)
}

// reportMCP tells a non-interactive run which servers did not start, and what was left out.
func reportMCP(startup tui.Startup, w io.Writer) {
	for _, row := range startup.Servers {
		if strings.HasPrefix(row[1], "failed") {
			fmt.Fprintf(w, "mcp %s: %s\n", row[0], row[1])
		}
	}
	for _, note := range startup.Notes {
		fmt.Fprintln(w, "mcp:", note)
	}
}
