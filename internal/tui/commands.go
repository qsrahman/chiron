package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

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
		{name: "/reset", description: "Forget the conversation and start over", run: (*Model).resetCommand},
		{name: "/model", usage: "[NAME]", description: "Show the model, or switch to NAME", run: (*Model).modelCommand},
		{name: "/effort", usage: "[LEVEL]", description: "Show the reasoning effort, or set it", args: effortLevels, run: (*Model).effortCommand},
		{name: "/tools", description: "List the tools and which of them ask before they run", run: (*Model).toolsCommand},
		{name: "/undo", description: "Remove the last exchange from the conversation", run: (*Model).undoCommand},
		{name: "/retry", description: "Send the last prompt again", run: (*Model).retryCommand},
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
	if m.events != nil {
		// Nothing is queued: a queued /reset or /model could apply a change the user no longer wants.
		m.hint = "A command cannot run during a turn: wait for it to finish, or press ctrl+c to cancel"
		return nil, true
	}
	m.input.Reset()
	if !known {
		m.say("Unknown command: " + word + ". Type /help to list commands.")
		return nil, true
	}
	cmd, err := c.run(m, arg)
	if err != nil {
		m.addEntry(entry{role: roleError, text: "Command error: " + err.Error()})
	}
	m.refresh()
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
	for _, b := range []key.Binding{m.keys.send, m.keys.newline, m.keys.scroll, interrupt, m.keys.eof} {
		rows = append(rows, [2]string{b.Help().Key, b.Help().Desc})
	}
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

func (m *Model) contextCommand(string) (tea.Cmd, error) {
	est := m.agent.ContextEstimate()
	rows := [][2]string{
		{"instructions", "~" + count(est.Instructions) + " tokens"},
		{fmt.Sprintf("tools (%d)", est.ToolCount), "~" + count(est.Tools) + " tokens"},
		{"history", "~" + count(est.History) + " tokens"},
		{"total", "~" + count(est.Total()) + " tokens"},
	}
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

// say adds a dim line to the transcript. It is not part of the conversation the model sees.
func (m *Model) say(text string) { m.addEntry(entry{role: roleInfo, text: text}) }

// sayTable adds aligned label and text rows to the transcript.
func (m *Model) sayTable(rows [][2]string) { m.addEntry(entry{role: roleInfo, rows: rows}) }
