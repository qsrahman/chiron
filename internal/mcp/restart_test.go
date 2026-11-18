//go:build unix

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qsrahman/desk-agent/internal/config"
)

// restartable starts one stdio server and returns the hub and where its "die" tool records that it ran.
func restartable(t *testing.T) (*Hub, string) {
	t.Helper()
	dieLog := filepath.Join(t.TempDir(), "died")
	h := startHub(t, map[string]config.MCPServer{"local": stdio("serve", map[string]string{dieLogVar: dieLog})}, Options{Timeout: 10 * time.Second})
	if st := statusOf(h, "local"); st.Err != nil {
		t.Fatal(st.Err)
	}
	return h, dieLog
}

// kill ends the server's process from outside and waits until the connection is known to be lost.
func kill(t *testing.T, h *Hub) {
	t.Helper()
	out, err := callTool(t, h, "local__pid", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(out)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !statusOf(h, "local").Dead; {
		if time.Now().After(deadline) {
			t.Fatal("the lost connection was never noticed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestACallThatFindsTheConnectionBrokenIsNotRepeated(t *testing.T) {
	h, dieLog := restartable(t)

	_, err := callTool(t, h, "local__die", `{}`)

	if err == nil || !strings.Contains(err.Error(), "the next call starts it again") {
		t.Fatalf("err = %v, want the lost connection to be said", err)
	}
	if data, _ := os.ReadFile(dieLog); strings.Count(string(data), "died") != 1 {
		t.Errorf("the tool ran %d times; a call that may have done part of its work is never repeated", strings.Count(string(data), "died"))
	}

	// The next call starts the server again.
	out, err := callTool(t, h, "local__echo", `{"text":"again"}`)
	if err != nil || out != "echo: again" {
		t.Errorf("the next call = %q, %v", out, err)
	}
	if st := statusOf(h, "local"); st.Restarts != 1 || st.Dead {
		t.Errorf("status = %+v, want one restart and a live server", st)
	}
}

func TestAServerThatDiedWhileIdleIsStartedAgainByTheNextCallWithoutFailingIt(t *testing.T) {
	h, _ := restartable(t)
	kill(t, h)

	out, err := callTool(t, h, "local__echo", `{"text":"hi"}`)

	if err != nil || out != "echo: hi" {
		t.Errorf("a call after an idle death = %q, %v: nothing had been sent, so nothing needs to fail", out, err)
	}
	data, _ := os.ReadFile(statusOf(h, "local").LogPath)
	if strings.Count(string(data), "test server starting") != 2 {
		t.Errorf("the log should hold both starts (a restart appends): %q", data)
	}
}

func TestRestartsAreLimitedAndReconnectStartsAfresh(t *testing.T) {
	h, _ := restartable(t)
	for i := 1; i <= maxRestarts; i++ {
		kill(t, h)
		if out, err := callTool(t, h, "local__echo", `{"text":"x"}`); err != nil || out != "echo: x" {
			t.Fatalf("restart %d: %q, %v", i, out, err)
		}
	}

	kill(t, h)
	_, err := callTool(t, h, "local__echo", `{"text":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "/reconnect local") {
		t.Fatalf("after %d restarts the call gave %v, want it to point at /reconnect", maxRestarts, err)
	}

	if err := h.Reconnect(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if out, err := callTool(t, h, "local__echo", `{"text":"fresh"}`); err != nil || out != "echo: fresh" {
		t.Errorf("after /reconnect = %q, %v", out, err)
	}
	if st := statusOf(h, "local"); st.Restarts != 0 || st.Dead {
		t.Errorf("status = %+v, want the count to start again", st)
	}
}

// An error the server itself sends is not a lost connection.
func TestAnErrorFromTheServerDoesNotMakeItRestart(t *testing.T) {
	h, _ := restartable(t)

	_, err := callTool(t, h, "local__echo", `{"text":5}`) // the wrong type: the server answers with an error of its own
	if err == nil {
		t.Fatal("the server should have refused the wrong type")
	}
	if strings.Contains(err.Error(), "lost") {
		t.Errorf("an answer from the server was taken for a lost connection: %v", err)
	}

	if st := statusOf(h, "local"); st.Dead || st.Restarts != 0 {
		t.Errorf("status = %+v", st)
	}
	if out, err := callTool(t, h, "local__echo", `{"text":"fine"}`); err != nil || out != "echo: fine" {
		t.Errorf("the next call = %q, %v", out, err)
	}
	if st := statusOf(h, "local"); st.Restarts != 0 {
		t.Errorf("the server was restarted although it never died: %+v", st)
	}
}

func TestReconnectWithoutANameStartsAServerWhoseConnectionWasLost(t *testing.T) {
	h, _ := restartable(t)
	kill(t, h)

	if err := h.Reconnect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	if st := statusOf(h, "local"); st.Dead || st.Err != nil {
		t.Errorf("status = %+v, want the lost server to be started again", st)
	}
	if out, err := callTool(t, h, "local__echo", `{"text":"back"}`); err != nil || out != "echo: back" {
		t.Errorf("after /reconnect = %q, %v", out, err)
	}
}
