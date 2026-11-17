package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	gstyles "charm.land/glamour/v2/styles"

	"github.com/qsrahman/desk-agent/internal/agent"
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
		m := sized(&fakeSender{events: events}, 80, 24)
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
	m := newTestModel(&fakeSender{})
	e := entry{role: roleUser, text: "hello"}
	// 167;139;250 is the dark theme's primary colour (#A78BFA).
	if got := m.render(e); !strings.Contains(got, "38;2;167;139;250") {
		t.Errorf("the user bar does not use the primary colour: %q", got)
	}
}
