package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fakeServers is the MCP side of the UI: a fixed list, and a reconnect that says what was asked of it.
type fakeServers struct {
	list      []ServerInfo
	reconnect Startup
	asked     []string
}

func (f *fakeServers) List() []ServerInfo { return f.list }
func (f *fakeServers) Reconnect(name string) Startup {
	f.asked = append(f.asked, name)
	return f.reconnect
}

func withServers(s Servers) Model {
	m := newTestModel(&fakeAgent{})
	m.info.Servers = s
	return m
}

func TestServersListsEachServerWithWhatMattersAboutIt(t *testing.T) {
	m := withServers(&fakeServers{list: []ServerInfo{
		{Name: "docs", Tools: 2, Hidden: 3, Tokens: 1240, LogPath: "/state/docs.log", Restarts: 1},
		{Name: "web", Tools: 1, Tokens: 90},
		{Name: "dead", Tools: 4, Tokens: 400, Lost: true},
		{Name: "broken", Err: "did not start within 10s", LogPath: "/state/broken.log"},
	}})

	m, _ = typeAndEnter(t, m, "/servers")

	got := strings.Join(strings.Fields(said(m)), " ") // long rows wrap
	for _, want := range []string{
		"docs", "connected · 2 tools (3 hidden) · ~1,240 tokens of definitions · started again 1 time · log /state/docs.log",
		"web", "connected · 1 tool · ~90 tokens of definitions",
		"dead", "connection lost, the next call starts it again",
		"broken", "failed: did not start within 10s · log /state/broken.log",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/servers is missing %q:\n%s", want, got)
		}
	}
}

func TestServersAndReconnectWithoutServers(t *testing.T) {
	for _, line := range []string{"/servers", "/reconnect"} {
		m, cmd := typeAndEnter(t, newTestModel(&fakeAgent{}), line)
		if said(m) != "No MCP servers are configured" || cmd != nil || m.starting {
			t.Errorf("%s: said %q, starting %v", line, said(m), m.starting)
		}
	}
}

func TestReconnectRunsInTheBackgroundAndPromptsWaitForIt(t *testing.T) {
	servers := &fakeServers{
		list:      []ServerInfo{{Name: "docs"}},
		reconnect: Startup{Servers: [][2]string{{"docs", "connected, 2 tools"}}, Status: "mcp 1/1"},
	}
	a := &fakeAgent{}
	m := newTestModel(a)
	m.info.Servers = servers

	m, cmd := typeAndEnter(t, m, "/reconnect docs")
	if cmd == nil || !m.starting || len(servers.asked) != 0 {
		t.Fatalf("the command must return the work as a command, not do it now: cmd %v, starting %v, asked %v", cmd != nil, m.starting, servers.asked)
	}
	if !strings.Contains(ansi.Strip(m.headerView()), "connecting…") {
		t.Error("the header does not say the servers are starting")
	}

	m, _ = typeAndEnter(t, m, "typed meanwhile")
	if len(a.texts) != 0 || len(m.queue.prompts) != 1 {
		t.Fatalf("a prompt was sent while reconnecting: %v", a.texts)
	}

	m = pump(t, m, cmd)

	if m.starting || len(servers.asked) != 1 || servers.asked[0] != "docs" {
		t.Errorf("starting %v, asked %v", m.starting, servers.asked)
	}
	if strings.Join(a.texts, "|") != "typed meanwhile" {
		t.Errorf("the waiting prompt should go when the reconnect is done: sent %v", a.texts)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "connected, 2 tools") || !strings.Contains(view, "mcp 1/1") {
		t.Errorf("the result is not shown:\n%s", view)
	}
}

func TestReconnectWithoutANameAsksForNone(t *testing.T) {
	servers := &fakeServers{list: []ServerInfo{{Name: "a"}}, reconnect: Startup{Notes: []string{"Nothing to reconnect: every server is running"}}}
	m := withServers(servers)

	m, cmd := typeAndEnter(t, m, "/reconnect")
	m = pump(t, m, cmd)

	if len(servers.asked) != 1 || servers.asked[0] != "" {
		t.Errorf("asked %q", servers.asked)
	}
	if !strings.Contains(said(m), "Nothing to reconnect") {
		t.Errorf("said %q", said(m))
	}
}

func TestReconnectSuggestsTheServerNames(t *testing.T) {
	m := withServers(&fakeServers{list: []ServerInfo{{Name: "docs"}, {Name: "web"}, {Name: "wiki"}}})

	m.input.SetValue("/reconnect w")
	list, _ := m.suggestions()

	var labels []string
	for _, s := range list {
		labels = append(labels, s.label)
	}
	if strings.Join(labels, ",") != "web,wiki" {
		t.Errorf("suggestions = %v", labels)
	}
}

func TestGoldenServers(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m.info.Servers = &fakeServers{list: []ServerInfo{
		{Name: "docs", Tools: 2, Hidden: 3, Tokens: 1240, LogPath: "/home/u/.local/state/desk-agent/mcp/docs.log", Restarts: 1},
		{Name: "web", Tools: 1, Tokens: 90},
		{Name: "broken", Err: "did not start within 10s", LogPath: "/home/u/.local/state/desk-agent/mcp/broken.log"},
	}}
	m, _ = typeAndEnter(t, m, "/servers")
	checkGolden(t, "servers_100x24", screen(m))
}
