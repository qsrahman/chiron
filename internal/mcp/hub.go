package mcp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qsrahman/chiron/internal/config"
	"github.com/qsrahman/chiron/internal/tools"
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

// timeout is the startup deadline.
func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return defaultTimeout
	}
	return o.Timeout
}

func (o Options) lookup() func(string) (string, bool) {
	if o.Lookup != nil {
		return o.Lookup
	}
	return os.LookupEnv
}

// Status says how one server is doing.
type Status struct {
	Name     string
	Err      error // why it did not start; nil if it did
	Dead     bool  // it started, but its connection was lost
	Tools    int   // how many tools it offers the model
	Hidden   int   // how many it has that the allow-list keeps from the model
	Tokens   int   // the estimated size of the definitions of the tools it offers
	Restarts int   // how often a call started it again
	LogPath  string
}

// Note is what a server said about its own tools, for the system prompt.
type Note struct{ Server, Text string }

// maxNoteLength is the most characters of one server's notes that are used.
const maxNoteLength = 4000

// Hub runs the configured servers.
type Hub struct {
	servers []*server // sorted by name
	done    chan struct{}
	cancel  context.CancelFunc // ends the startup of servers that are still starting
	opts    Options
	names   []string        // every configured server, enabled or not, for error messages
	off     map[string]bool // the servers that are disabled
}

// Start launches every enabled server and returns at once. All of them start in parallel and share one deadline;
// Wait says when they have all answered. A server that fails or is too slow is reported and stays out: it does not
// stop the others.
func Start(ctx context.Context, cfgs map[string]config.MCPServer, o Options) *Hub {
	timeout := o.timeout()
	h := &Hub{done: make(chan struct{}), opts: o, names: slices.Sorted(maps.Keys(cfgs)), off: map[string]bool{}}
	for _, name := range h.names {
		if cfgs[name].IsEnabled() {
			h.servers = append(h.servers, &server{name: name, cfg: cfgs[name]})
		} else {
			h.off[name] = true
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
		s.mu.Lock()
		st := Status{Name: s.name, Err: s.err, Dead: s.dead, Restarts: s.restarts, Tools: len(s.tools), Hidden: len(s.all) - len(s.tools), LogPath: s.logPath}
		s.mu.Unlock()
		for _, remote := range s.tools {
			if t, err := newTool(s, remote, apiName(s.name, remote.Name), false); err == nil {
				st.Tokens += tools.EstimateTokens(t.Spec())
			}
		}
		out = append(out, st)
	}
	return out
}

// Notes returns the usage advice of the servers that gave some and are configured with "instructions": true, each cut
// to a length the prompt can afford.
func (h *Hub) Notes() []Note {
	var notes []Note
	for _, s := range h.servers {
		text := strings.TrimSpace(s.notes)
		if s.err != nil || !s.cfg.Instructions || text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > maxNoteLength {
			text = string(runes[:maxNoteLength]) + "\n[cut]"
		}
		notes = append(notes, Note{Server: s.name, Text: text})
	}
	return notes
}

// Reconnect starts servers afresh, once the first start has finished (so that what failed is known): the named one,
// whatever its state, or with no name every server that failed or lost its connection. A server that was too slow
// the first time joins this way. How each one fared is in Status. The caller must make sure no call is running.
func (h *Hub) Reconnect(ctx context.Context, name string) error {
	h.Wait()
	var targets []*server
	if name != "" {
		i := slices.IndexFunc(h.servers, func(s *server) bool { return s.name == name })
		switch {
		case i >= 0:
			targets = h.servers[i : i+1]
		case h.off[name]:
			return fmt.Errorf("%s is disabled in the config file", name)
		default:
			return fmt.Errorf("there is no MCP server named %q (configured: %s)", name, strings.Join(h.names, ", "))
		}
	} else {
		for _, s := range h.servers {
			s.mu.Lock()
			if s.err != nil || s.dead {
				targets = append(targets, s)
			}
			s.mu.Unlock()
		}
	}

	timeout := h.opts.timeout()
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, context.DeadlineExceeded)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range targets {
		wg.Go(func() {
			s.reset()
			s.start(ctx, h.opts)
			s.err = describe(ctx, s.err, timeout.String())
		})
	}
	wg.Wait()
	return nil
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
			t, err := newTool(s, remote, name, s.cfg.AutoApprove.Covers(remote.Name))
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
