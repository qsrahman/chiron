package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func labels(m Model) []string {
	list, _ := m.suggestions()
	var out []string
	for _, s := range list {
		out = append(out, s.label)
	}
	return out
}

func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		next, cmd := update(m, tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next
		// Only the fetch matters here; the input's own commands (a blinking cursor) never end.
		if cmd != nil && m.modelsAsked && m.models == nil {
			m = pump(t, m, cmd)
		}
	}
	return m
}

func TestModelCommandListsTheServersModels(t *testing.T) {
	a := &fakeAgent{models: []string{"alpha", "beta"}}

	m, cmd := runLine(t, a, "/model")

	out := screen(pump(t, m, cmd))
	for _, want := range []string{"Model: test-model", "available", "alpha, beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

func TestModelCommandSaysWhyThereIsNoList(t *testing.T) {
	for name, tt := range map[string]struct {
		agent *fakeAgent
		want  string
	}{
		"an error":  {&fakeAgent{modelsErr: errors.New("no such route")}, "Could not list the models: no such route"},
		"no models": {&fakeAgent{}, "The server lists no models"},
	} {
		t.Run(name, func(t *testing.T) {
			m, cmd := runLine(t, tt.agent, "/model")
			if out := screen(pump(t, m, cmd)); !strings.Contains(out, tt.want) {
				t.Errorf("screen lacks %q:\n%s", tt.want, out)
			}
		})
	}
}

func TestModelNamesAreCompletedAfterTheCommand(t *testing.T) {
	a := &fakeAgent{models: []string{"Qwen/Qwen3", "alpha", "beta"}}

	m := typeText(t, newTestModel(a), "/model ")
	if got := labels(m); !slices.Equal(got, a.models) {
		t.Fatalf("suggestions = %q, want every model", got)
	}
	m = typeText(t, m, "qw") // the case of a name does not have to be typed
	if got := labels(m); !slices.Equal(got, []string{"Qwen/Qwen3"}) {
		t.Errorf("suggestions = %q", got)
	}
	if a.modelCalls != 1 {
		t.Errorf("the server was asked %d times, want once", a.modelCalls)
	}
}

func TestTheModelsAreNotAskedForUntilTheyAreNeeded(t *testing.T) {
	a := &fakeAgent{models: []string{"alpha"}}

	typeText(t, newTestModel(a), "/effort low and some words")

	if a.modelCalls != 0 {
		t.Errorf("asked %d times", a.modelCalls)
	}
}
