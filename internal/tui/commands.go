package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/qsrahman/desk-agent/internal/agent"
	"github.com/qsrahman/desk-agent/internal/config"
)

// command is one slash command. A later phase adds its own by adding an entry to commandList.
type command struct {
	name        string // lower case, with the slash: "/undo"
	usage       string // the argument as shown, "[NAME]"; empty when the command takes none
	description string // one short line
	// args lists the values the argument may take, for the suggestion list; nil when there is no fixed list.
	args func(m *Model) []string
	run  func(m *Model, arg string) (tea.Cmd, error)
}

// commandList returns the commands sorted by name. Aliases are entries of their own.
func commandList() []command {
	list := []command{
		{name: "/help", description: "List the commands and keys", run: (*Model).helpCommand},
		{name: "/exit", description: "Quit", run: quitCommand},
		{name: "/quit", description: "Quit", run: quitCommand},
		{name: "/clear", description: "Clear the screen; the conversation is kept", run: (*Model).clearCommand},
		{name: "/reconnect", usage: "[NAME]", description: "Start MCP servers again: the named one, or those that failed", args: serverNames, run: (*Model).reconnectCommand},
		{name: "/reset", description: "Forget the conversation and start over", run: (*Model).resetCommand},
		{name: "/servers", description: "List the MCP servers and how they are doing", run: (*Model).serversCommand},
		{name: "/model", usage: "[NAME]", description: "Show the model, or switch to NAME", run: (*Model).modelCommand},
		{name: "/effort", usage: "[LEVEL]", description: "Show the reasoning effort, or set it", args: effortLevels, run: (*Model).effortCommand},
		{name: "/tools", description: "List the tools and which of them ask before they run", run: (*Model).toolsCommand},
		{name: "/undo", description: "Remove the last exchange from the conversation", run: (*Model).undoCommand},
		{name: "/retry", description: "Send the last prompt again", run: (*Model).retryCommand},
		{name: "/copy", usage: "[code]", description: "Copy the last answer, or its last code block, to the clipboard", args: func(*Model) []string { return []string{"code"} }, run: (*Model).copyCommand},
		{name: "/compact", usage: "[FOCUS]", description: "Summarize the oldest turns; FOCUS says what to keep", run: (*Model).compactCommand},
		{name: "/sessions", usage: "[all]", description: "List saved sessions (resume with --resume ID)", args: func(*Model) []string { return []string{"all"} }, run: (*Model).sessionsCommand},
		{name: "/summary", description: "Show the summary that stands for the oldest turns", run: (*Model).summaryCommand},
		{name: "/context", description: "Show how much of the context window is used", run: (*Model).contextCommand},
	}
	slices.SortFunc(list, func(a, b command) int { return strings.Compare(a.name, b.name) })
	return list
}

func (m *Model) command(name string) (command, bool) {
	for _, c := range m.commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// dispatch runs line if it is a command and reports whether it was one. A line that starts with "/" and has
// more words ("/usr/bin is missing") is a prompt; a lone word that is no command is reported, because sending a
// typo to the model would cost a request.
func (m *Model) dispatch(line string) (tea.Cmd, bool) {
	if !strings.HasPrefix(line, "/") {
		return nil, false
	}
	word, arg := line, ""
	if i := strings.IndexFunc(line, unicode.IsSpace); i >= 0 {
		word, arg = line[:i], strings.TrimSpace(line[i:])
	}
	c, known := m.command(strings.ToLower(word))
	if !known && arg != "" {
		return nil, false
	}
	// Nothing is queued: a queued /reset or /model could apply a change the user no longer wants.
	if m.events != nil {
		m.hint = "A command cannot run during a turn: wait for it to finish, or press ctrl+c to cancel"
		return nil, true
	}
	if m.starting {
		m.hint = "A command cannot run while the servers are starting: wait a moment"
		return nil, true
	}
	m.input.Reset()
	defer m.refresh() // whatever the command said must reach the screen, an unknown one included
	if !known {
		m.say("Unknown command: " + word + ". Type /help to list commands.")
		return nil, true
	}
	cmd, err := c.run(m, arg)
	if err != nil {
		m.addEntry(entry{role: roleError, text: "Command error: " + err.Error()})
	}
	return cmd, true
}

func quitCommand(*Model, string) (tea.Cmd, error) { return tea.Quit, nil }

func (m *Model) helpCommand(string) (tea.Cmd, error) {
	var rows [][2]string
	for _, c := range m.commands {
		rows = append(rows, [2]string{strings.TrimSpace(c.name + " " + c.usage), c.description})
	}
	m.sayTable(rows)

	interrupt := m.keys.interrupt
	interrupt.SetHelp("ctrl+c", "cancel a turn, clear the input, quit")
	rows = nil
	for _, b := range []key.Binding{m.keys.send, m.keys.newline, m.keys.scroll, m.keys.history, m.keys.search, m.keys.editor, m.keys.suspend, interrupt, m.keys.eof} {
		rows = append(rows, [2]string{b.Help().Key, b.Help().Desc})
	}
	rows = append(rows, [2]string{"mouse wheel", "scroll the transcript (hold Shift to select text)"})
	m.sayTable(rows)
	return nil, nil
}

func (m *Model) clearCommand(string) (tea.Cmd, error) {
	m.entries, m.cards.selected = nil, -1
	return nil, nil
}

func (m *Model) resetCommand(string) (tea.Cmd, error) {
	m.agent.Reset()
	m.contextTokens = 0
	m.say("Conversation reset")
	return nil, nil
}

func (m *Model) modelCommand(arg string) (tea.Cmd, error) {
	if arg == "" {
		m.say("Model: " + m.agent.Settings().Model)
		return nil, nil
	}
	if strings.ContainsFunc(arg, unicode.IsSpace) {
		return nil, errors.New("a model name has no spaces")
	}
	m.agent.SetModel(arg)
	m.contextTokens = 0
	m.say("Model set to " + arg)
	return nil, nil
}

func effortLevels(*Model) []string { return append(slices.Clone(config.Efforts), "default") }

func (m *Model) effortCommand(arg string) (tea.Cmd, error) {
	if arg == "" {
		effort := m.agent.Settings().ReasoningEffort
		if effort == "" {
			effort = "model default"
		}
		m.say("Reasoning effort: " + effort)
		return nil, nil
	}
	level := strings.ToLower(arg)
	switch {
	case level == "default":
		m.agent.SetReasoningEffort("")
		m.say("Reasoning effort: model default")
	case slices.Contains(config.Efforts, level):
		m.agent.SetReasoningEffort(level)
		m.say("Reasoning effort: " + level)
	default:
		return nil, fmt.Errorf("unknown effort %q: use one of %s", arg, strings.Join(effortLevels(m), ", "))
	}
	return nil, nil
}

func (m *Model) undoCommand(string) (tea.Cmd, error) {
	if !m.agent.Undo() {
		m.say("Nothing to undo")
		return nil, nil
	}
	m.contextTokens = 0
	m.say("Last exchange removed")
	return nil, nil
}

func (m *Model) retryCommand(string) (tea.Cmd, error) {
	prompt, ok := m.agent.Rewind()
	if !ok {
		m.say("Nothing to retry")
		return nil, nil
	}
	m.contextTokens = 0
	return m.startTurn(prompt), nil
}

func (m *Model) toolsCommand(string) (tea.Cmd, error) {
	l := m.agent.ToolList()
	if len(l.Free)+len(l.Allowed)+len(l.Ask) == 0 {
		m.say("Tools: none")
		return nil, nil
	}
	if m.agent.Settings().AutoApprove {
		m.say("Every call is allowed without asking (--yes)")
	}
	var rows [][2]string
	for _, group := range []struct {
		label string
		names []string
	}{{"run without asking", l.Free}, {"allowed this session", l.Allowed}, {"ask first", l.Ask}} {
		if len(group.names) > 0 {
			rows = append(rows, [2]string{group.label, strings.Join(group.names, ", ")})
		}
	}
	m.sayTable(rows)
	return nil, nil
}

// copyCommand puts the last answer, or the last code block of it, on the clipboard through the terminal. With the mouse
// reported to the program the terminal's own selection needs Shift, and this needs nothing. Control characters the
// model wrote are removed first: the text will be pasted somewhere.
func (m *Model) copyCommand(arg string) (tea.Cmd, error) {
	var answer string
	for i := len(m.entries) - 1; i >= 0 && answer == ""; i-- {
		if m.entries[i].role == roleAssistant {
			answer = sanitize(m.entries[i].text)
		}
	}
	if strings.TrimSpace(answer) == "" {
		m.say("Nothing to copy")
		return nil, nil
	}
	what, text := "the last answer", answer
	switch strings.ToLower(arg) {
	case "":
	case "code":
		what, text = "the last code block", lastCodeBlock(answer)
		if text == "" {
			m.say("The last answer has no code block")
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("unknown argument %q: say code for the last code block, or nothing for the whole answer", arg)
	}
	m.say(fmt.Sprintf("Copied %s (%s) to the clipboard", what, plural(len([]rune(text)), "character")))
	return tea.SetClipboard(text), nil
}

// lastCodeBlock is the inside of the last fenced code block of the markdown, or "".
func lastCodeBlock(markdown string) string {
	block := ""
	for _, seg := range splitFences(markdown) {
		if seg.code {
			block = seg.text
		}
	}
	return block
}

// serverNames lists the configured servers, for completing /reconnect.
func serverNames(m *Model) []string {
	if m.info.Servers == nil {
		return nil
	}
	var names []string
	for _, s := range m.info.Servers.List() {
		names = append(names, s.Name)
	}
	return names
}

func (m *Model) serversCommand(string) (tea.Cmd, error) {
	if m.info.Servers == nil {
		m.say("No MCP servers are configured")
		return nil, nil
	}
	var rows [][2]string
	for _, s := range m.info.Servers.List() {
		rows = append(rows, [2]string{s.Name, s.Describe()})
	}
	m.sayTable(rows)
	return nil, nil
}

// Describe is one line about a server: how it is doing, what it offers and what that costs.
func (s ServerInfo) Describe() string {
	var parts []string
	switch {
	case s.Err != "":
		parts = append(parts, "failed: "+s.Err)
	case s.Lost:
		parts = append(parts, "connection lost, the next call starts it again")
	default:
		offered := plural(s.Tools, "tool")
		if s.Hidden > 0 {
			offered += fmt.Sprintf(" (%d hidden)", s.Hidden)
		}
		parts = append(parts, "connected", offered, "~"+count(s.Tokens)+" tokens of definitions")
	}
	if s.Restarts > 0 {
		parts = append(parts, "started again "+plural(s.Restarts, "time"))
	}
	if s.LogPath != "" {
		parts = append(parts, "log "+s.LogPath)
	}
	return strings.Join(parts, " · ")
}

// reconnectCommand starts the servers again in the background. Prompts wait while it runs, as at startup, and the
// result is shown the same way.
func (m *Model) reconnectCommand(name string) (tea.Cmd, error) {
	if m.info.Servers == nil {
		m.say("No MCP servers are configured")
		return nil, nil
	}
	servers := m.info.Servers
	m.starting = true
	return func() tea.Msg { return startupMsg{servers.Reconnect(name)} }, nil
}

func (m *Model) contextCommand(string) (tea.Cmd, error) {
	est := m.agent.ContextEstimate()
	rows := [][2]string{
		{"instructions", "~" + count(est.Instructions) + " tokens"},
		{fmt.Sprintf("tools (%d)", est.ToolCount), "~" + count(est.Tools) + " tokens"},
	}
	history := "history"
	if est.Summary > 0 {
		rows = append(rows, [2]string{"summary", "~" + count(est.Summary) + " tokens, replaces the oldest turns"})
		history = "history after it"
	}
	rows = append(rows,
		[2]string{history, "~" + count(est.History) + " tokens"},
		[2]string{"total", "~" + count(est.Total()) + " tokens"},
		[2]string{"budget", fmt.Sprintf("%s tokens (%d%% used)", count(est.Budget), est.Total()*100/max(est.Budget, 1))},
	)
	if est.LastInput > 0 {
		rows = append(rows, [2]string{"last request", count(int(est.LastInput)) + " tokens, counted by the server"})
	} else {
		rows = append(rows, [2]string{"last request", "not known yet"})
	}
	if m.info.ContextWindow > 0 {
		rows = append(rows, [2]string{"window", count(m.info.ContextWindow) + " tokens"})
	}
	m.sayTable(rows)
	return nil, nil
}

// compactCommand summarizes the oldest turns. The model answers in the background; prompts typed meanwhile wait, as
// they do while the servers start.
func (m *Model) compactCommand(focus string) (tea.Cmd, error) {
	if !m.agent.HasConversation() {
		m.say("Nothing to summarize yet")
		return nil, nil
	}
	m.starting, m.startLabel = true, "summarizing…"
	a := m.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		c, err := a.Compact(ctx, focus)
		return compactedMsg{c, err}
	}, nil
}

func (m *Model) sessionsCommand(arg string) (tea.Cmd, error) {
	if arg != "" && arg != "all" {
		return nil, fmt.Errorf("unknown argument %q: use /sessions or /sessions all", arg)
	}
	list, err := m.agent.ListSessions(arg == "all")
	if err != nil {
		m.say(err.Error())
		return nil, nil
	}
	if len(list) == 0 {
		note := "No saved sessions yet"
		if arg != "all" {
			note += " for this directory (/sessions all lists every directory)"
		}
		m.say(note)
		return nil, nil
	}
	var rows [][2]string
	for _, s := range list {
		label := s.ID
		if s.Current {
			label += " (this one)"
		}
		detail := fmt.Sprintf("%s · %s · %s", s.Updated.Local().Format("2006-01-02 15:04"), plural(s.Turns, "turn"), sanitize(singleLine(s.Prompt)))
		if arg == "all" {
			detail = sanitize(s.Dir) + " · " + detail
		}
		rows = append(rows, [2]string{label, detail})
	}
	m.sayTable(rows)
	if len(list) == agent.SessionListLimit {
		m.say(fmt.Sprintf("Showing the newest %d", agent.SessionListLimit))
	}
	return nil, nil
}

func (m *Model) summaryCommand(string) (tea.Cmd, error) {
	summary := m.agent.Summary()
	if summary == "" {
		m.say("No summary yet: /compact makes one")
		return nil, nil
	}
	m.say("Summary of the oldest turns:\n" + sanitize(summary))
	return nil, nil
}

// say adds a dim line to the transcript. It is not part of the conversation the model sees.
func (m *Model) say(text string) { m.addEntry(entry{role: roleInfo, text: text}) }

// sayTable adds aligned label and text rows to the transcript.
func (m *Model) sayTable(rows [][2]string) { m.addEntry(entry{role: roleInfo, rows: rows}) }
