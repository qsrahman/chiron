package tui

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// unsafeText matches what could manipulate the terminal or spoof what it shows, in text that comes from the
// model, a tool or a server: control characters (escape sequences such as clipboard writes and cursor moves,
// bell, carriage return, which can overwrite a line), the C1 controls, Unicode bidi marks, overrides and
// isolates that can make text display misleadingly, and the invisible tag characters that can hide text.
// Newline and tab are kept, and so are the zero-width joiners that Persian, Urdu and emoji text need.
// Removing ESC alone defuses an escape sequence, so this also works on a chunk of a stream that splits one.
var unsafeText = regexp.MustCompile(`[\x{00}-\x{08}\x{0b}-\x{1f}\x{7f}-\x{9f}\x{061c}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}\x{e0000}-\x{e007f}]`)

// escape shows what sanitize would remove as text (\x1b, \r, \u202e), for the places where the user must see
// exactly what is there, such as the details of a call they are asked to allow.
func escape(text string) string {
	return unsafeText.ReplaceAllStringFunc(text, func(r string) string {
		quoted := strconv.QuoteToASCII(r)
		return quoted[1 : len(quoted)-1]
	})
}

// sanitize removes the characters described at unsafeText. Bytes that are not UTF-8 are replaced first: removing a
// character from between two of them could join them into one of those characters.
func sanitize(text string) string {
	return unsafeText.ReplaceAllString(strings.ToValidUTF8(text, "\uFFFD"), "")
}

// entity is anything html.UnescapeString may read as a character reference, with or without its semicolon.
var entity = regexp.MustCompile(`&(?:#[xX][0-9a-fA-F]+|#[0-9]+|[a-zA-Z][a-zA-Z0-9]*);?`)

// removeUnsafeEntities removes the character references that stand for what sanitize removes. The markdown renderer
// decodes them (with html.UnescapeString), so clean text such as "&#27;" would reach the terminal as an escape. It
// repeats until nothing changes, because removing one reference can join the text around it into another.
func removeUnsafeEntities(markdown string) string {
	for {
		cleaned := entity.ReplaceAllStringFunc(markdown, func(ref string) string {
			if unsafeText.MatchString(html.UnescapeString(ref)) {
				return ""
			}
			return ref
		})
		if cleaned == markdown {
			return cleaned
		}
		markdown = cleaned
	}
}
