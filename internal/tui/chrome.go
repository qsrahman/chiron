package tui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
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

	brand := m.sty.brand.Render("chiron")
	where := "" // the directory and the branch, which give way before the name does
	if dir := shortenHome(m.info.WorkDir); dir != "" {
		where += "  " + m.sty.meta.Render(dir)
	}
	if m.info.Branch != "" {
		where += " " + m.sty.toolMeta.Render("on "+m.info.Branch)
	}

	// In a narrow terminal the right side gives up what matters least, in this order: the servers, the token count.
	// The name of the program and the model stay.
	model := m.sty.meta.Render(sanitize(m.agent.Settings().Model))
	status := ""
	switch {
	case m.starting:
		status = m.sty.toolMeta.Render(cmp.Or(m.startLabel, "connecting…") + " · ")
	case m.status != "":
		status = m.sty.toolMeta.Render(m.status + " · ")
	}
	usage := m.usageStyle()
	candidates := []string{
		status + model + usage.Render(m.usageText()),
		model + usage.Render(m.usageText()),
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

// contextBudget is the size a request may have (max_context_tokens), which the header's share is counted against.
func (m *Model) contextBudget() int64 { return int64(m.agent.Settings().MaxContextTokens) }

// usageStyle is the muted style of the usage figure, turning amber when the context is three quarters of the budget and
// red when it is nine tenths: the point where a long conversation needs /compact, or the model starts to lose the start.
func (m *Model) usageStyle() lipgloss.Style {
	budget := m.contextBudget()
	if budget <= 0 {
		return m.sty.toolMeta
	}
	switch percent := m.contextTokens * 100 / budget; {
	case percent >= 90:
		return m.sty.iconErr
	case percent >= 75:
		return m.sty.iconRunning
	}
	return m.sty.toolMeta
}

// estimateMark goes in front of a figure that is an estimate and not the server's count.
func (m *Model) estimateMark() string {
	if m.contextEstimated {
		return "~"
	}
	return ""
}

// usageText shows how much context the conversation uses, for the header.
func (m *Model) usageText() string {
	if m.contextTokens == 0 {
		return ""
	}
	return " · " + m.estimateMark() + formatTokens(m.contextTokens) + " tokens"
}

// shareText is the share of the budget the conversation uses, for the right edge of the bottom row; "" when it is not
// known (no budget, or nothing counted yet).
func (m *Model) shareText() string {
	if budget := m.contextBudget(); budget > 0 && m.contextTokens > 0 {
		return fmt.Sprintf("context %s%d%%", m.estimateMark(), m.contextTokens*100/budget)
	}
	return ""
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
	case m.search.open:
		bindings = m.keys.searchHelp()
	case m.picker.open:
		bindings = m.keys.pickerHelp(m.picker.confirm)
	case m.approval.open:
		bindings = m.keys.approvalHelp()
	case m.cards.selected >= 0:
		bindings = m.keys.cardHelp()
	case m.input.Value() == "" && len(m.toolCards()) > 0:
		bindings = append(bindings, m.keys.cardNext)
	}
	bar := m.help.ShortHelpView(bindings)
	width := m.contentWidth() - 1
	// What sits at the right edge matters more than the last key hints, which give way to it: output that is out of
	// sight, and the share of the context budget.
	var right []string
	if below := m.linesBelow(); below > 0 {
		right = append(right, m.sty.iconRunning.Render("↓ "+plural(below, "line")+" below"))
	}
	if share := m.shareText(); share != "" {
		right = append(right, m.usageStyle().Render(share))
	}
	if len(right) > 0 {
		tail := strings.Join(right, "  ")
		bar = ansi.Truncate(bar, max(0, width-ansi.StringWidth(tail)-2), "…")
		bar += strings.Repeat(" ", max(2, width-ansi.StringWidth(bar)-ansi.StringWidth(tail))) + tail
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
	s.Cursor.Shape, s.Cursor.Blink, s.Cursor.Color = tea.CursorBar, true, nil // the terminal's own colour for the cursor
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

// windowTitle is the title of the terminal window or tab: the program and the directory, with a mark in front while it
// works (●), waits for an answer (▲), or, when the window did not have the focus at the time, has finished (✓) or failed
// (✗). The tab of a terminal you are not looking at then says what it wants.
func (m *Model) windowTitle() string {
	title := "chiron"
	if dir := sanitize(filepath.Base(m.info.WorkDir)); dir != "" && dir != "." && dir != "/" {
		title += " · " + dir
	}
	mark := m.attention
	switch {
	case m.approval.open:
		mark = "▲"
	case m.events != nil || m.starting:
		mark = "●"
	}
	if mark == "" {
		return title
	}
	return mark + " " + title
}

// progressBar is the terminal's own progress indicator (a tab or taskbar marker in terminals that have one): running
// while a turn works or the servers start, and yellow while the program waits for an approval.
func (m *Model) progressBar() *tea.ProgressBar {
	switch {
	case m.approval.open:
		return tea.NewProgressBar(tea.ProgressBarWarning, 100)
	case m.events != nil || m.starting:
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	return nil
}

// attend tells a user who is looking at another window that the program wants them: a bell, and a mark in the title.
// It does nothing while the window has the focus.
func (m *Model) attend(mark string) tea.Cmd {
	if m.focused {
		return nil
	}
	m.attention = mark
	return tea.Raw("\a")
}

// cursor is the terminal's own cursor, in the input box. The textarea knows where it is within itself; the box's border
// and padding, the margin and everything above the box move it to its place on the screen. It is hidden while the
// approval dialog has the screen.
func (m *Model) cursor(rowsAbove int) *tea.Cursor {
	c := m.input.Cursor()
	if c == nil || m.approval.open {
		return nil
	}
	c.X += margin + 2    // the margin, then the box's border and padding
	c.Y += rowsAbove + 1 // the rows above, then the box's top border
	return c
}
