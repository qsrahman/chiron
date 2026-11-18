package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/desk-agent/internal/agent"
)

func TestToolParams(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantMain string
		wantRest []string
	}{
		{"path is the main parameter", `{"path":"main.go","limit":200}`, "main.go", []string{"limit=200"}},
		{"command beats unknown keys", `{"timeout":30,"command":"go test ./..."}`, "go test ./...", []string{"timeout=30"}},
		{"path wins over command", `{"command":"cat","path":"a.txt"}`, "a.txt", []string{"command=cat"}},
		{"no known key: everything is key=value, sorted", `{"b":true,"a":"x"}`, "", []string{"a=x", "b=true"}},
		{"nested values stay on one line", `{"path":"p","opts":{"x":[1,2]}}`, "p", []string{`opts={"x":[1,2]}`}},
		{"multi-line strings are cut at the first line", `{"command":"echo a\necho b"}`, "echo a …", nil},
		{"long values are cut", `{"path":"p","note":"0123456789012345678901234567890123456789012345678901234567890"}`, "p", []string{"note=012345678901234567890123456789012345678…"}},
		{"empty object", `{}`, "", nil},
		{"empty string", ``, "", nil},
		{"not JSON: shown as it is", `not json`, "not json", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMain, gotRest := toolParams(tt.raw)
			if gotMain != tt.wantMain || !reflect.DeepEqual(gotRest, tt.wantRest) {
				t.Errorf("toolParams(%s) = %q, %q; want %q, %q", tt.raw, gotMain, gotRest, tt.wantMain, tt.wantRest)
			}
		})
	}
}

func TestCardHeaderNeverShowsRawJSON(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: `{"path":"main.go","limit":200}`}})

	plain := ansi.Strip(lastEntry(m).rendered)
	if want := "read_file  main.go (limit=200)"; !strings.Contains(plain, want) {
		t.Errorf("card = %q, want it to contain %q", plain, want)
	}
	if strings.Contains(plain, "{") || strings.Contains(plain, `"path"`) {
		t.Errorf("card shows raw JSON: %q", plain)
	}
}

func TestSlowCallsShowTheirDuration(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	m.now = func() time.Time { return now }

	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{"command":"make"}`}})
	if plain := ansi.Strip(lastEntry(m).rendered); strings.Contains(plain, "·") {
		t.Errorf("a fresh call shows no duration: %q", plain)
	}

	now = start.Add(3200 * time.Millisecond)
	m, _ = update(m, eventMsg{agent.ToolFinished{CallID: "c1", Name: "shell", Output: "ok"}})
	if plain := ansi.Strip(lastEntry(m).rendered); !strings.Contains(plain, "· 3s") {
		t.Errorf("a finished slow call shows its duration: %q", plain)
	}
}

func TestSpinnerAnimatesWhileBusyAndStopsWhenIdle(t *testing.T) {
	m := newTestModel(&fakeAgent{block: true})
	m, _ = typeAndEnter(t, m, "go")
	t.Cleanup(func() { m.cancel() })
	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "shell", Arguments: `{}`}})

	before := lastEntry(m).rendered
	m, cmd := update(m, m.spin.Tick())
	if cmd == nil {
		t.Error("a busy model keeps the spinner ticking")
	}
	if lastEntry(m).rendered == before {
		t.Error("the running card did not advance its spinner frame")
	}

	idle := newTestModel(&fakeAgent{})
	if _, cmd := update(idle, spinner.TickMsg{}); cmd != nil {
		t.Error("an idle model must stop the spinner ticks")
	}
}

func TestInputGrowsWithItsTextAndShrinksTheTranscript(t *testing.T) {
	m := sized(&fakeAgent{}, 80, 24)
	one := m.viewport.Height()

	m.input.SetValue("a\nb\nc\nd")
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := m.viewport.Height(); got != one-3 {
		t.Errorf("viewport height = %d, want %d (three more input rows)", got, one-3)
	}
}
