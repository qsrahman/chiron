package tui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// unsafeRune says again, without the regular expression under test, what must never reach the terminal.
func unsafeRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return r >= 0xe0000 && r <= 0xe007f
}

var hostileText = []string{
	"", "plain", "\x1b]52;c;Zm9v\x07", "a\rb", "\u202egnp.exe", "\U000e0041", "\x9b31m", "می\u200cخواهم 👩\u200d💻",
	"&#27;]52;c;Zm9v&#7;", "&#x202e;abc", "&#x9b;31m", "[x](http://a&#x1b;b)",
	"```go\nx := a - b\n```", "~~~\nunclosed", "- a very long list item with --flags and read-only words that has to wrap",
	"| a-b | c |\n|---|---|\n| 1 | 2 |", "# H\n\n> quote\n\n1. one\n   ```\n   in a list\n   ```", "a\tb", "\ue000-x",
	"\xe2\x80\x00\xae", // removing the byte in the middle leaves a bidi override
	// Entities the renderer decodes: without a semicolon, named, too large to fit, and one made by removing another.
	"a&#27[31mb", "a&rlm;b&lrm;c", "a&#4294967323;b", "&&#27;#x202e;abc", "`a&#13;b`", "a&amp;#27;b", "a\\&#27;b",
	"<span>&#27;</span>", "<div>\n&amp;#27;\n</div>", "![&#27;](&#27;)", "| &#27; |\n|---|\n| &#x202e; |", "[^&#27;]: &#27;",
}

func FuzzSanitize(f *testing.F) {
	for _, s := range hostileText {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		clean, shown := sanitize(text), escape(text)
		for name, out := range map[string]string{"sanitize": clean, "escape": shown} {
			if i := strings.IndexFunc(out, unsafeRune); i >= 0 {
				t.Errorf("%s(%q) keeps %q", name, text, out[i:])
			}
		}
		if again := sanitize(clean); again != clean {
			t.Errorf("sanitize is not stable: %q then %q", clean, again)
		}
		if !utf8.ValidString(text) {
			return
		}
		want := strings.Map(func(r rune) rune {
			if unsafeRune(r) {
				return -1
			}
			return r
		}, text)
		if clean != want {
			t.Errorf("sanitize(%q) = %q, want %q", text, clean, want)
		}
	})
}

// styling is what the renderer itself writes: colours and hyperlinks.
var styling = regexp.MustCompile("\x1b\\[[0-9;:]*m|\x1b\\]8;[^\x07\x1b]*(\x07|\x1b\\\\)")

// FuzzRender holds what render promises for any text of the model: nothing too wide, nothing that drives the
// terminal, and no stand-in character left behind.
func FuzzRender(f *testing.F) {
	for _, s := range hostileText {
		f.Add(s, 80)
		f.Add(s, 24)
	}
	f.Fuzz(func(t *testing.T, text string, width int) {
		if len(text) > 4000 || !utf8.ValidString(text) {
			return
		}
		m := sized(&fakeAgent{}, 20+int(uint(width)%200), 30)

		out := m.render(entry{role: roleAssistant, text: text})

		for _, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > m.contentWidth() {
				t.Errorf("a line of %d cells, at most %d: %q", w, m.contentWidth(), line)
			}
		}
		rest := styling.ReplaceAllString(out, "")
		if i := strings.IndexFunc(rest, unsafeRune); i >= 0 {
			t.Errorf("render(%q) writes %q", text, rest[i:min(len(rest), i+40)])
		}
		if strings.Contains(out, unbreakable) && !strings.Contains(text, unbreakable) && !strings.Contains(text, "&") {
			t.Errorf("the stand-in for a hyphen is left in the output of %q", text)
		}
	})
}

func FuzzKeepHyphenated(f *testing.F) {
	for _, s := range hostileText {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if strings.Contains(text, unbreakable) {
			return
		}
		if got := strings.ReplaceAll(keepHyphenated(text), unbreakable, "-"); got != text {
			t.Errorf("%q does not come back: %q", text, got)
		}
		splitFences(text)
	})
}
