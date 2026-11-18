// Package tui is the Bubble Tea front-end: a header, a transcript, an input box and a help bar.
// It only consumes agent events, so the agent package never depends on it.
package tui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/qsrahman/desk-agent/internal/agent"
)

// Agent is what the UI uses of the agent: *agent.Agent implements it, and tests use a fake. The slash commands
// call everything but Send, and only between turns.
type Agent interface {
	Send(ctx context.Context, text string) <-chan agent.Event
	Reset()
	Undo() bool
	Rewind() (prompt string, ok bool)
	HasConversation() bool
	Settings() agent.Settings
	SetModel(model string)
	SetReasoningEffort(effort string)
	ContextEstimate() agent.ContextEstimate
}

// Info is what the UI shows about the session. Only the fields it displays are here.
type Info struct {
	Version       string
	WorkDir       string
	Branch        string // empty outside a git repository
	ContextWindow int    // tokens; 0 when unknown
}

// eventMsg carries one agent event into Update; turnClosedMsg means the channel is closed.
type eventMsg struct{ event agent.Event }
type turnClosedMsg struct{}

// Model is the Bubble Tea model. Like all Bubble Tea models it is copied on every Update,
// so it holds no strings.Builder or other copy-sensitive values.
type Model struct {
	agent    Agent
	info     Info
	commands []command

	entries   []entry
	streaming string // assistant text of the turn in progress, not yet markdown-rendered

	// events is non-nil while a turn is running. cancel stops it; cancelling is set after the
	// first Ctrl-C so that a second one quits instead of waiting.
	events     <-chan agent.Event
	cancel     context.CancelFunc
	cancelling bool
	turnStart  time.Time
	now        func() time.Time // replaceable so tests can control time

	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
	help     help.Model
	keys     keyMap

	th       theme
	sty      styles
	markdown *glamour.TermRenderer

	// quitArmed is set by a first Ctrl-C or Ctrl-D on an empty input once there is a conversation, which only
	// lives in memory: one stray press must not end it. hint is what the help bar says about it.
	quitArmed bool
	hint      string

	// The suggestion list: the selected row, and the input text it was closed with Esc for.
	sel       int
	dismissed string

	contextTokens int64 // size of the conversation after the latest response, 0 until known

	width, height int
}

// New returns a model that sends prompts to a.
func New(a Agent, info Info) Model {
	keys := newKeyMap()
	input := textarea.New()
	input.Placeholder = "Ask something…"
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 8
	input.KeyMap.InsertNewline = keys.newline
	input.Focus()

	// Whatever the session shows may come from the environment (a directory name, say): clean it once here.
	info.Version, info.WorkDir, info.Branch = sanitize(info.Version), sanitize(info.WorkDir), sanitize(info.Branch)

	m := Model{
		agent:    a,
		info:     info,
		commands: commandList(),
		now:      time.Now,
		viewport: viewport.New(),
		input:    input,
		keys:     keys,
		width:    80,
		height:   24,
	}
	m.applyTheme(newTheme(true, nil)) // replaced when the terminal reports its background
	m.layout()
	m.refresh()
	return m
}

// applyTheme (re)builds everything that depends on the palette.
func (m *Model) applyTheme(t theme) {
	m.th = t
	m.sty = newStyles(t)
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.sty.iconRunning))
	m.help = help.New()
	m.help.Styles = m.sty.help
	m.help.ShortSeparator = " · "
	m.inputStyles()
	m.markdown = newMarkdownRenderer(m.contentWidth()-gutter, t)
}

// Init asks the terminal for its background colour so the palette fits it.
func (m Model) Init() tea.Cmd {
	return tea.RequestBackgroundColor
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	next.layout() // the input grows and shrinks with its text
	return next, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.markdown = newMarkdownRenderer(m.contentWidth()-gutter, m.th)
		m.rerender()
		return m, nil

	case tea.BackgroundColorMsg:
		m.applyTheme(newTheme(msg.IsDark(), color.Color(msg.Color)))
		m.rerender()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case eventMsg:
		m.handleEvent(msg.event)
		return m, waitForEvent(m.events)

	case turnClosedMsg:
		m.finishTurn()
		m.refresh()
		return m, nil

	case spinner.TickMsg:
		if m.events == nil {
			return m, nil // the turn is over: stop ticking
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.rerenderRunning()
		return m, cmd
	}

	// Anything else (cursor blink, paste, ...) belongs to the input box.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	busy := m.events != nil
	if list, typed := m.suggestions(); len(list) > 0 && m.suggestKey(msg, list, typed) {
		return m, nil
	}
	interrupt, eof := key.Matches(msg, m.keys.interrupt), key.Matches(msg, m.keys.eof)
	if !interrupt && !eof {
		m.quitArmed, m.hint = false, "" // any other key takes the warning back
	}
	switch {
	case interrupt:
		if busy {
			if m.cancelling {
				return m, tea.Quit
			}
			m.cancelling = true
			m.cancel()
			m.refresh()
			return m, nil
		}
		if m.input.Value() != "" { // a stray Ctrl-C should cost the typed text, not the conversation
			m.input.Reset()
			m.quitArmed, m.hint = false, ""
			return m, nil
		}
		return m.confirmQuit("ctrl+c")

	case eof:
		if !busy && m.input.Value() == "" {
			return m.confirmQuit("ctrl+d")
		}

	case key.Matches(msg, m.keys.send):
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		if cmd, handled := m.dispatch(text); handled {
			return m, cmd
		}
		if busy {
			return m, nil
		}
		return m.submit(text)

	case key.Matches(msg, m.keys.scroll):
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.sel = 0 // the list changed under the selection
	}
	return m, cmd
}

// confirmQuit quits at once when there is nothing to lose, and otherwise asks for the key to be pressed again.
// The conversation lives only in memory, so one stray press must not end it.
func (m Model) confirmQuit(pressed string) (Model, tea.Cmd) {
	if !m.agent.HasConversation() || m.quitArmed {
		return m, tea.Quit
	}
	m.quitArmed = true
	m.hint = "Press " + pressed + " again to quit: the conversation is not saved"
	return m, nil
}

func (m Model) submit(text string) (Model, tea.Cmd) {
	cmd := m.startTurn(text)
	return m, cmd
}

// startTurn shows text as the user's prompt and sends it to the agent.
func (m *Model) startTurn(text string) tea.Cmd {
	m.input.Reset()
	m.addEntry(entry{role: roleUser, text: text})

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.agent.Send(ctx, text)
	m.turnStart = m.now()
	m.inputStyles() // the prompt dims while a turn runs
	m.refresh()
	return tea.Batch(waitForEvent(m.events), m.spin.Tick)
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
	case agent.Retry:
		// The reply starts over, so what was streamed for the failed attempt is dropped.
		m.streaming = ""
		m.addEntry(entry{role: roleNotice, text: e.Reason})
	case agent.ToolStarted:
		m.commitStreaming() // keep the text the model wrote before the call above its card
		m.addEntry(entry{role: roleTool, callID: e.CallID, name: e.Name, args: e.Arguments, started: m.now()})
	case agent.ToolFinished:
		m.finishTool(e)
	case agent.Usage:
		m.contextTokens = e.InputTokens + e.OutputTokens
	case agent.Done:
		m.commitStreaming()
	case agent.Error:
		// Keep what was already shown; the agent has dropped it from its own history.
		m.commitStreaming()
		if errors.Is(e.Err, context.Canceled) {
			m.addEntry(entry{role: roleNotice, text: "Cancelled"})
		} else {
			m.addEntry(entry{role: roleError, text: e.Err.Error()})
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
		card := &m.entries[i]
		if card.role == roleTool && card.callID == e.CallID {
			card.done, card.failed, card.cancelled, card.output = true, e.Failed, e.Cancelled, e.Output
			card.elapsed = m.now().Sub(card.started)
			card.rendered = m.render(*card)
			return
		}
	}
}

// finishTurn runs once the agent has closed its channel, so the next prompt may start.
func (m *Model) finishTurn() {
	m.cancel()
	m.events, m.cancel, m.cancelling = nil, nil, false
	m.inputStyles()
}

func (m *Model) addEntry(e entry) {
	e.rendered = m.render(e)
	m.entries = append(m.entries, e)
}

// layout sizes the children for the current window and input height.
func (m *Model) layout() {
	width := m.contentWidth()
	m.input.SetWidth(width - boxFrame)
	inputHeight := m.input.Height() + 2 // plus the box border
	m.help.SetWidth(width - 1)          // the bar drops its last hints instead of overflowing
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(max(1, m.height-headerHeight-inputHeight-helpHeight-m.suggestionHeight()))
}

// rerender re-styles every entry after a resize or a theme change.
func (m *Model) rerender() {
	for i := range m.entries {
		m.entries[i].rendered = m.render(m.entries[i])
	}
	m.refresh()
}

// rerenderRunning redraws the cards still waiting for a result, so their spinner moves.
func (m *Model) rerenderRunning() {
	for i := range m.entries {
		if m.entries[i].role == roleTool && !m.entries[i].done {
			m.entries[i].rendered = m.render(m.entries[i])
		}
	}
	m.refresh()
}

// refresh rebuilds the viewport content, following new output only if the user is at the bottom.
func (m *Model) refresh() {
	var parts []string
	for _, e := range m.entries {
		parts = append(parts, e.rendered)
	}
	if m.streaming != "" {
		parts = append(parts, indent(wrap(sanitize(m.streaming), m.contentWidth()-gutter), gutter))
	}
	if line := m.activityLine(); line != "" {
		parts = append(parts, line)
	}
	content := m.welcomeView()
	if len(parts) > 0 {
		content = strings.Join(parts, "\n\n")
	}

	follow := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if follow {
		m.viewport.GotoBottom()
	}
}

// View draws the header, the transcript, the input box and the help bar.
func (m Model) View() tea.View {
	parts := []string{m.headerView(), m.viewport.View()}
	if list := m.suggestionView(); list != "" {
		parts = append(parts, list)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, append(parts, m.inputView(), m.helpView())...)
	v := tea.NewView(indent(body, margin))
	v.AltScreen = true
	return v
}

// Run starts the interactive UI and returns when the user quits.
func Run(a Agent, info Info) error {
	final, err := tea.NewProgram(New(a, info)).Run()
	// Quitting with a turn still running (second Ctrl-C) must not leave its request open.
	if m, ok := final.(Model); ok && m.cancel != nil {
		m.cancel()
	}
	return err
}
