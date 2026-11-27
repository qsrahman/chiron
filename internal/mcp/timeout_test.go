package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qsrahman/chiron/internal/config"
)

func TestEachServerHasItsOwnStartupTimeout(t *testing.T) {
	short := stdio("hang", nil)
	short.StartupTimeout = new(1)
	shared := stdio("hang", nil)
	h := Start(context.Background(), map[string]config.MCPServer{"own": short, "shared": shared},
		Options{Timeout: 2 * time.Second, LogDir: filepath.Join(t.TempDir(), "logs")})
	t.Cleanup(h.Close)

	check := func(when string) {
		t.Helper()
		for name, want := range map[string]string{"own": "did not start within 1s", "shared": "did not start within 2s"} {
			if err := statusOf(h, name).Err; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s, %s: %v, want %q", when, name, err, want)
			}
		}
	}
	h.Wait()
	check("at launch")

	if err := h.Reconnect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	check("on reconnect")
}

func TestTheStartupTimeoutDefaultsToThirtySeconds(t *testing.T) {
	s := &server{cfg: config.MCPServer{}}
	if got := s.startupTimeout(Options{}); got != 30*time.Second {
		t.Errorf("timeout = %v", got)
	}
}
