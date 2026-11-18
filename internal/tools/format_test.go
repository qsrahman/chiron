package tools

import "testing"

func TestFormatArguments(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"an escape is decoded", `{"command":"pwd && ls -la"}`, "command: pwd && ls -la"},
		{"the order of the model is kept", `{"b":1,"a":"x","c":true,"d":null}`, "b: 1\na: x\nc: true\nd: null"},
		{"a value of several lines goes under its name", `{"text":"one\ntwo\n\nfour"}`, "text:\n  one\n  two\n  \n  four"},
		{"a short structure stays on one line", `{"opts":{"depth": 2, "all":true}}`, `opts: {"depth":2,"all":true}`},
		{"a short list", `{"files":["a.go", "b.go"]}`, `files: ["a.go","b.go"]`},
		{"a long structure is indented", `{"opts":{"alpha":"aaaaaaaaaaaaaaaaaaaa","beta":"bbbbbbbbbbbbbbbbbbbb","gamma":"cccccccccccccccccccc"}}`,
			"opts:\n  {\n    \"alpha\": \"aaaaaaaaaaaaaaaaaaaa\",\n    \"beta\": \"bbbbbbbbbbbbbbbbbbbb\",\n    \"gamma\": \"cccccccccccccccccccc\"\n  }"},
		{"numbers keep their form", `{"n":1.50,"big":12345678901234567890}`, "n: 1.50\nbig: 12345678901234567890"},
		{"a control character is decoded, for the screen to show", `{"c":"a\rb"}`, "c: a\rb"},
		{"nothing", ``, "(no arguments)"},
		{"an empty object", `{}`, "(no arguments)"},
		{"an empty string value", `{"s":""}`, "s: "},
		{"not an object", `["a"]`, `["a"]`},
		{"not JSON", `just words`, `just words`},
		{"cut off", `{"a":`, `{"a":`},
		{"data after the object", `{"a":1} {"b":2}`, `{"a":1} {"b":2}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatArguments(tt.in); got != tt.want {
				t.Errorf("FormatArguments(%q) =\n%q\nwant\n%q", tt.in, got, tt.want)
			}
		})
	}
}
