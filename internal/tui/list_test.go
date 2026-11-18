package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func renderPlain(m *Model, markdown string, width int) []string {
	var lines []string
	for _, l := range strings.Split(m.markdownText(markdown, width), "\n") {
		lines = append(lines, strings.TrimRight(ansi.Strip(l), " "))
	}
	return lines
}

func TestWrappedListItemsHangUnderTheirText(t *testing.T) {
	m := sized(&fakeAgent{}, 44, 24) // glamour wraps at 40 here (44 columns, a margin on each side and the gutter)
	md := "- A long first item with **bold words that wrap** across several lines of text here\n" +
		"- second\n" +
		"  - nested item that is also long enough to need wrapping across two lines okay\n\n" +
		"1. numbered long item that goes on and on and on past the edge of the screen width\n"

	got := strings.ReplaceAll(strings.Join(renderPlain(&m, md, 40), "\n"), "\n\n\n", "\n\n") // glamour leaves two blank rows after a nested list

	want := `• A long first item with bold words that
  wrap across several lines of text here
• second
  • nested item that is also long enough
    to need wrapping across two lines
    okay

1. numbered long item that goes on and
   on and on past the edge of the screen
   width`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, l := range strings.Split(m.markdownText(md, 40), "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("a line is %d cells wide, wider than the %d it was given: %q", w, 40, l)
		}
	}
}

func TestListHangLeavesOtherLinesAlone(t *testing.T) {
	m := sized(&fakeAgent{}, 44, 24)
	md := "Plain paragraph that is long enough to wrap onto a second line at this width of forty.\n\n" +
		"1. Step with code:\n\n   ```\n   run this\n   ```\n\n2. next step\n"

	got := strings.Join(renderPlain(&m, md, 40), "\n")

	want := `Plain paragraph that is long enough to
wrap onto a second line at this width of
forty.

1. Step with code:
run this

2. next step`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestContinuesItem(t *testing.T) {
	const prev = "• a line that is full up to the edge"
	tests := []struct {
		name, next string
		want       bool
	}{
		{"soft-wrapped rest, same indent", "continuation words", true},
		{"first word would have fitted", "ok words", false},
		{"different indent", "  continuation words", false},
		{"next item", "• continuation words", false},
		{"blank line", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := continuesItem(prev, tt.next, 0, 40); got != tt.want {
				t.Errorf("continuesItem(%q) = %v, want %v", tt.next, got, tt.want)
			}
		})
	}
}
