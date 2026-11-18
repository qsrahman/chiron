package mcp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/qsrahman/desk-agent/internal/config"
	"github.com/qsrahman/desk-agent/internal/tools"
)

// defaultTimeout is the one deadline all servers share for starting, counted from the launch.
const defaultTimeout = 10 * time.Second

// Options are what the servers need besides their own configuration.
type Options struct {
	Timeout time.Duration               // for all servers together; 0 means 10 s
	LogDir  string                      // where stdio servers write their error output
	Version string                      // of this program, told to the servers
	Lookup  func(string) (string, bool) // reads ${NAME}; nil means the environment
}

func (o Options) lookup() func(string) (string, bool) {
	if o.Lookup != nil {
		return o.Lookup
	}
	return os.LookupEnv
}

// Status says how one server started.
type Status struct {
	Name    string
	Err     error // why it did not start; nil if it did
	Tools   int   // how many tools it offers
	Source  string
	LogPath string
}

// Hub runs the configured servers.
type Hub struct {
	servers []*server // sorted by name
	done    chan struct{}
	cancel  context.CancelFunc // ends the startup of servers that are still starting
}

// Start launches every enabled server and returns at once. All of them start in parallel and share one deadline;
// Wait says when they have all answered. A server that fails or is too slow is reported and stays out: it does not
// stop the others.
func Start(ctx context.Context, cfgs map[string]config.MCPServer, o Options) *Hub {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	h := &Hub{done: make(chan struct{})}
	for _, name := range slices.Sorted(maps.Keys(cfgs)) {
		if cfgs[name].IsEnabled() {
			h.servers = append(h.servers, &server{name: name, cfg: cfgs[name]})
		}
	}

	ctx, cancel := context.WithTimeoutCause(ctx, timeout, context.DeadlineExceeded)
	h.cancel = cancel
	var wg sync.WaitGroup
	for _, s := range h.servers {
		wg.Go(func() {
			s.start(ctx, o)
			s.err = describe(ctx, s.err, timeout.String())
		})
	}
	go func() {
		wg.Wait()
		cancel()
		close(h.done)
	}()
	return h
}

// Wait blocks until every server has started or failed.
func (h *Hub) Wait() { <-h.done }

// Status describes every server, once Wait has returned.
func (h *Hub) Status() []Status {
	var out []Status
	for _, s := range h.servers {
		out = append(out, Status{Name: s.name, Err: s.err, Tools: len(s.tools), Source: s.source, LogPath: s.logPath})
	}
	return out
}

// Tools returns the tools of the servers that started, once Wait has returned, and notices about the ones left out.
// Names are made unique in server order after every server has answered, so they never depend on which was faster.
// builtin holds the names of the tools that are already taken: a built-in tool keeps its name, and the MCP tool is
// left out.
func (h *Hub) Tools(builtin []string) ([]tools.Tool, []string) {
	used := map[string]bool{}
	for _, name := range builtin {
		used[name] = true
	}
	var list []tools.Tool
	var notices []string
	for _, s := range h.servers {
		if s.err != nil {
			continue
		}
		for _, remote := range s.tools {
			name := apiName(s.name, remote.Name)
			switch {
			case slices.Contains(builtin, name):
				notices = append(notices, fmt.Sprintf("%s: the tool %q is left out, because a built-in tool has the name %s", s.name, remote.Name, name))
				continue
			case len(builtin)+len(list) >= maxTools:
				notices = append(notices, fmt.Sprintf("%s: the tool %q is left out: a request takes at most %d tools", s.name, remote.Name, maxTools))
				continue
			}
			name = uniqueName(name, used)
			t, err := newTool(s, remote, name, slices.Contains(s.cfg.AutoApprove, remote.Name))
			if err != nil {
				notices = append(notices, fmt.Sprintf("%s: the tool %q is left out: %v", s.name, remote.Name, err))
				continue
			}
			used[name] = true
			list = append(list, t)
		}
	}
	return list, notices
}

// Close ends every session, in parallel: stopping one can take a few seconds. A server that is still starting is
// told to give up first.
func (h *Hub) Close() {
	h.cancel()
	<-h.done
	var wg sync.WaitGroup
	for _, s := range h.servers {
		wg.Go(s.stop)
	}
	wg.Wait()
}
