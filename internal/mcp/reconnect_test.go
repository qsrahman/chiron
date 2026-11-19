package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qsrahman/chiron/internal/config"
)

// switchable is an environment that a test changes between the start of the servers and a reconnect.
type switchable struct {
	mu   sync.Mutex
	vars map[string]string
}

func (e *switchable) set(name, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vars[name] = value
}

func (e *switchable) lookup(name string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.vars[name]
	return v, ok
}

// modal is a server whose mode comes from ${MODE}: "serve" works, "exit" dies at once.
func modal(extra map[string]string) config.MCPServer {
	return config.MCPServer{Command: os.Args[0], Env: selfCommand("${MODE}", extra)}
}

func TestReconnectBringsInAServerThatFailedAndLeavesTheRestAlone(t *testing.T) {
	env := &switchable{vars: map[string]string{"MODE": "exit"}}
	h := startHub(t, map[string]config.MCPServer{"flaky": modal(nil), "steady": stdio("serve", nil)}, Options{Lookup: env.lookup, Timeout: 5 * time.Second})
	if statusOf(h, "flaky").Err == nil {
		t.Fatal("flaky should have failed to start")
	}
	steadySession := h.servers[1].session

	env.set("MODE", "serve")
	if err := h.Reconnect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	if st := statusOf(h, "flaky"); st.Err != nil || st.Tools != 5 {
		t.Errorf("flaky after reconnect = %+v", st)
	}
	if out, err := callTool(t, h, "flaky__echo", `{"text":"joined"}`); err != nil || out != "echo: joined" {
		t.Errorf("a reconnected server's tool = %q, %v", out, err)
	}
	if h.servers[1].session != steadySession {
		t.Error("a server that was fine was restarted by /reconnect without a name")
	}
}

func TestReconnectWithANameRestartsThatServerEvenWhenItIsFine(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{"steady": stdio("serve", nil)}, Options{Timeout: 5 * time.Second})
	before := h.servers[0].session

	if err := h.Reconnect(context.Background(), "steady"); err != nil {
		t.Fatal(err)
	}

	if h.servers[0].session == before || statusOf(h, "steady").Err != nil {
		t.Error("the named server was not started afresh")
	}
}

func TestReconnectRefusesAnUnknownOrDisabledName(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{"a": stdio("serve", nil), "b": {Command: "x", Enabled: new(false)}}, Options{})

	err := h.Reconnect(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("unknown name: %v, want the names that exist", err)
	}
	err = h.Reconnect(context.Background(), "b")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("disabled server: %v", err)
	}
}

func TestReconnectWaitsForTheFirstStartToFinish(t *testing.T) {
	h := Start(context.Background(), map[string]config.MCPServer{"slow": stdio("hang", nil)}, Options{Timeout: time.Second, LogDir: filepath.Join(t.TempDir(), "logs")})
	t.Cleanup(h.Close)

	started := time.Now()
	if err := h.Reconnect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	select {
	case <-h.done:
	default:
		t.Error("Reconnect returned while the first start was still running")
	}
	if time.Since(started) < 900*time.Millisecond {
		t.Errorf("Reconnect returned after %v, before the first start could have ended", time.Since(started))
	}
}

func TestReconnectWithNothingFailedDoesNothing(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{"a": stdio("serve", nil)}, Options{})
	before := h.servers[0].session

	if err := h.Reconnect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	if h.servers[0].session != before {
		t.Error("a healthy server was restarted")
	}
}

func TestTheAllowListOffersOnlyWhatItNamesAndCountsTheRest(t *testing.T) {
	cfg := stdio("serve", nil)
	cfg.Tools = []string{"echo", "env", "echo"}
	h := startHub(t, map[string]config.MCPServer{"local": cfg}, Options{})

	st := statusOf(h, "local")
	list, _ := h.Tools(nil)

	if st.Err != nil || st.Tools != 2 || st.Hidden != 3 || len(list) != 2 {
		t.Errorf("status = %+v, %d tools offered", st, len(list))
	}
	if list[0].Name() != "local__echo" || list[1].Name() != "local__env" {
		t.Errorf("offered %q and %q", list[0].Name(), list[1].Name())
	}
}

func TestAnAllowListNameTheServerLacksFailsTheServer(t *testing.T) {
	cfg := stdio("serve", nil)
	cfg.Tools = []string{"echo", "ecoh"}
	h := startHub(t, map[string]config.MCPServer{"local": cfg}, Options{})

	err := statusOf(h, "local").Err

	if err == nil || !strings.Contains(err.Error(), `"ecoh"`) || !strings.Contains(err.Error(), "echo, env") {
		t.Errorf("err = %v, want the typo and what the server offers", err)
	}
	if list, _ := h.Tools(nil); len(list) != 0 {
		t.Error("a server with a bad allow-list offered tools")
	}
}

func TestAnEmptyAllowListOffersNothing(t *testing.T) {
	cfg := stdio("serve", nil)
	cfg.Tools = []string{}
	h := startHub(t, map[string]config.MCPServer{"local": cfg}, Options{})

	if st := statusOf(h, "local"); st.Err != nil || st.Tools != 0 || st.Hidden != 5 {
		t.Errorf("status = %+v", st)
	}
}

func TestStatusSaysWhatTheDefinitionsCost(t *testing.T) {
	h := startHub(t, map[string]config.MCPServer{"local": stdio("serve", nil)}, Options{})

	st := statusOf(h, "local")

	if st.Tokens < 50 || st.Tokens > 2000 {
		t.Errorf("five tool definitions are estimated at %d tokens", st.Tokens)
	}
}

func TestNotesCarryOnlyWhatWasAskedForAndAreCut(t *testing.T) {
	asked, quiet := stdio("serve", nil), stdio("serve", nil)
	asked.Instructions = true
	h := startHub(t, map[string]config.MCPServer{"asked": asked, "quiet": quiet}, Options{})

	notes := h.Notes()

	if len(notes) != 1 || notes[0].Server != "asked" || notes[0].Text != "Use echo for greetings." {
		t.Errorf("notes = %+v, want only the server that was configured for it", notes)
	}

	long := &Hub{servers: []*server{{name: "wordy", cfg: config.MCPServer{Instructions: true}, notes: strings.Repeat("é", 5000)}}}
	if got := long.Notes(); len(got) != 1 || len([]rune(got[0].Text)) > maxNoteLength+10 || !strings.HasSuffix(got[0].Text, "[cut]") {
		t.Errorf("a long note was not cut: %d characters", len([]rune(got[0].Text)))
	}
}
