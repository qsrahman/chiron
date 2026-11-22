package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/qsrahman/chiron/internal/config"
	"github.com/qsrahman/chiron/internal/tools"
)

// maxRestarts is how often a server that died is started again by a call, per session. /reconnect starts afresh.
const maxRestarts = 3

// keepAlive is how often an idle connection is checked, so that a server that vanished is noticed.
const keepAlive = 30 * time.Second

// server is one configured MCP server and, once it has started, its session and its tools.
type server struct {
	name string
	cfg  config.MCPServer
	opts Options

	source   string // how it is run, for the approval prompt: the command line as configured, or the host
	logPath  string // where a stdio server's error output goes
	closeLog func()

	mu       sync.Mutex // guards session, dead and restarts, which calls, the watcher and /servers all touch
	session  *sdk.ClientSession
	dead     bool // the connection was lost: the next call starts the server again
	restarts int

	all       []*sdk.Tool // everything the server offers, sorted by name
	tools     []*sdk.Tool // what is offered to the model: all of them, or those the allow-list names
	defTokens int         // the estimated size of the definitions of those tools, counted once when the server started
	notes     string      // the usage advice the server gave when it started
	err       error       // why it did not start
}

// start launches the server, connects and lists its tools. The outcome is left in the fields; a server that did not
// start has released everything it had.
func (s *server) start(ctx context.Context, o Options) {
	s.opts = o
	transport, err := s.transport(false)
	if err != nil {
		s.err = err
		return
	}
	s.attach(ctx, transport, o.Version)
	if s.err != nil {
		s.stop()
	}
}

// transport resolves the configuration and prepares the way to reach the server.
func (s *server) transport(appendLog bool) (sdk.Transport, error) {
	l, err := resolve(s.cfg, s.opts.lookup())
	if err != nil {
		return nil, err
	}
	if s.cfg.URL != "" {
		var transport sdk.Transport
		transport, s.source, err = remoteTransport(l)
		return transport, err
	}
	s.source = strings.TrimSpace(s.cfg.Command + " " + strings.Join(s.cfg.Args, " "))
	var transport sdk.Transport
	transport, s.logPath, s.closeLog, err = stdioTransport(l, s.opts.LogDir, s.name, appendLog)
	return transport, err
}

// connect opens a session over the transport and has it watched.
func (s *server) connect(ctx context.Context, transport sdk.Transport, version string) (*sdk.ClientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "chiron", Version: version}, &sdk.ClientOptions{KeepAlive: keepAlive})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	go func() {
		_ = session.Wait()
		s.markDead(session)
	}()
	return session, nil
}

// attach connects over the transport, lists the tools and applies the allow-list.
func (s *server) attach(ctx context.Context, transport sdk.Transport, version string) {
	session, err := s.connect(ctx, transport, version)
	if err != nil {
		s.err = err
		return
	}
	s.session = session
	s.notes = session.InitializeResult().Instructions
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			s.err = fmt.Errorf("listing tools: %w", err)
			return
		}
		s.all = append(s.all, tool)
	}
	slices.SortFunc(s.all, func(a, b *sdk.Tool) int { return strings.Compare(a.Name, b.Name) })
	s.err = s.filter()
	s.defTokens = s.definitionTokens()
}

// definitionTokens estimates what the definitions of the tools offered cost in every request.
func (s *server) definitionTokens() int {
	n := 0
	for _, remote := range s.tools {
		if t, err := newTool(s, remote, apiName(s.name, remote.Name), false); err == nil {
			n += tools.EstimateTokens(t.Spec())
		}
	}
	return n
}

// filter picks the tools the allow-list names. A name the server does not have is an error: a typo would otherwise
// offer nothing, and nobody would know why.
func (s *server) filter() error {
	s.tools = s.all
	if s.cfg.Tools == nil {
		return nil
	}
	var picked []*sdk.Tool
	for _, name := range s.cfg.Tools {
		i := slices.IndexFunc(s.all, func(t *sdk.Tool) bool { return t.Name == name })
		if i < 0 {
			offered := make([]string, len(s.all))
			for j, t := range s.all {
				offered[j] = t.Name
			}
			return fmt.Errorf("tools: the server has no tool %q (it offers %s)", name, strings.Join(offered, ", "))
		}
		if !slices.Contains(picked, s.all[i]) {
			picked = append(picked, s.all[i])
		}
	}
	slices.SortFunc(picked, func(a, b *sdk.Tool) int { return strings.Compare(a.Name, b.Name) })
	s.tools = picked
	return nil
}

// markDead notes that a session ended, unless it has been replaced since.
func (s *server) markDead(session *sdk.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == session {
		s.dead = true
	}
}

// live returns the session to call, starting the server again first if its connection was lost. A call that was
// already sent when the connection broke is not repeated, since it may have done part of its work; this is the next
// one. Restarting does not change the tools offered: if the server no longer has one, that call fails with the
// server's own error.
func (s *server) live(ctx context.Context) (*sdk.ClientSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dead {
		return s.session, nil
	}
	if s.restarts >= maxRestarts {
		return nil, fmt.Errorf("the server %s stopped and has been started again %d times: run /reconnect %s to start it afresh", s.name, maxRestarts, s.name)
	}
	s.restarts++

	if s.session != nil {
		_ = s.session.Close()
	}
	if s.closeLog != nil {
		s.closeLog()
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.timeout())
	defer cancel()
	transport, err := s.transport(true)
	if err != nil {
		return nil, fmt.Errorf("starting %s again: %w", s.name, err)
	}
	session, err := s.connect(ctx, transport, s.opts.Version)
	if err != nil {
		return nil, fmt.Errorf("starting %s again: %w", s.name, err)
	}
	s.session, s.dead = session, false
	return session, nil
}

// lost reports whether a call failed because the connection broke, and if so has the next call start the server
// again. An error the server itself sent, or the end of the context, is not that. A program that dies in the middle
// of a call gives a bare end-of-file, and the session notices its connection is gone a moment later.
func (s *server) lost(ctx context.Context, session *sdk.ClientSession, err error) bool {
	var rpc *jsonrpc.Error
	if ctx.Err() != nil || errors.As(err, &rpc) {
		return false
	}
	if !errors.Is(err, sdk.ErrConnectionClosed) && !s.noticesDeath(session) {
		return false
	}
	s.markDead(session)
	return true
}

// noticesDeath waits briefly for the watcher to see the session end.
func (s *server) noticesDeath(session *sdk.ClientSession) bool {
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s.mu.Lock()
		dead := s.dead && s.session == session
		s.mu.Unlock()
		if dead {
			return true
		}
	}
	return false
}

// reset makes the server ready to be started afresh: nothing of the old session is kept, and the restarts count anew.
func (s *server) reset() {
	s.stop()
	s.mu.Lock()
	s.session, s.dead, s.restarts = nil, false, 0
	s.mu.Unlock()
	s.all, s.tools, s.notes, s.err, s.defTokens = nil, nil, "", nil, 0
}

// stop ends the session, which stops a program the server runs, and closes its log.
func (s *server) stop() {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	if s.closeLog != nil {
		s.closeLog()
		s.closeLog = nil
	}
}

// describe turns a start error caused by the deadline or a cancel into words, and leaves other errors as they are.
func describe(ctx context.Context, err error, timeout string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		return fmt.Errorf("did not start within %s", timeout)
	case ctx.Err() != nil:
		return errors.New("startup was cancelled")
	}
	return err
}
