package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func parseResponse(t *testing.T, status, extra string, items ...string) *responses.Response {
	t.Helper()
	body := fmt.Sprintf(`{"id":"r","object":"response","status":%q,"output":[%s]%s}`, status, joinItems(items), extra)
	var r responses.Response
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func joinItems(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it
	}
	return out
}

func TestResponseProblem(t *testing.T) {
	call := `{"type":"function_call","id":"fc","call_id":"c","name":"x","arguments":"{}","status":"completed"}`
	tests := []struct {
		name string
		resp *responses.Response
		want string
	}{
		{"a reply", parseResponse(t, "completed", "", messageItem("hi")), ""},
		{"a call and no text", parseResponse(t, "completed", "", call), ""},
		{"no status at all", parseResponse(t, "", "", messageItem("hi")), ""},
		{"incomplete", parseResponse(t, "incomplete", `,"incomplete_details":{"reason":"max_output_tokens"}`, messageItem("hi")), "Response incomplete: max_output_tokens"},
		{"incomplete with no reason", parseResponse(t, "incomplete", ""), "Response incomplete: unknown"},
		{"failed", parseResponse(t, "failed", `,"error":{"code":"server_error","message":"boom"}`), "Response failed: server_error: boom"},
		{"failed without an error", parseResponse(t, "failed", ""), "Response failed: unknown error"},
		{"another status", parseResponse(t, "cancelled", ""), "Response not completed: cancelled"},
		{"refused", parseResponse(t, "completed", "", refusalItem), "Refused: I can't help with that."},
		{"nothing at all", parseResponse(t, "completed", ""), noText},
		{"an empty message", parseResponse(t, "completed", "", messageItem("")), noText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responseProblem(tt.resp); got != tt.want {
				t.Errorf("responseProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}
