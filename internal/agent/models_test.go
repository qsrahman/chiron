package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestModelsListsTheNamesThatCanBeTyped(t *testing.T) {
	srv := newScriptedServer(t, reply{json: true, body: `{"object":"list","data":[
		{"id":"zeta"},{"id":"Qwen/Qwen3-8B"},{"id":"zeta"},{"id":""},{"id":"two words"},{"id":"bad\u001b[31m"},{"id":"flip\u202e"}]}`})

	got, err := newTestAgent(srv).Models(context.Background())

	if want := []string{"Qwen/Qwen3-8B", "zeta"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("models = %q, %v; want %q", got, err, want)
	}
}

func TestModelsDescribesAServerThatCannotList(t *testing.T) {
	srv := newScriptedServer(t, apiError(404, "", "no such route"))

	_, err := newTestAgent(srv).Models(context.Background())

	if err == nil || !strings.Contains(err.Error(), "API error 404: no such route") {
		t.Errorf("error = %v", err)
	}
}
