package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

var sampleStartup = Startup{
	Servers: [][2]string{{"docs", "connected, 2 tools"}, {"toolbox", "failed: did not start within 10s"}},
	Notes:   []string{"docs: the tool \"x\" is left out"},
	Status:  "mcp 1/2",
}

// starting returns a model whose Startup function has not finished.
func starting(t *testing.T) (Model, *fakeAgent) {
	t.Helper()
	a := &fakeAgent{}
	m := newTestModel(a)
	m.info.Startup = func() Startup { return sampleStartup }
	m.starting = true
	return m, a
}

func TestWhileStartingPromptsWaitAndCommandsAreRefused(t *testing.T) {
	m, a := starting(t)

	if !strings.Contains(ansi.Strip(m.headerView()), "connecting…") {
		t.Errorf("the header does not say the servers are starting: %q", ansi.Strip(m.headerView()))
	}
	m, cmd := typeAndEnter(t, m, "first prompt")
	if cmd != nil || len(a.texts) != 0 || len(m.queue.prompts) != 1 {
		t.Fatalf("the prompt was sent or lost: cmd %v, sent %v, queue %v", cmd != nil, a.texts, m.queue.prompts)
	}

	m, _ = typeAndEnter(t, m, "/help")
	if len(m.queue.prompts) != 1 || !strings.Contains(m.hint, "starting") || m.input.Value() != "/help" {
		t.Errorf("a command during startup: queue %v, hint %q, input %q", m.queue.prompts, m.hint, m.input.Value())
	}
	if list, _ := m.suggestions(); len(list) != 0 {
		t.Error("commands are refused while starting, so none should be suggested")
	}
}

func TestWhenStartupEndsItIsReportedAndTheWaitingPromptGoes(t *testing.T) {
	m, a := starting(t)
	m, _ = typeAndEnter(t, m, "first prompt")

	m, cmd := update(m, startupMsg{sampleStartup})

	if m.starting || cmd == nil || strings.Join(a.texts, "|") != "first prompt" {
		t.Fatalf("starting %v, sent %v: the queued prompt should go now", m.starting, a.texts)
	}
	screen := ansi.Strip(m.View().Content)
	for _, want := range []string{"docs", "connected, 2 tools", "toolbox", "failed: did not start within 10s", "the tool \"x\" is left out", "mcp 1/2"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen is missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "connecting…") {
		t.Error("the header still says connecting")
	}
}

func TestStartupWithNothingWaitingSendsNothing(t *testing.T) {
	m, a := starting(t)

	m, cmd := update(m, startupMsg{sampleStartup})

	if cmd != nil || len(a.texts) != 0 || m.events != nil {
		t.Errorf("a turn started with nothing to send: sent %v", a.texts)
	}
}

func TestInitRunsTheStartupFunction(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.info.Startup = func() Startup { return sampleStartup }

	cmd := m.Init()

	queue := []tea.Cmd{cmd}
	var got *Startup
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case startupMsg:
			got = &msg.Startup
		}
	}
	if got == nil || got.Status != "mcp 1/2" {
		t.Errorf("Init did not run the startup function: %+v", got)
	}
}

func TestWithoutAStartupFunctionThereIsNothingToWaitFor(t *testing.T) {
	if m := newTestModel(&fakeAgent{}); m.starting {
		t.Error("a model with no Startup function starts out waiting")
	}
}

func TestAToolWithATitleShowsItOnItsCard(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)

	m, _ = update(m, eventMsg{agent.ToolStarted{CallID: "c1", Name: "srv__echo", Title: "srv → echo", Arguments: `{"text":"hi"}`}})

	card := ansi.Strip(m.entries[0].rendered)
	if !strings.Contains(card, "srv → echo") || strings.Contains(card, "srv__echo") {
		t.Errorf("card: %q", card)
	}
}

func TestTheDialogSaysWhereAnMCPToolRuns(t *testing.T) {
	m, _, _ := asking(t, "contents")
	m, _ = update(m, eventMsg{agent.ApprovalRequested{CallID: "c1", Tool: "docs__search", Summary: "docs → search", Details: "{}",
		Source: "npx -y @scope/docs\x1b[2J@latest", Reply: make(chan agent.Decision, 1)}})

	view := m.View().Content
	plain := ansi.Strip(view)

	if !strings.Contains(plain, "Runs: npx -y @scope/docs[2J@latest") {
		t.Errorf("the dialog does not say where the server runs:\n%s", plain)
	}
	if strings.Contains(view, "\x1b[2J") {
		t.Error("the source reached the screen unsanitised")
	}
}

func TestGoldenStartup(t *testing.T) {
	m, _ := starting(t)
	m = sized(m.agent, 100, 24)
	m.info.Startup = func() Startup { return sampleStartup }
	m.starting = true
	checkGolden(t, "starting_100x24", screen(m))

	m, _ = update(m, startupMsg{sampleStartup})
	checkGolden(t, "started_100x24", screen(m))
}

func TestGoldenMCPApproval(t *testing.T) {
	m, _, _ := asking(t, "query: streaming parsers\nlimit: 5")
	m.approval.req.Summary = "docs → search"
	m.approval.req.Source = "npx -y @scope/docs-mcp@latest"
	m.layoutApproval()
	checkGolden(t, "approval_mcp_100x30", screen(m))
}
