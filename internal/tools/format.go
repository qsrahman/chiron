package tools

import (
	"bytes"
	"encoding/json"
	"strings"
)

// compactLimit is the longest structured value that is shown on one line.
const compactLimit = 60

// FormatArguments lays the arguments of a call out for a person: one "name: value" line per argument in the order the
// model gave them, a value of several lines under its name, and a structured value as JSON, on one line if it is short
// and indented otherwise. The values are decoded, so what is shown is what the tool receives: a JSON escape such as
// & shows as &. Arguments that are not a JSON object are returned as they are.
func FormatArguments(arguments string) string {
	text := strings.TrimSpace(arguments)
	if text == "" {
		return "(no arguments)"
	}
	dec := json.NewDecoder(strings.NewReader(text))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return arguments
	}
	var lines []string
	for dec.More() {
		keyToken, err := dec.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return arguments
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return arguments
		}
		lines = append(lines, formatArgument(key, value)...)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || dec.InputOffset() != int64(len(text)) {
		return arguments
	}
	if len(lines) == 0 {
		return "(no arguments)"
	}
	return strings.Join(lines, "\n")
}

func formatArgument(key string, value json.RawMessage) []string {
	var s string
	// null also unmarshals into a string, as an empty one: only a JSON string is shown as text.
	if bytes.HasPrefix(bytes.TrimSpace(value), []byte(`"`)) && json.Unmarshal(value, &s) == nil {
		if !strings.Contains(s, "\n") {
			return []string{key + ": " + s}
		}
		return append([]string{key + ":"}, indented(s)...)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, value) != nil {
		return []string{key + ": " + string(value)}
	}
	if compact.Len() <= compactLimit || (compact.Bytes()[0] != '{' && compact.Bytes()[0] != '[') {
		return []string{key + ": " + compact.String()}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, value, "", "  ") != nil {
		return []string{key + ": " + compact.String()}
	}
	return append([]string{key + ":"}, indented(pretty.String())...)
}

// indented puts the lines of s two columns in.
func indented(s string) []string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return lines
}
