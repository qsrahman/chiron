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
	headerHeight = 2 // title row and a rule
	helpHeight   = 1
	boxFrame     = 4 // columns taken by the input box: border and padding on both sides
)

// headerView is the title row (name, directory and branch on the left, model and context on the right)
// followed by a rule.
func (m *Model) headerView() string {
	width := m.contentWidth()

	brand := m.sty.brand.Render("desk-agent")
	where := "" // the directory and the branch, which give way before the name does
	if dir := shortenHome(m.info.WorkDir); dir != "" {
		where += "  " + m.sty.meta.Render(dir)
	}
	if m.info.Branch != "" {
		where += " " + m.sty.toolMeta.Render("on "+m.info.Branch)
	}

	// In a narrow terminal the right side gives up what matters least, in this order: the servers, the share of the
	// context window, the token count. The name of the program and the model stay.
	model := m.sty.meta.Render(sanitize(m.agent.Settings().Model))
	status := ""
	switch {
	case m.starting:
		status = m.sty.toolMeta.Render("connecting… · ")
	case m.status != "":
		status = m.sty.toolMeta.Render(m.status + " · ")
	}
	candidates := []string{
		status + model + m.sty.toolMeta.Render(m.usageText(true)),
		model + m.sty.toolMeta.Render(m.usageText(true)),
		model + m.sty.toolMeta.Render(m.usageText(false)),
		model,
	}
	room := width - lipgloss.Width(brand) - 2
	right := candidates[len(candidates)-1]
	for _, c := range candidates {
		if lipgloss.Width(c) <= room {
			right = c
			break
		}
	}
	right = ansi.Truncate(right, max(0, room), "…")
	left := brand + ansi.Truncate(where, max(0, width-lipgloss.Width(right)-2-lipgloss.Width(brand)), "…")
	gap := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))
	return left + gap + right + "\n" + m.sty.rule.Render(strings.Repeat("─", width))
}

// usageText shows how much context the conversation uses, with a percentage when the window is known and asked for.
func (m *Model) usageText(percent bool) string {
	if m.contextTokens == 0 {
		return ""
	}
	text := " · " + formatTokens(m.contextTokens) + " tokens"
	if percent && m.info.ContextWindow > 0 {
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
	case m.cards.selected >= 0:
		bindings = m.keys.cardHelp()
	case m.input.Value() == "" && len(m.toolCards()) > 0:
		bindings = append(bindings, m.keys.cardNext)
	}
	bar := m.help.ShortHelpView(bindings)
	width := m.contentWidth() - 1
	if below := m.linesBelow(); below > 0 {
		// Output that is out of sight matters more than the last key hints, which give way to it.
		hint := m.sty.iconRunning.Render("↓ " + plural(below, "line") + " below")
		bar = ansi.Truncate(bar, max(0, width-ansi.StringWidth(hint)-2), "…")
		bar += strings.Repeat(" ", max(2, width-ansi.StringWidth(bar)-ansi.StringWidth(hint))) + hint
	}
	// The help component still adds an item when not even its ellipsis fits, so cut the line ourselves.
	return indent(ansi.Truncate(bar, width, "…"), 1)
}

// linesBelow is how many lines of the transcript are below the window, when it has been scrolled up.
func (m *Model) linesBelow() int {
	return max(0, m.viewport.TotalLineCount()-m.viewport.YOffset()-m.viewport.Height())
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
	m.input.SetPromptFunc(promptWidth, func(info textarea.PromptInfo) string {
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
