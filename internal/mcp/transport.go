package mcp

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/qsrahman/chiron/internal/config"
)

// terminateDuration is how long a stdio server gets to exit after its input is closed, before it is signalled.
// The SDK's default is longer, and quitting should not hang on a server that ignores the end of its input.
const terminateDuration = 2 * time.Second

// launch is a server's configuration with every ${NAME} expanded.
type launch struct {
	command string
	args    []string
	env     []string // NAME=value, added to the environment of this program
	url     string
	headers map[string]string
}

// resolve expands the configuration. A variable that is not set is an error that says where it was used.
func resolve(cfg config.MCPServer, lookup func(string) (string, bool)) (launch, error) {
	var l launch
	var err error
	field := func(where, value string) string {
		if err != nil {
			return ""
		}
		var out string
		if out, err = expand(value, lookup); err != nil {
			err = fmt.Errorf("%s: %w", where, err)
		}
		return out
	}
	l.command = field("command", cfg.Command)
	for i, a := range cfg.Args {
		l.args = append(l.args, field(fmt.Sprintf("args[%d]", i), a))
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.Env)) { // in order, so that the first unset variable is always the same
		l.env = append(l.env, k+"="+field("env."+k, cfg.Env[k]))
	}
	l.url = field("url", cfg.URL)
	if len(cfg.Headers) > 0 {
		l.headers = map[string]string{}
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.Headers)) {
		l.headers[k] = field("headers."+k, cfg.Headers[k])
	}
	return l, err
}

// checkURL accepts an https address, or http only for this machine, without credentials in it. The problem is
// described without repeating the address, which may carry a secret in its path or query.
func checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("url is not a valid address")
	}
	if u.User != nil {
		return nil, errors.New("url must not contain a user name or password: use headers")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return nil, errors.New("url must be https (http is only allowed for localhost)")
	}
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// originGuard adds the configured headers to requests for the configured host and to no other: a redirect must
// not carry a key to another site. (Go forwards custom headers across a redirect; it strips only a few.)
type originGuard struct {
	base    http.RoundTripper
	host    string
	headers map[string]string
}

func (g originGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == g.host {
		req = req.Clone(req.Context())
		for k, v := range g.headers {
			req.Header.Set(k, v)
		}
	}
	return g.base.RoundTrip(req)
}

// remoteTransport connects to a server over HTTP.
func remoteTransport(l launch) (sdk.Transport, string, error) {
	u, err := checkURL(l.url)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{
		Transport: originGuard{base: http.DefaultTransport, host: u.Host, headers: l.headers},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != u.Host {
				return errors.New("the server redirected to another address, which is refused")
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	return &sdk.StreamableClientTransport{Endpoint: l.url, HTTPClient: client}, u.Host, nil
}

// stdioTransport starts the server as a child process. It gets this program's environment plus its own, and its
// error output goes to a log file: the terminal belongs to the UI. The log is private; it starts empty, and a
// restart appends to it.
func stdioTransport(l launch, logDir, name string, appendLog bool) (transport sdk.Transport, logPath string, closeLog func(), err error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, "", nil, fmt.Errorf("creating the log directory: %w", err)
	}
	logPath = filepath.Join(logDir, name+".log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendLog { // a restart adds to what the server said before it died
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(logPath, flags, 0o600)
	if err != nil {
		return nil, "", nil, fmt.Errorf("opening the log: %w", err)
	}
	cmd := exec.Command(l.command, l.args...)
	cmd.Env = append(os.Environ(), l.env...)
	cmd.Stderr = file
	return &sdk.CommandTransport{Command: cmd, TerminateDuration: terminateDuration}, logPath, func() { file.Close() }, nil
}
