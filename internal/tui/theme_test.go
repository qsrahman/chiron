package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	gstyles "charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

func brightness(c color.Color) uint32 {
	r, g, b, _ := c.RGBA()
	return (r + g + b) / 3
}

func TestBandIsDerivedFromTheTerminalBackground(t *testing.T) {
	darkBG := color.RGBA{R: 30, G: 30, B: 46, A: 255}
	if band := newTheme(true, darkBG).band; brightness(band) <= brightness(darkBG) {
		t.Error("on a dark terminal the band should be lighter than the background")
	}
	if band := newTheme(false, color.White).band; brightness(band) >= brightness(color.White) {
		t.Error("on a light terminal the band should be darker than the background")
	}
	// Without a reported background there is still a usable band.
	for _, dark := range []bool{true, false} {
		if newTheme(dark, nil).band == nil {
			t.Errorf("dark=%v: no fallback band", dark)
		}
	}
}

func TestMarkdownStyleDoesNotChangeGlamourDefaults(t *testing.T) {
	heading, h1 := *gstyles.DarkStyleConfig.Heading.Color, *gstyles.DarkStyleConfig.H1.BackgroundColor
	markdownStyle(newTheme(true, nil))
	if *gstyles.DarkStyleConfig.Heading.Color != heading || *gstyles.DarkStyleConfig.H1.BackgroundColor != h1 {
		t.Error("building our style changed glamour's package-level default style")
	}
}

func TestThemeSwitchChangesColours(t *testing.T) {
	events := []agent.Event{
		agent.ToolStarted{CallID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		agent.ToolFinished{CallID: "c1", Name: "read_file", Output: "line"},
		agent.Done{},
	}
	card := func(bg color.Color) string {
		m := sized(&fakeAgent{events: events}, 80, 24)
		m, _ = update(m, tea.BackgroundColorMsg{Color: bg})
		m, cmd := typeAndEnter(t, m, "x")
		return lastEntry(pump(t, m, cmd)).rendered
	}

	dark, light := card(color.Black), card(color.White)
	if dark == light {
		t.Error("the light and dark themes render the same card")
	}
	// Colour only comes from the theme: no hard-coded terminal palette indexes (e.g. "\x1b[31m").
	for name, out := range map[string]string{"dark": dark, "light": light} {
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("%s card has no styling at all", name)
		}
	}
}

func TestUserMessagesCarryTheBrandColour(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	e := entry{role: roleUser, text: "hello"}
	// 167;139;250 is the dark theme's primary colour (#A78BFA).
	if got := m.render(e); !strings.Contains(got, "38;2;167;139;250") {
		t.Errorf("the user bar does not use the primary colour: %q", got)
	}
}

// Every item is separated from the next by exactly one blank row, so no entry may start or end with one.
func TestEntriesHaveNoLeadingOrTrailingBlankRows(t *testing.T) {
	m := conversation(t, 100, 40)
	m.entries = append(m.entries, entry{role: roleAssistant, text: "text before code\n\n```go\nfunc main() {}\n```\n"})
	m.rerender()

	for i, e := range m.entries {
		lines := strings.Split(e.rendered, "\n")
		for _, line := range []string{lines[0], lines[len(lines)-1]} {
			if strings.TrimSpace(ansi.Strip(line)) == "" {
				t.Errorf("entry %d (role %d) starts or ends with a blank row:\n%q", i, e.role, e.rendered)
			}
		}
	}
}

func TestHeadingsHaveNoMarkdownMarkers(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	for _, md := range []string{"# One", "## Two", "### Three", "#### Four"} {
		out := ansi.Strip(m.markdownText(md, 80))
		if strings.Contains(out, "#") {
			t.Errorf("%q renders with its markers: %q", md, out)
		}
	}
}

func TestUserMessageIsAShadedBlock(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	rendered := m.render(entry{role: roleUser, text: "a question\nover two lines"})
	// 35;35;41 is the dark theme's fallback band (#232329): the block must be shaded on every row.
	for i, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "48;2;35;35;41") {
			t.Errorf("row %d of the user message is not shaded: %q", i, line)
		}
		if w := ansi.StringWidth(line); w != m.contentWidth() {
			t.Errorf("row %d is %d cells wide, want the full %d", i, w, m.contentWidth())
		}
	}
}

// Glamour registers its custom code colours under one global name, so the first theme used used to win for the
// whole process. Rendering the same code under both themes, in either order, must give different colours.
func TestCodeColoursFollowTheTheme(t *testing.T) {
	const md = "```go\nfunc main() { fmt.Println(\"hi\") }\n```"
	render := func(dark bool) string {
		m := newTestModel(&fakeAgent{})
		m.applyTheme(newTheme(dark, nil))
		return m.markdownText(md, 80)
	}

	darkFirst, lightAfter := render(true), render(false)
	if darkFirst == lightAfter {
		t.Error("the light theme renders code with the dark theme's colours")
	}
	if render(true) != darkFirst || render(false) != lightAfter {
		t.Error("switching themes back and forth changed the code colours")
	}
}

// The title of an answer is not a badge, and inline code is a colour, not a padded band that breaks where it wraps.
func TestHeadingsAndInlineCodeHaveNoBackground(t *testing.T) {
	for _, dark := range []bool{true, false} {
		m := sized(&fakeAgent{}, 80, 24)
		m.applyTheme(newTheme(dark, nil))

		out := m.markdownText("# chiron\n\n## Packages\n\nUse `go build` now.\n", 76)

		if strings.Contains(out, "\x1b[48;") || strings.Contains(out, ";48;") {
			t.Errorf("dark=%v: a heading or inline code has a background: %q", dark, out)
		}
		if strings.Contains(out, " ") || strings.Contains(ansi.Strip(out), " chiron ") {
			t.Errorf("dark=%v: a heading or inline code is padded: %q", dark, ansi.Strip(out))
		}
		if !strings.Contains(out, "\x1b[38;") {
			t.Errorf("dark=%v: headings and code lost their colour", dark)
		}
	}
}
