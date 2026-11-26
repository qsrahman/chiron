package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qsrahman/chiron/internal/session"
	"github.com/qsrahman/chiron/internal/tools"
)

// FuzzResume resumes a conversation whose items hold anything. If the file is accepted, everything that reads the
// history must cope with it.
func FuzzResume(f *testing.F) {
	call := `{"type":"function_call","call_id":"c","name":"n","arguments":"{}"}`
	for _, raw := range []string{
		"", "null", "{}", "[]", `""`, `{"content":"hi"}`, `{"content":[{"text":"hi"}]}`, `{"content":5}`, call,
		`{"output":{}}`, `{"type":"reasoning","encrypted_content":"x"}`, `{"type":"message","role":"user"}`,
	} {
		for _, kind := range []string{kindUser, kindAssistant, kindCall, kindCallOutput, kindReasoning, "message"} {
			f.Add(kind, "c", []byte(raw), []byte(call), "summary")
		}
	}
	f.Fuzz(func(t *testing.T, kind, callID string, raw, next []byte, summary string) {
		a := New(newFakeServer(t).client(), testSettings("m"), "", &tools.Registry{})
		file := session.File{Version: 1, ID: "x-1", Model: "m", Summary: summary, Items: []session.Item{
			{Kind: kindUser, Raw: []byte(`{"content":"first"}`)},
			{Kind: kind, CallID: callID, Raw: raw},
			{Kind: kindUser, Raw: []byte(`{"content":"second"}`)},
			{Kind: kindCall, CallID: "c", Raw: next},
		}}
		if err := a.Resume(file, "e"); err != nil {
			return
		}
		a.Messages()
		a.ContextEstimate()
		a.input(a.history.items)
		transcript(a.history.items)
		a.history.planCompaction(1)
		a.Rewind()
		a.Undo()
		a.Messages()
	})
}

func FuzzCapOutput(f *testing.F) {
	f.Add("short", 1)
	f.Add("é", maxOutputBytes)
	f.Add("a€😀", maxOutputBytes/8+1)
	f.Add("\x80", maxOutputBytes+1)
	f.Fuzz(func(t *testing.T, unit string, times int) {
		if len(unit) > 64 {
			return
		}
		text := strings.Repeat(unit, int(uint(times)%uint(maxOutputBytes+100)))

		got := capOutput(text)

		if len(got) > maxOutputBytes+64 {
			t.Errorf("%d bytes after the cut", len(got))
		}
		if utf8.ValidString(text) && !utf8.ValidString(got) {
			t.Error("the cut split a character")
		}
	})
}
