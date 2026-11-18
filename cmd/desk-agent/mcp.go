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
	if dir, err := config.StateDir(); err == nil {
		logDir = filepath.Join(dir, "mcp")
	}
	return mcp.Start(ctx, cfg.MCPServers, mcp.Options{LogDir: logDir, Version: version})
}

// mcpControl connects the MCP servers to the agent and to the commands of the UI. The agent must be idle whenever
// it acts: nothing is sent before the first connect is done, and commands run only between turns.
type mcpControl struct {
	ctx     context.Context
	hub     *mcp.Hub
	agent   *agent.Agent
	workDir string
}

// connect waits for the servers to start and gives the agent their tools.
func (c *mcpControl) connect() tui.Startup {
	c.hub.Wait()
	return c.apply()
}

// Reconnect starts servers afresh and gives the agent the tools of those that came up.
func (c *mcpControl) Reconnect(name string) tui.Startup {
	if name == "" && !c.anythingToReconnect() {
		return tui.Startup{Notes: []string{"Nothing to reconnect: every server is running"}, Status: c.status()}
	}
	if err := c.hub.Reconnect(c.ctx, name); err != nil {
		return tui.Startup{Notes: []string{err.Error()}, Status: c.status()}
	}
	return c.apply()
}

func (c *mcpControl) anythingToReconnect() bool {
	for _, st := range c.hub.Status() {
		if st.Err != nil || st.Dead {
			return true
		}
	}
	return false
}

// apply builds the registry and the instructions from the servers as they are now, hands them to the agent, and
// says how each server is doing.
func (c *mcpControl) apply() tui.Startup {
	list, notes := c.hub.Tools(nil) // there are no built-in tools yet to clash with

	startup := tui.Startup{Notes: notes, Status: c.status()}
	for _, info := range c.List() {
		startup.Servers = append(startup.Servers, [2]string{info.Name, info.Describe()})
	}

	registry, err := tools.NewRegistry(list...)
	if err != nil {
		startup.Notes = append(startup.Notes, "MCP tools are not available: "+err.Error())
		return startup
	}
	var serverNotes []agent.ServerNote
	for _, n := range c.hub.Notes() {
		serverNotes = append(serverNotes, agent.ServerNote(n))
	}
	instructions, err := agent.BuildInstructions(c.ctx, c.workDir, time.Now(), registry.Len() > 0, serverNotes...)
	if err != nil {
		startup.Notes = append(startup.Notes, "MCP tools are not available: "+err.Error())
		return startup
	}
	c.agent.SetTools(registry, instructions)
	return startup
}

// status is the few words for the header: how many servers are running.
func (c *mcpControl) status() string {
	statuses := c.hub.Status()
	running := 0
	for _, st := range statuses {
		if st.Err == nil && !st.Dead {
			running++
		}
	}
	return fmt.Sprintf("mcp %d/%d", running, len(statuses))
}

// List says how each server is doing, for /servers.
func (c *mcpControl) List() []tui.ServerInfo {
	var out []tui.ServerInfo
	for _, st := range c.hub.Status() {
		info := tui.ServerInfo{Name: st.Name, Lost: st.Dead, Tools: st.Tools, Hidden: st.Hidden, Tokens: st.Tokens, Restarts: st.Restarts, LogPath: st.LogPath}
		if st.Err != nil {
			info.Err = st.Err.Error()
		}
		out = append(out, info)
	}
	return out
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
