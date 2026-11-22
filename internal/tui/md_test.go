package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSplitFences(t *testing.T) {
	type seg = fenceSegment
	tests := []struct {
		name, in string
		want     []seg
	}{
		{"no code", "just text\nmore", []seg{{text: "just text\nmore"}}},
		{"a block between prose", "before\n```go\nx := 1\n```\nafter", []seg{{text: "before"}, {code: true, lang: "go", text: "x := 1"}, {text: "after"}}},
		{"no language", "```\nplain\n```", []seg{{code: true, text: "plain"}}},
		{"tildes", "~~~bash\nls -la\n~~~", []seg{{code: true, lang: "bash", text: "ls -la"}}},
		{"a longer fence is not closed by a shorter one", "````\n```\ninner\n```\n````", []seg{{code: true, text: "```\ninner\n```"}}},
		{"not closed yet (streaming)", "text\n```py\nprint(1)", []seg{{text: "text"}, {code: true, lang: "py", text: "print(1)"}}},
		{"an empty block", "```\n```", []seg{{code: true, text: ""}}},
		{"two blocks", "```a\n1\n```\n```b\n2\n```", []seg{{code: true, lang: "a", text: "1"}, {code: true, lang: "b", text: "2"}}},
		{"an indented fence stays with the prose, as it may be in a list item", "1. step\n   ```\n   code\n   ```", []seg{{text: "1. step\n   ```\n   code\n   ```"}}},
		{"a fence with other text after it is not a closing fence", "```\na\n```not\nb\n```", []seg{{code: true, text: "a\n```not\nb"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFences(tt.in)
			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", tt.want) {
				t.Errorf("splitFences(%q) =\n%+v\nwant\n%+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestKeepHyphenated(t *testing.T) {
	h := unbreakable
	tests := []struct{ name, in, want string }{
		{"a flag", "use --config-file now", "use " + h + h + "config" + h + "file now"},
		{"inside code spans too", "`--base-url`", "`" + h + h + "base" + h + "url`"},
		{"a list marker", "- item", "- item"},
		{"a rule", "---", "---"},
		{"a table separator", "| a | b |\n|---|---|", "| a | b |\n|---|---|"},
		{"the words of a bullet", "- read-only tools", "- read" + h + "only tools"},
		{"a fenced block is left alone", "x-y\n```sh\nls -la --all\n```\nz-w", "x" + h + "y\n```sh\nls -la --all\n```\nz" + h + "w"},
		{"an indented fence too", "1. a-b\n   ```\n   c-d\n   ```", "1. a" + h + "b\n   ```\n   c-d\n   ```"},
		{"text that has the stand-in already is left as it is", "a-b " + h, "a-b " + h},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keepHyphenated(tt.in); got != tt.want {
				t.Errorf("keepHyphenated(%q) =\n%q\nwant\n%q", tt.in, got, tt.want)
			}
		})
	}
}

// A flag is never split at its hyphens, and what is on screen holds the original hyphens.
func TestFlagsAreNotBrokenAtTheirHyphens(t *testing.T) {
	m := sized(&fakeAgent{}, 46, 24)
	md := "Optional: `--model`, `--base-url`, `--reasoning-effort`, `--resume`, `--streaming/--no-streaming`, a-hyphenated-word."

	out := ansi.Strip(m.markdownText(md, m.contentWidth()-gutter))

	if strings.Contains(out, unbreakable) {
		t.Error("the stand-in for the hyphen was left in the text")
	}
	for _, word := range []string{"--model", "--base-url", "--reasoning-effort", "--resume", "a-hyphenated-word"} {
		if !strings.Contains(out, word) {
			t.Errorf("%q was split across lines:\n%s", word, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if t2 := strings.TrimSpace(line); strings.HasSuffix(t2, "-") {
			t.Errorf("a line ends at a hyphen: %q", line)
		}
	}
}

func TestAFencedCodeBlockIsAShadedBandAsWideAsTheText(t *testing.T) {
	m := sized(&fakeAgent{}, 60, 24)
	width := m.contentWidth() - gutter

	out := m.markdownText("Run it:\n\n```bash\ngo build ./... # now\nexport A=1\n```\n\nThen test.", width)

	lines := strings.Split(out, "\n")
	var band []string
	for _, l := range lines {
		if strings.Contains(l, "\x1b[48;") {
			band = append(band, l)
		}
	}
	if len(band) != 2 {
		t.Fatalf("%d rows have the band colour, want the two code lines:\n%q", len(band), out)
	}
	for _, row := range band {
		if w := ansi.StringWidth(row); w != width {
			t.Errorf("a band row is %d cells wide, want %d: %q", w, width, ansi.Strip(row))
		}
		// After every reset the band colour comes back, so the shade runs unbroken to the end of the row.
		for _, reset := range []string{"\x1b[0m", "\x1b[m"} {
			for _, after := range strings.Split(row, reset)[1:] {
				if !strings.HasPrefix(after, "\x1b[48;") && after != "" {
					t.Errorf("the band colour is lost after a reset: %q", after)
				}
			}
		}
	}
	var trimmed []string
	for _, l := range strings.Split(ansi.Strip(out), "\n") {
		trimmed = append(trimmed, strings.TrimRight(l, " "))
	}
	plain := strings.Join(trimmed, "\n")
	if !strings.Contains(plain, " go build ./... # now") || !strings.Contains(plain, "Run it:\n\n") || !strings.Contains(plain, "\n\nThen test.") {
		t.Errorf("the band is not set off from the prose by blank lines:\n%s", plain)
	}
	if !strings.Contains(out, "\x1b[38;") {
		t.Error("the code lost its syntax colours")
	}
}

func TestACodeBlockThatIsStillStreamingIsABand(t *testing.T) {
	m := sized(&fakeAgent{}, 60, 24)

	out := m.markdownText("Look:\n\n```go\nfunc main() {", m.contentWidth()-gutter)

	if !strings.Contains(out, "\x1b[48;") || !strings.Contains(ansi.Strip(out), "func main() {") {
		t.Errorf("an unclosed block should already show as a band: %q", out)
	}
}

func TestLongCodeLinesAreCutNotWrapped(t *testing.T) {
	m := sized(&fakeAgent{}, 40, 24)
	width := m.contentWidth() - gutter

	out := m.markdownText("```\n"+strings.Repeat("x", 200)+"\n```", width)

	rows := strings.Split(out, "\n")
	if len(rows) != 1 || ansi.StringWidth(rows[0]) != width || !strings.Contains(ansi.Strip(rows[0]), "…") {
		t.Errorf("a long code line is cut with an ellipsis to the width of the band: %d rows, width %d (want %d)", len(rows), ansi.StringWidth(rows[0]), width)
	}
}

func TestAnIndentedFenceInAListStaysWithTheList(t *testing.T) {
	m := sized(&fakeAgent{}, 60, 24)

	out := m.markdownText("1. Step one:\n\n   ```\n   run this\n   ```\n\n2. Step two", m.contentWidth()-gutter)

	plain := ansi.Strip(out)
	for _, want := range []string{"1. Step one:", "run this", "2. Step two"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the list lost %q:\n%s", want, plain)
		}
	}
	if strings.Contains(out, "\x1b[48;") {
		t.Error("a fence inside a list item was taken out of the list and shaded")
	}
}

func TestInlineCodeUsesItsOwnColourNotAmber(t *testing.T) {
	m := sized(&fakeAgent{}, 80, 24)

	out := m.markdownText("Use `go build` now.", 76)

	if !strings.Contains(out, "38;2;94;234;212") {
		t.Errorf("inline code is not in the code colour: %q", out)
	}
	if strings.Contains(out, "251;191;36") {
		t.Error("inline code is amber, the colour of warnings and of work in progress")
	}
}

// --- layout: the margin, the header, the hint that output is below ---

func TestThereIsAMarginOnBothSides(t *testing.T) {
	for _, width := range []int{40, 50, 80, 100, 160} {
		for name, m := range map[string]Model{"empty": sized(&fakeAgent{}, width, 30), "conversation": conversation(t, width, 40)} {
			for i, line := range strings.Split(screen(m), "\n") {
				if w := ansi.StringWidth(line); w > width-margin {
					t.Errorf("%s at width %d: line %d is %d cells wide, but the last column is the margin: %q", name, width, i, w, line)
				}
			}
		}
	}
}

func TestTheHeaderIsTwoRowsAndTheTranscriptStartsRightUnderIt(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 24)
	m.addEntry(entry{role: roleAssistant, text: "First answer."})
	m.refresh()

	if rows := strings.Count(m.headerView(), "\n") + 1; rows != headerHeight || headerHeight != 2 {
		t.Errorf("the header is %d rows (headerHeight %d), want 2", rows, headerHeight)
	}
	lines := strings.Split(screen(m), "\n")
	if !strings.Contains(lines[1], "───") || !strings.Contains(lines[2], "First answer.") {
		t.Errorf("rows 1 and 2: %q, %q", lines[1], lines[2])
	}
	if h := len(lines) - 1; h != 24 { // the screen ends with a newline
		t.Errorf("the screen is %d rows, want the window's 24", h)
	}
}

func TestTheHelpBarSaysHowMuchIsBelowWhenScrolledUp(t *testing.T) {
	m := scrollable(t)
	if strings.Contains(ansi.Strip(m.helpView()), "below") {
		t.Fatal("the hint is shown at the bottom")
	}

	m = scrolledUp(m, 1)
	one := ansi.Strip(m.helpView())
	if !strings.Contains(one, "↓ 1 line below") {
		t.Fatalf("one line up: %q", one)
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if got := ansi.Strip(m.helpView()); !strings.Contains(got, "↓ ") || strings.Contains(got, "↓ 1 line") {
		t.Errorf("a page up should count more lines: %q", got)
	}

	m.viewport.GotoBottom()
	if strings.Contains(ansi.Strip(m.helpView()), "below") {
		t.Error("the hint stays at the bottom")
	}
}

func TestTheHintDoesNotOverflowAndKeepsItsPlaceInANarrowTerminal(t *testing.T) {
	for _, width := range []int{40, 50, 80} {
		m := sized(&fakeAgent{}, width, 20)
		for i := 0; i < 60; i++ {
			m.addEntry(entry{role: roleNotice, text: fmt.Sprintf("line %d", i)})
		}
		m.refresh()
		m.viewport.GotoBottom()
		m = scrolledUp(m, 1)

		bar := ansi.Strip(m.helpView())
		if !strings.HasSuffix(strings.TrimRight(bar, " "), "below") || ansi.StringWidth(bar) > width-margin {
			t.Errorf("width %d: help bar %q (%d cells)", width, bar, ansi.StringWidth(bar))
		}
	}
}

// Re-wrapping a list item must not break at a hyphen either.
func TestHyphenatedWordsInListItemsAreNotBrokenWhenTheyHang(t *testing.T) {
	md := "- first item with some words before --reasoning-effort and a-hyphenated-word and more read-only words here\n- second"
	for width := 44; width <= 70; width++ {
		m := sized(&fakeAgent{}, width, 24)
		out := ansi.Strip(m.markdownText(md, m.contentWidth()-gutter))
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "-") {
				t.Fatalf("width %d: a list item line ends at a hyphen:\n%s", width, out)
			}
		}
	}
}
