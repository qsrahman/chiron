// Package mcp connects desk-agent to MCP servers and presents their tools as tools.Tool.
package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expand replaces ${NAME} with the value of the environment variable NAME. A variable that is not set is an
// error that names it (never a value). The text that was put in is not scanned again, so a value that contains
// ${OTHER} stays as it is, and $NAME without braces is left alone: a command line may need a literal dollar sign.
func expand(s string, lookup func(string) (string, bool)) (string, error) {
	var out strings.Builder
	last := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(s, -1) {
		name := s[m[2]:m[3]]
		value, ok := lookup(name)
		if !ok {
			return "", fmt.Errorf("variable ${%s} is not set", name)
		}
		out.WriteString(s[last:m[0]])
		out.WriteString(value)
		last = m[1]
	}
	out.WriteString(s[last:])
	return out.String(), nil
}
