package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// theme is the colour palette. The terminal's own background is never painted; the only backgrounds are
// "bands" (code and tool output) derived from the real background, so the UI fits any terminal theme.
// Body text has no colour of its own and uses the terminal's default foreground.
type theme struct {
	dark bool

	primary color.Color // brand, user messages, focus
	info    color.Color // tool names, headings
	success color.Color
	warning color.Color
	danger  color.Color

	subtle color.Color // secondary text
	muted  color.Color // hints, borders, metadata

	band     color.Color // background of code and tool output
	onAccent color.Color // text on a coloured badge
}

// Band strength: how far the terminal background is moved towards the foreground.
const (
	darkBandShift  = 0.09
	lightBandShift = 0.06
)

// newTheme picks the dark or light palette. bg is the terminal background if the terminal reported
// it; otherwise a neutral band is used.
func newTheme(dark bool, bg color.Color) theme {
	if dark {
		t := theme{
			dark:     true,
			primary:  lipgloss.Color("#A78BFA"),
			info:     lipgloss.Color("#60A5FA"),
			success:  lipgloss.Color("#4ADE80"),
			warning:  lipgloss.Color("#FBBF24"),
			danger:   lipgloss.Color("#F87171"),
			subtle:   lipgloss.Color("#A1A1AA"),
			muted:    lipgloss.Color("#71717A"),
			band:     lipgloss.Color("#232329"),
			onAccent: lipgloss.Color("#18181B"),
		}
		if bg != nil {
			t.band = lipgloss.Lighten(bg, darkBandShift)
		}
		return t
	}
	t := theme{
		primary:  lipgloss.Color("#6D28D9"),
		info:     lipgloss.Color("#2563EB"),
		success:  lipgloss.Color("#15803D"),
		warning:  lipgloss.Color("#B45309"),
		danger:   lipgloss.Color("#DC2626"),
		subtle:   lipgloss.Color("#52525B"),
		muted:    lipgloss.Color("#71717A"),
		band:     lipgloss.Color("#EEEEF2"),
		onAccent: lipgloss.Color("#FFFFFF"),
	}
	if bg != nil {
		t.band = lipgloss.Darken(bg, lightBandShift)
	}
	return t
}

// styles are the lipgloss styles built from a theme.
type styles struct {
	brand, meta, rule lipgloss.Style

	user, notice, errorText lipgloss.Style
	badge                   lipgloss.Style

	toolName, toolParam, toolMeta         lipgloss.Style
	iconRunning, iconOK, iconErr, iconOff lipgloss.Style
	band, bandHint                        lipgloss.Style

	welcomeTitle, welcomeText lipgloss.Style

	inputBox, inputBoxBusy lipgloss.Style
	prompt, promptBusy     lipgloss.Style
	help                   help.Styles
}

func newStyles(t theme) styles {
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	return styles{
		brand: fg(t.primary).Bold(true),
		meta:  fg(t.subtle),
		rule:  fg(t.muted),

		// A shaded block with a bar in the brand colour sets the user's own words apart from everything else.
		user: lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(t.primary).
			BorderBackground(t.band).
			Background(t.band).
			Padding(0, 1),
		notice:    fg(t.subtle).Italic(true),
		errorText: fg(t.subtle),
		badge:     lipgloss.NewStyle().Padding(0, 1).Bold(true).Background(t.danger).Foreground(t.onAccent),

		toolName:  fg(t.info),
		toolParam: fg(t.subtle),
		toolMeta:  fg(t.muted),

		iconRunning: fg(t.warning),
		iconOK:      fg(t.success),
		iconErr:     fg(t.danger),
		iconOff:     fg(t.muted),

		band:     lipgloss.NewStyle().Background(t.band).Foreground(t.subtle),
		bandHint: lipgloss.NewStyle().Background(t.band).Foreground(t.muted),

		welcomeTitle: fg(t.primary).Bold(true),
		welcomeText:  fg(t.subtle),

		inputBox:     box.BorderForeground(t.primary),
		inputBoxBusy: box.BorderForeground(t.muted),
		prompt:       fg(t.primary).Bold(true),
		promptBusy:   fg(t.muted),

		help: help.Styles{
			ShortKey:       fg(t.subtle),
			ShortDesc:      fg(t.muted),
			ShortSeparator: fg(t.muted),
			Ellipsis:       fg(t.muted),
		},
	}
}

// markdownStyle derives the glamour style from the theme. It starts from glamour's own dark or light
// style (whose code colours are already tuned) and changes what has to match the rest of the UI: no
// document margin or colour, theme colours for headings and inline code, and the band as code background.
func markdownStyle(t theme) ansi.StyleConfig {
	cfg := gstyles.LightStyleConfig
	if t.dark {
		cfg = gstyles.DarkStyleConfig
	}
	// cfg is a copy, but it shares pointers with glamour's package variables. Only ever assign new
	// pointers below; writing through an existing one would change glamour's defaults for everyone.
	zero := uint(0)
	cfg.Document = ansi.StyleBlock{Margin: &zero}

	cfg.Heading.Color = hex(t.info)
	for _, h := range []*ansi.StyleBlock{&cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5} {
		h.Color = hex(t.info)
	}
	cfg.H6.Color = hex(t.subtle)
	// Headings are told apart by colour and weight; the "##" markers would look like raw markdown.
	for _, h := range []*ansi.StyleBlock{&cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix = ""
	}
	cfg.H1.Color, cfg.H1.BackgroundColor = hex(t.onAccent), hex(t.primary)

	cfg.Code.Color, cfg.Code.BackgroundColor = hex(t.warning), hex(t.band)

	// Code blocks use a named chroma style. Glamour's own per-theme colour tables are registered once under a
	// single global name, so the first theme rendered would win for the whole process and a later switch
	// between dark and light would keep the old code colours. Named styles have no such problem.
	cfg.CodeBlock.Margin = &zero
	cfg.CodeBlock.Chroma = nil
	cfg.CodeBlock.Theme = "github"
	if t.dark {
		cfg.CodeBlock.Theme = "monokai"
	}
	return cfg
}

// hex converts a colour to the "#rrggbb" form glamour expects.
func hex(c color.Color) *string {
	col, _ := colorful.MakeColor(c)
	h := col.Hex()
	return &h
}
