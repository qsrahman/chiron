package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Fixed heights of the parts around the transcript.
const (
	headerHeight = 3 // title row, a rule and a blank row of breathing space
	helpHeight   = 1
	boxFrame     = 4 // columns taken by the input box: border and padding on both sides
)

// headerView is the title row (name, directory and branch on the left, model and context on the right)
// followed by a rule.
func (m *Model) headerView() string {
	width := m.contentWidth()

	left := m.sty.brand.Render("desk-agent")
	if dir := shortenHome(m.info.WorkDir); dir != "" {
		left += "  " + m.sty.meta.Render(dir)
	}
	if m.info.Branch != "" {
		left += " " + m.sty.toolMeta.Render("on "+m.info.Branch)
	}

	right := m.sty.meta.Render(sanitize(m.agent.Settings().Model)) + m.sty.toolMeta.Render(m.usageText())

	// The left side gives way first when the terminal is narrow.
	right = ansi.Truncate(right, width, "…")
	left = ansi.Truncate(left, max(0, width-lipgloss.Width(right)-2), "…")
	gap := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))
	return left + gap + right + "\n" + m.sty.rule.Render(strings.Repeat("─", width)) + "\n"
}

// usageText shows how much context the conversation uses, with a percentage when the window is known.
func (m *Model) usageText() string {
	if m.contextTokens == 0 {
		return ""
	}
	text := " · " + formatTokens(m.contextTokens) + " tokens"
	if m.info.ContextWindow > 0 {
		text += fmt.Sprintf(" (%d%%)", m.contextTokens*100/int64(m.info.ContextWindow))
	}
	return text
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// shortenHome writes the home directory as "~".
func shortenHome(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(dir, home) {
		return dir
	}
	return "~" + strings.TrimPrefix(dir, home)
}

// welcomeView is shown until the first message.
func (m *Model) welcomeView() string {
	title := m.sty.welcomeTitle.Render("Welcome")
	if m.info.Version != "" {
		title += " " + m.sty.toolMeta.Render(m.info.Version)
	}
	width := m.contentWidth() - gutter
	text := m.sty.welcomeText.Width(width)
	return indent(strings.Join([]string{
		title,
		text.Render("An AI coding assistant for your terminal."),
		"",
		text.Render("Describe what you want to do, or ask about the code in this directory."),
		m.sty.toolMeta.Width(width).Render("Enter sends. / lists commands. Shift+Enter: new line. Ctrl+C: cancel or quit."),
	}, "\n"), gutter)
}

// activityLine shows what the agent is doing while a turn runs: a spinner and the time since it started.
func (m *Model) activityLine() string {
	if m.events == nil {
		return ""
	}
	if m.cancelling {
		return indent(m.sty.iconOff.Render("●")+" "+m.sty.toolMeta.Render("Cancelling… press ctrl+c again to quit"), gutter)
	}
	text := "Working…"
	if d := m.now().Sub(m.turnStart); d >= time.Second {
		text += " " + d.Round(time.Second).String()
	}
	return indent(m.sty.iconRunning.Render(m.spin.View())+" "+m.sty.toolMeta.Render(text), gutter)
}

// inputView is the framed input box. The frame is muted while a turn runs: you can keep typing a draft,
// but Enter will not send it yet.
func (m *Model) inputView() string {
	box := m.sty.inputBox
	if m.events != nil {
		box = m.sty.inputBoxBusy
	}
	return box.Width(m.contentWidth()).Render(m.input.View())
}

func (m *Model) helpView() string {
	if m.hint != "" {
		return indent(ansi.Truncate(m.sty.iconRunning.Render(m.hint), m.contentWidth()-1, "…"), 1)
	}
	bindings := m.keys.helpBindings(m.events != nil, m.cancelling)
	if list, _ := m.suggestions(); len(list) > 0 {
		bindings = m.keys.suggestionHelp()
	}
	switch {
	case m.approval.open:
		bindings = m.keys.approvalHelp()
	case m.card >= 0:
		bindings = m.keys.cardHelp()
	case m.input.Value() == "" && len(m.toolCards()) > 0:
		bindings = append(bindings, m.keys.cardNext)
	}
	bar := m.help.ShortHelpView(bindings)
	// The help component still adds an item when not even its ellipsis fits, so cut the line ourselves.
	return indent(ansi.Truncate(bar, m.contentWidth()-1, "…"), 1)
}

// inputStyles adapts the textarea to the theme: no highlighted cursor line, a muted placeholder, and a
// prompt only on the first row.
func (m *Model) inputStyles() {
	s := textarea.DefaultStyles(m.th.dark)
	for _, state := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		state.CursorLine = lipgloss.NewStyle()
		state.Placeholder = lipgloss.NewStyle().Foreground(m.th.muted)
		state.EndOfBuffer = lipgloss.NewStyle()
	}
	m.input.SetStyles(s)
	m.input.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		switch {
		case info.LineNumber > 0:
			return "  "
		case m.events != nil:
			return m.sty.promptBusy.Render("❯ ")
		default:
			return m.sty.prompt.Render("❯ ")
		}
	})
}
