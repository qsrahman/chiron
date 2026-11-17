// Package tui is the Bubble Tea front-end: a transcript, an input box and a status line.
// It only consumes agent events, so the agent package never depends on it.
package tui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// Sender starts an agent turn. *agent.Agent implements it; tests use a fake.
type Sender interface {
	Send(ctx context.Context, text string) <-chan agent.Event
}

const (
	inputHeight  = 3 // rows of the input box
	statusHeight = 1
)

// eventMsg carries one agent event into Update; turnClosedMsg means the channel is closed.
type eventMsg struct{ event agent.Event }
type turnClosedMsg struct{}

// Model is the Bubble Tea model. Like all Bubble Tea models it is copied on every Update,
// so it holds no strings.Builder or other copy-sensitive values.
type Model struct {
	sender    Sender
	modelName string

	entries   []entry
	streaming string // assistant text of the turn in progress, not yet markdown-rendered

	// events is non-nil while a turn is running. cancel stops it; cancelling is set after the
	// first Ctrl-C so that a second one quits instead of waiting.
	events     <-chan agent.Event
	cancel     context.CancelFunc
	cancelling bool

	viewport viewport.Model
	input    textarea.Model
	markdown *glamour.TermRenderer
	dark     bool

	width, height int
}

// New returns a model that sends prompts to sender. modelName is only shown in the status line.
func New(sender Sender, modelName string) Model {
	input := textarea.New()
	input.Placeholder = "Ask something…"
	input.ShowLineNumbers = false
	input.Prompt = "› "
	// Enter submits, so a newline needs another key.
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	input.Focus()

	m := Model{
		sender:    sender,
		modelName: modelName,
		viewport:  viewport.New(),
		input:     input,
		dark:      true, // replaced when the terminal reports its background
		width:     80,
		height:    24,
	}
	m.layout()
	return m
}

// Init asks the terminal for its background colour so markdown uses a matching theme.
func (m Model) Init() tea.Cmd {
	return tea.RequestBackgroundColor
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.rerender()
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.rerender()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case eventMsg:
		m.handleEvent(msg.event)
		return m, waitForEvent(m.events)

	case turnClosedMsg:
		m.finishTurn()
		return m, nil
	}

	// Anything else (cursor blink, paste, ...) belongs to the input box.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	busy := m.events != nil
	switch msg.String() {
	case "ctrl+c":
		if !busy || m.cancelling {
			return m, tea.Quit
		}
		m.cancelling = true
		m.cancel()
		return m, nil

	case "ctrl+d":
		if !busy && m.input.Value() == "" {
			return m, tea.Quit
		}

	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if busy || text == "" {
			return m, nil
		}
		return m.submit(text)

	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) submit(text string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.addEntry(entry{role: roleUser, text: text})

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.sender.Send(ctx, text)
	m.refresh()
	return m, waitForEvent(m.events)
}

// waitForEvent blocks in a command goroutine until the agent produces its next event.
func waitForEvent(events <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return turnClosedMsg{}
		}
		return eventMsg{event}
	}
}

func (m *Model) handleEvent(event agent.Event) {
	switch e := event.(type) {
	case agent.TextDelta:
		m.streaming += e.Text
	case agent.ToolStarted:
		m.commitStreaming() // keep the text the model wrote before the call above its card
		m.addEntry(entry{role: roleTool, callID: e.CallID, text: e.Name + " " + e.Arguments})
	case agent.ToolFinished:
		m.finishTool(e)
	case agent.Done:
		m.commitStreaming()
	case agent.Error:
		// Keep what was already shown; the agent has dropped it from its own history.
		m.commitStreaming()
		if errors.Is(e.Err, context.Canceled) {
			m.addEntry(entry{role: roleNotice, text: "cancelled"})
		} else {
			m.addEntry(entry{role: roleError, text: "error: " + e.Err.Error()})
		}
	}
	m.refresh()
}

func (m *Model) commitStreaming() {
	if m.streaming != "" {
		m.addEntry(entry{role: roleAssistant, text: m.streaming})
		m.streaming = ""
	}
}

// finishTool completes the card started for the same call. Parallel calls can finish in any order.
func (m *Model) finishTool(e agent.ToolFinished) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].role == roleTool && m.entries[i].callID == e.CallID {
			m.entries[i].done, m.entries[i].failed, m.entries[i].output = true, e.Failed, e.Output
			m.entries[i].rendered = m.render(m.entries[i])
			return
		}
	}
}

// finishTurn runs once the agent has closed its channel, so the next prompt may start.
func (m *Model) finishTurn() {
	m.cancel()
	m.events, m.cancel, m.cancelling = nil, nil, false
}

func (m *Model) addEntry(e entry) {
	e.rendered = m.render(e)
	m.entries = append(m.entries, e)
}

// layout sizes the children for the current window and rebuilds the markdown renderer,
// whose word wrap depends on the width.
func (m *Model) layout() {
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(max(1, m.height-inputHeight-statusHeight))
	m.input.SetWidth(m.width)
	m.input.SetHeight(inputHeight)
	m.markdown = newMarkdownRenderer(m.width, m.dark)
}

// rerender re-styles every entry after a resize or a theme change.
func (m *Model) rerender() {
	m.markdown = newMarkdownRenderer(m.width, m.dark)
	for i := range m.entries {
		m.entries[i].rendered = m.render(m.entries[i])
	}
	m.refresh()
}

// refresh rebuilds the viewport content, following new output only if the user is at the bottom.
func (m *Model) refresh() {
	parts := make([]string, 0, len(m.entries)+1)
	for _, e := range m.entries {
		parts = append(parts, e.rendered)
	}
	if m.streaming != "" {
		parts = append(parts, wrap(m.streaming, m.width))
	}

	follow := m.viewport.AtBottom()
	m.viewport.SetContent(strings.Join(parts, "\n\n"))
	if follow {
		m.viewport.GotoBottom()
	}
}

// View draws the transcript, the status line and the input box.
func (m Model) View() tea.View {
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		m.statusLine(),
		m.input.View(),
	))
	v.AltScreen = true
	return v
}

func (m Model) statusLine() string {
	var text string
	switch {
	case m.cancelling:
		text = "cancelling… ctrl+c again to quit"
	case m.events != nil:
		text = "working… ctrl+c cancels"
	default:
		text = m.modelName + " · enter send · ctrl+j newline · pgup/pgdn scroll · ctrl+c quit"
	}
	return noticeStyle.Render(text)
}

// Run starts the interactive UI and returns when the user quits.
func Run(sender Sender, modelName string) error {
	final, err := tea.NewProgram(New(sender, modelName)).Run()
	// Quitting with a turn still running (second Ctrl-C) must not leave its request open.
	if m, ok := final.(Model); ok && m.cancel != nil {
		m.cancel()
	}
	return err
}
