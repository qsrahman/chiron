package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/qsrahman/desk-agent/internal/config"
)

// server is one configured MCP server and, once it has started, its session and its tools.
type server struct {
	name string
	cfg  config.MCPServer

	source   string // how it is run, for the approval prompt: the command line as configured, or the host
	logPath  string // where a stdio server's error output goes
	closeLog func()

	session *sdk.ClientSession
	tools   []*sdk.Tool // sorted by name
	err     error       // why it did not start
}

// start launches the server, connects and lists its tools. The outcome is left in the fields; a server that did not
// start has released everything it had.
func (s *server) start(ctx context.Context, o Options) {
	l, err := resolve(s.cfg, o.lookup())
	if err != nil {
		s.err = err
		return
	}
	var transport sdk.Transport
	if s.cfg.URL != "" {
		transport, s.source, err = remoteTransport(l)
	} else {
		s.source = strings.TrimSpace(s.cfg.Command + " " + strings.Join(s.cfg.Args, " "))
		transport, s.logPath, s.closeLog, err = stdioTransport(l, o.LogDir, s.name)
	}
	if err != nil {
		s.err = err
		return
	}
	s.attach(ctx, transport, o.Version)
	if s.err != nil {
		s.stop()
	}
}

// attach connects over the transport and lists the tools.
func (s *server) attach(ctx context.Context, transport sdk.Transport, version string) {
	client := sdk.NewClient(&sdk.Implementation{Name: "desk-agent", Version: version}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		s.err = err
		return
	}
	s.session = session
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			s.err = fmt.Errorf("listing tools: %w", err)
			return
		}
		s.tools = append(s.tools, tool)
	}
	slices.SortFunc(s.tools, func(a, b *sdk.Tool) int { return strings.Compare(a.Name, b.Name) })
}

// stop ends the session, which stops a program the server runs, and closes its log.
func (s *server) stop() {
	if s.session != nil {
		_ = s.session.Close()
	}
	if s.closeLog != nil {
		s.closeLog()
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
