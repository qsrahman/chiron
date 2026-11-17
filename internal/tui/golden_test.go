package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// Golden files hold the screen as plain text (colours removed), so a layout change shows up as a readable
// diff. Run `go test ./internal/tui -update` after an intended change and review the diff.
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// screen returns what the user would see as plain text, without trailing spaces.
func screen(m Model) string {
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if got != string(want) {
		t.Errorf("screen differs from %s (run with -update if the change is intended):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// sized returns a model for a terminal of the given size, with a fixed clock.
func sized(sender Sender, width, height int) Model {
	m := newTestModel(sender)
	clock := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// conversation plays a typical exchange: markdown with a list and a code block, two tool calls (one
// succeeds, one fails) and a closing remark.
func conversation(t *testing.T, width, height int) Model {
	t.Helper()
	sender := &fakeSender{events: []agent.Event{
		agent.TextDelta{Text: "I'll check the project layout first.\n\n## Summary\n\nHere is what I found:\n\n- `main.go` starts the app\n- `internal/agent` runs the **conversation loop**\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n"},
		agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: `{"path":"main.go","limit":200}`},
		agent.ToolFinished{CallID: "c1", Name: "read_file", Output: "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n\n// end"},
		agent.ToolStarted{CallID: "c2", Name: "shell", Arguments: `{"command":"go test ./...","timeout":30}`},
		agent.ToolFinished{CallID: "c2", Name: "shell", Output: "error: exit status 1\nFAIL desk-agent/internal/agent 0.4s", Failed: true},
		agent.Usage{InputTokens: 2300, OutputTokens: 150},
		agent.TextDelta{Text: "The tests fail; I'll look into it."},
		agent.Done{},
	}}
	m := sized(sender, width, height)
	m, cmd := typeAndEnter(t, m, "What does this project do, and do the tests pass?")
	return pump(t, m, cmd)
}

func TestGoldenWelcome(t *testing.T) {
	checkGolden(t, "welcome_80x24", screen(sized(&fakeSender{}, 80, 24)))
}

func TestGoldenConversation(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"conversation_100x40", 100, 40},
		{"conversation_narrow_50x40", 50, 40},
		{"conversation_wide_160x40", 160, 40},
	} {
		t.Run(size.name, func(t *testing.T) {
			checkGolden(t, size.name, screen(conversation(t, size.width, size.height)))
		})
	}
}

func TestGoldenRunningTool(t *testing.T) {
	sender := &fakeSender{block: true}
	m := sized(sender, 100, 24)
	m, _ = typeAndEnter(t, m, "run the slow tests")
	t.Cleanup(func() { m.cancel() })
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"go test -race ./..."}`}})
	// Twelve seconds into the turn.
	later := m.now().Add(12 * time.Second)
	m.now = func() time.Time { return later }
	m, _ = update(m, m.spin.Tick())

	checkGolden(t, "running_tool_100x24", screen(m))
}

func TestGoldenErrorAndCancelled(t *testing.T) {
	sender := &fakeSender{events: []agent.Event{
		agent.TextDelta{Text: "Working on it"},
		agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"sleep 100"}`},
		agent.ToolFinished{CallID: "c1", Name: "shell", Output: "error: context canceled", Failed: true, Cancelled: true},
		agent.Error{Err: context.Canceled},
	}}
	m := sized(sender, 100, 24)
	m, cmd := typeAndEnter(t, m, "start it")
	m = pump(t, m, cmd)

	checkGolden(t, "cancelled_100x24", screen(m))
}

func TestGoldenMultilineInput(t *testing.T) {
	m := sized(&fakeSender{}, 80, 24)
	m.input.SetValue("first line\nsecond line\nthird line")
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24}) // any message re-lays out the input

	checkGolden(t, "multiline_input_80x24", screen(m))
	if got := m.input.Height(); got != 3 {
		t.Errorf("input height = %d, want 3 for three lines", got)
	}
}

// Whatever the terminal size (from 40 columns up), no line may be wider than the terminal: that would wrap
// and break the layout.
func TestNoLineIsWiderThanTheTerminal(t *testing.T) {
	for _, width := range []int{40, 50, 80, 100, 160, 220} {
		for name, build := range map[string]func() Model{
			"welcome":      func() Model { return sized(&fakeSender{}, width, 30) },
			"conversation": func() Model { return conversation(t, width, 40) },
		} {
			m := build()
			for i, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("%s at width %d: line %d is %d cells wide: %q", name, width, i, w, line)
				}
			}
		}
	}
}

func TestTextIsCappedOnWideTerminals(t *testing.T) {
	m := conversation(t, 220, 40)
	for i, line := range strings.Split(screen(m), "\n") {
		if w := ansi.StringWidth(line); w > maxTextWidth+margin {
			t.Errorf("line %d is %d cells wide, want at most %d: %q", i, w, maxTextWidth+margin, line)
		}
	}
}
