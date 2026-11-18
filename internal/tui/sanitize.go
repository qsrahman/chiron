package tui

import "regexp"

// unsafeText matches what could manipulate the terminal or spoof what it shows, in text that comes from the
// model, a tool or a server: control characters (escape sequences such as clipboard writes and cursor moves,
// bell, carriage return, which can overwrite a line), the C1 controls, Unicode bidi marks, overrides and
// isolates that can make text display misleadingly, and the invisible tag characters that can hide text.
// Newline and tab are kept, and so are the zero-width joiners that Persian, Urdu and emoji text need.
// Removing ESC alone defuses an escape sequence, so this also works on a chunk of a stream that splits one.
var unsafeText = regexp.MustCompile(`[\x{00}-\x{08}\x{0b}-\x{1f}\x{7f}-\x{9f}\x{061c}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}\x{e0000}-\x{e007f}]`)

// sanitize removes the characters described at unsafeText.
func sanitize(text string) string {
	return unsafeText.ReplaceAllString(text, "")
}
