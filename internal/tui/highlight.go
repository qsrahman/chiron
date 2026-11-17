package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
)

// highlight colours code for the terminal. It returns the code unchanged if the language is unknown,
// so a failure to colour never hides the text.
func highlight(code, lexer string, dark bool) string {
	style := "github"
	if dark {
		style = "monokai"
	}
	var out strings.Builder
	if err := quick.Highlight(&out, code, lexer, "terminal16m", style); err != nil {
		return code
	}
	return strings.TrimRight(out.String(), "\n")
}
