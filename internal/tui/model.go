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

	"github.com/qsrahman/chiron/internal/agent"
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
	Compact(ctx context.Context, focus string) (agent.Compaction, error)
	Summary() string
	Messages() []agent.Message
	ListSessions() ([]agent.SessionInfo, error)
	ResumeSession(id string) error
	DeleteSession(id string) error
	SessionID() string
	ToolTitle(name string) string
	ToolList() agent.ToolList
}

// Info is what the UI shows about the session. Only the fields it displays are here.
type Info struct {
	WorkDir string
	Branch  string // empty outside a git repository

	// Startup is work that finishes after the window has opened, such as connecting to MCP servers. It runs once, in
	// the background. Prompts typed meanwhile wait, so that the model sees the tools it sets up.
	Startup func() Startup

	// Servers is what /servers and /reconnect use; nil when no MCP server is configured.
	Servers Servers

	// HistoryPath is the file the sent prompts are kept in; empty keeps them for this run only.
	HistoryPath string
}

// ServerInfo says how one MCP server is doing.
type ServerInfo struct {
	Name     string
	Err      string // why it did not start; empty if it did
	Lost     bool   // it started, but its connection was lost
	Tools    int    // how many tools it offers the model
	Hidden   int    // how many more it has, kept from the model by the allow-list
	Tokens   int    // the estimated size of the definitions of the tools it offers
	Restarts int    // how often a call started it again
	LogPath  string // where a local server's error output goes; empty for a remote one
}

// Servers is what the commands need of the MCP servers.
type Servers interface {
	List() []ServerInfo
	// Reconnect starts servers afresh, the named one or with no name every one that failed or lost its connection,
	// and says how it went. It blocks until they have answered, and is called while no turn runs.
	Reconnect(name string) Startup
}

// Startup is what a Startup function reports.
type Startup struct {
	Servers [][2]string // one row per server: its name and how it started
	Notes   []string    // anything else worth saying
	Status  string      // a few words for the header; empty for none
}

// startupMsg carries the result of the Startup function into Update.
type startupMsg struct{ Startup }

// compactedMsg is the end of /compact.
type compactedMsg struct {
	result agent.Compaction
	err    error
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

	entries    []entry
	streaming  string // assistant text of the turn in progress
	streamView string // streaming as markdown: redrawn on each spinner tick, since rendering on every token is too slow

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

	hist     *promptHistory // the prompts sent so far, for Up and Down and Ctrl-R
	recall   recallState    // where Up and Down have got to
	search   searchState    // Ctrl-R
	picker   pickerState    // /sessions
	suggest  suggestState   // the list that opens while a command is typed
	approval approval       // the modal that asks about a tool call
	queue    queueState     // prompts typed during a turn
	cards    cardState      // the tool card chosen with Tab

	contextTokens int64 // size of the conversation after the latest response, 0 until known

	// starting is set while the Startup function runs; status is what it left for the header.
	starting bool
	// startLabel says what the header shows while starting is set, when it is not the servers connecting.
	startLabel string
	status     string

	// focused is whether the terminal window has the focus (true until the terminal says otherwise, which it does
	// only if it reports focus). attention marks the title when something happened while it did not.
	focused   bool
	attention string

	width, height int
}

// The empty input says what to do and where the commands are; in a narrow terminal the shorter form is used, since a
// hint cut off in the middle of a phrase says less than a short one.
const (
	placeholder      = "Ask anything, or /help for commands..."
	placeholderShort = "Ask anything, or /help..."
)

// promptWidth is the columns the prompt "❯ " takes in front of the text.
const promptWidth = 2

func placeholderFor(inputWidth int) string {
	if inputWidth-promptWidth >= len([]rune(placeholder)) {
		return placeholder
	}
	return placeholderShort
}

// New returns a model that sends prompts to a.
func New(a Agent, info Info) Model {
	keys := newKeyMap()
	input := textarea.New()
	input.Placeholder = placeholder
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 8
	input.KeyMap.InsertNewline = keys.newline
	input.SetVirtualCursor(false) // the terminal's own cursor is drawn at View, so it follows the user's cursor settings
	input.Focus()

	// Whatever the session shows may come from the environment (a directory name, say): clean it once here.
	info.WorkDir, info.Branch = sanitize(info.WorkDir), sanitize(info.Branch)

	m := Model{
		agent:    a,
		info:     info,
		commands: commandList(),
		now:      time.Now,
		viewport: viewport.New(),
		input:    input,
		keys:     keys,
		cards:    cardState{selected: -1},
		hist:     loadPromptHistory(info.HistoryPath),
		starting: info.Startup != nil,
		focused:  true,
		width:    80,
		height:   24,
	}
	m.applyTheme(newTheme(true, nil)) // replaced when the terminal reports its background
	m.restore()
	m.layout()
	m.refresh()
	return m
}

// restore shows a resumed conversation again: its messages, and its tool calls with what they returned.
func (m *Model) restore() {
	messages := m.agent.Messages()
	if len(messages) == 0 && m.agent.Summary() == "" {
		return
	}
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			m.addEntry(entry{role: roleUser, text: msg.Text})
		case "assistant":
			m.addEntry(entry{role: roleAssistant, text: msg.Text})
		default:
			m.addEntry(entry{role: roleTool, callID: msg.CallID, name: msg.Name, title: msg.Title, args: msg.Arguments, output: msg.Output, done: true, outcome: msg.Outcome})
		}
	}
	note := "Resumed session " + m.agent.SessionID()
	if m.agent.Summary() != "" {
		note += "; the oldest turns are summarized (/summary)"
	}
	m.addEntry(entry{role: roleNotice, text: note})
}

// retitleCards gives the cards of a resumed conversation the names of tools that have connected since.
func (m *Model) retitleCards() {
	for i := range m.entries {
		if e := &m.entries[i]; e.role == roleTool && e.title == "" {
			if title := m.agent.ToolTitle(e.name); title != "" {
				e.title = title
				e.rendered = m.render(*e)
			}
		}
	}
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
	if m.info.Startup == nil {
		return tea.RequestBackgroundColor
	}
	run := m.info.Startup
	return tea.Batch(tea.RequestBackgroundColor, func() tea.Msg { return startupMsg{run()} })
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
		m.layoutApproval()
		m.rerender()
		return m, nil

	case tea.BackgroundColorMsg:
		m.applyTheme(newTheme(msg.IsDark(), color.Color(msg.Color)))
		m.rerender()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.KeyboardEnhancementsMsg:
		// Shift+Enter reaches the program as a key of its own only in a terminal that supports it; in any other it is Enter,
		// which sends. Until the terminal says so, the help names the key that works everywhere.
		if msg.SupportsKeyDisambiguation() {
			m.keys.newline.SetHelp("shift+enter", "new line")
		}
		return m, nil

	case tea.FocusMsg:
		m.focused, m.attention = true, ""
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			m.click(msg.X, msg.Y)
		}
		return m, nil

	case tea.MouseWheelMsg:
		// The wheel scrolls what has the focus: the details of a call being asked about, or else the transcript.
		if m.approval.open {
			m.approval.details, _ = m.approval.details.Update(msg)
		} else {
			m.viewport, _ = m.viewport.Update(msg)
		}
		return m, nil

	case eventMsg:
		m.handleEvent(msg.event)
		next := waitForEvent(m.events)
		if _, asks := msg.event.(agent.ApprovalRequested); asks {
			next = tea.Batch(next, m.attend("▲"))
		}
		return m, next

	case startupMsg:
		m.starting, m.status = false, sanitize(msg.Status)
		m.retitleCards() // a resumed conversation was shown before the MCP tools were known
		if len(msg.Servers) > 0 {
			m.sayTable(msg.Servers)
		}
		for _, note := range msg.Notes {
			m.say(note)
		}
		next := m.nextQueued() // a prompt typed while the servers started goes now
		m.refresh()
		return m, next

	case editorDoneMsg:
		m.finishEditor(msg)
		return m, nil

	case compactedMsg:
		m.starting, m.startLabel = false, ""
		if msg.err != nil {
			m.addEntry(entry{role: roleError, text: "Could not summarize: " + msg.err.Error()})
		} else {
			m.say(msg.result.String())
		}
		next := m.nextQueued()
		m.refresh()
		return m, next

	case turnClosedMsg:
		m.finishTurn()
		next := m.nextQueued()
		if next == nil { // the run is over: tell a user who looked away
			mark := "✓"
			if m.queue.failed {
				mark = "✗"
			}
			next = m.attend(mark)
		}
		m.refresh()
		return m, next

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
	m.attention = "" // the user is here
	if m.approval.open && !key.Matches(msg, m.keys.interrupt) {
		return m.approvalKey(msg)
	}
	if m.search.open {
		m.searchKey(msg)
		return m, nil
	}
	if m.picker.open {
		m.pickerKey(msg)
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.search):
		m.startSearch()
		return m, nil
	case key.Matches(msg, m.keys.editor):
		return m, m.editInEditor()
	case key.Matches(msg, m.keys.suspend):
		return m, tea.Suspend
	}
	if list, typed := m.suggestions(); len(list) > 0 && m.suggestKey(msg, list, typed) {
		return m, nil
	}
	interrupt, eof := key.Matches(msg, m.keys.interrupt), key.Matches(msg, m.keys.eof)
	if !interrupt && !eof {
		m.quitArmed, m.hint = false, "" // any other key takes the warning back
		if m.input.Value() == "" && m.emptyInputKey(msg) {
			return m, nil
		}
		if m.recalling() && m.recallKey(msg) {
			return m, nil
		}
	}
	switch {
	case interrupt:
		return m.interruptKey(busy)

	case eof:
		if !busy && m.input.Value() == "" {
			return m.confirmQuit("ctrl+d")
		}

	case key.Matches(msg, m.keys.send):
		return m.sendKey(busy)

	case key.Matches(msg, m.keys.scroll):
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.suggest.sel = 0 // the list changed under the selection
	}
	return m, cmd
}

// interruptKey is Ctrl-C: it cancels a running turn (a second press quits), else clears the typed text, else quits.
func (m Model) interruptKey(busy bool) (Model, tea.Cmd) {
	if busy {
		if m.cancelling {
			return m, tea.Quit
		}
		m.cancelling = true
		m.dropQueue() // what the user typed to follow this turn is not wanted once it is cancelled
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
}

// sendKey is Enter: it runs a command, queues the prompt while a turn or the startup is busy, or else sends it.
func (m Model) sendKey(busy bool) (Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		if !busy && !m.starting && len(m.queue.prompts) > 0 { // after an error the queue waits for this
			return m, m.sendQueued()
		}
		return m, nil
	}
	m.hist.add(text)
	if cmd, handled := m.dispatch(text); handled {
		return m, cmd
	}
	if busy || m.starting {
		m.enqueue(text)
		return m, nil
	}
	return m.submit(text)
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
	m.input.Reset()
	cmd := m.startTurn(text)
	return m, cmd
}

// startTurn shows text as the user's prompt and sends it to the agent. It leaves the input alone: a prompt
// taken from the queue must not wipe what the user is typing.
func (m *Model) startTurn(text string) tea.Cmd {
	m.addEntry(entry{role: roleUser, text: text})
	m.queue.failed = false

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
		if m.streamView == "" {
			m.renderStreaming() // the first text shows as markdown at once, not as plain text for a moment
		}
	case agent.Notice:
		m.addEntry(entry{role: roleNotice, text: e.Text})
	case agent.Retry:
		// The reply starts over, so what was streamed for the failed attempt is dropped.
		m.streaming, m.streamView = "", ""
		m.addEntry(entry{role: roleNotice, text: e.Reason})
	case agent.ToolStarted:
		m.commitStreaming() // keep the text the model wrote before the call above its card
		m.addEntry(entry{role: roleTool, callID: e.CallID, name: e.Name, title: e.Title, args: e.Arguments, started: m.now()})
	case agent.ToolFinished:
		m.finishTool(e)
	case agent.ApprovalRequested:
		m.openApproval(e)
	case agent.Usage:
		m.contextTokens = e.InputTokens + e.OutputTokens
	case agent.Done:
		m.commitStreaming()
	case agent.Error:
		// Keep what was already shown; the agent has dropped it from its own history.
		m.commitStreaming()
		if errors.Is(e.Err, context.Canceled) {
			m.addEntry(entry{role: roleNotice, text: "Cancelled"})
			if m.queue.dropped > 0 {
				m.say("Dropped " + plural(m.queue.dropped, "queued prompt"))
				m.queue.dropped = 0
			}
		} else {
			m.queue.failed = true
			m.addEntry(entry{role: roleError, text: e.Err.Error()})
		}
	}
	m.refresh()
}

func (m *Model) commitStreaming() {
	if m.streaming != "" {
		m.addEntry(entry{role: roleAssistant, text: m.streaming})
		m.streaming, m.streamView = "", ""
	}
}

// finishTool completes the card started for the same call. Parallel calls can finish in any order.
func (m *Model) finishTool(e agent.ToolFinished) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		card := &m.entries[i]
		if card.role == roleTool && card.callID == e.CallID {
			card.done, card.outcome, card.output = true, e.Outcome, e.Output
			card.elapsed = m.now().Sub(card.started)
			card.rendered = m.render(*card)
			return
		}
	}
}

// finishTurn runs once the agent has closed its channel, so the next prompt may start.
func (m *Model) finishTurn() {
	m.approval = approval{} // a cancelled turn leaves its prompt open
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
	m.input.Placeholder = placeholderFor(m.input.Width())
	inputHeight := m.input.Height() + 2 // plus the box border
	m.help.SetWidth(width - 1)          // the bar drops its last hints instead of overflowing
	m.viewport.SetWidth(width)
	// A window at the bottom of the transcript stays there when its height changes (the suggestion list opens and
	// closes, the queue grows, the terminal is resized): otherwise the next output lands below the fold.
	atBottom := m.viewport.AtBottom()
	m.viewport.SetHeight(max(1, m.height-headerHeight-inputHeight-helpHeight-m.suggestionHeight()-m.queueHeight()-m.searchHeight()-m.pickerHeight()))
	if atBottom {
		m.viewport.GotoBottom()
	}
}

// rerender re-styles every entry after a resize or a theme change.
func (m *Model) rerender() {
	m.renderStreaming()
	for i := range m.entries {
		m.entries[i].rendered = m.render(m.entries[i])
	}
	m.refresh()
}

// rerenderRunning redraws the cards still waiting for a result, so their spinner moves.
func (m *Model) rerenderRunning() {
	m.renderStreaming()
	for i := range m.entries {
		if m.entries[i].role == roleTool && !m.entries[i].done {
			m.entries[i].rendered = m.render(m.entries[i])
		}
	}
	m.refresh()
}

// renderStreaming draws the reply in progress the way it will look when finished.
func (m *Model) renderStreaming() {
	m.streamView = ""
	if m.streaming != "" {
		m.streamView = m.render(entry{role: roleAssistant, text: m.streaming})
	}
}

// refresh rebuilds the viewport content, following new output only if the user is at the bottom.
func (m *Model) refresh() {
	var transcript strings.Builder
	for i, e := range m.entries {
		if i > 0 {
			transcript.WriteString(separator(m.entries[i-1], e))
		}
		transcript.WriteString(e.rendered)
	}
	content := transcript.String()
	for _, tail := range []string{m.streamView, m.activityLine()} {
		if tail == "" {
			continue
		}
		if content != "" {
			content += "\n\n"
		}
		content += tail
	}

	follow := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if follow {
		m.viewport.GotoBottom()
	}
	if m.cards.reveal && m.cards.selected >= 0 {
		m.cards.reveal = false
		m.showCard()
	}
}

// View draws the header, the transcript, the input box and the help bar.
func (m Model) View() tea.View {
	parts := []string{m.headerView(), m.viewport.View()}
	if queue := m.queueView(); queue != "" {
		parts = append(parts, queue)
	}
	if search := m.searchView(); search != "" {
		parts = append(parts, search)
	}
	if picker := m.pickerView(); picker != "" {
		parts = append(parts, picker)
	}
	if list := m.suggestionView(); list != "" {
		parts = append(parts, list)
	}
	above := lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, parts...)) // the rows above the input box
	body := lipgloss.JoinVertical(lipgloss.Left, append(parts, m.inputView(), m.helpView())...)
	screen := indent(body, margin)
	if m.approval.open {
		screen = m.overlay(screen)
	}
	v := tea.NewView(screen)
	v.AltScreen = true
	v.ReportFocus = true
	v.WindowTitle = m.windowTitle()
	v.ProgressBar = m.progressBar()
	v.Cursor = m.cursor(above)
	v.MouseMode = tea.MouseModeCellMotion // for the wheel; the terminal's own text selection then needs Shift (Option on macOS)
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
