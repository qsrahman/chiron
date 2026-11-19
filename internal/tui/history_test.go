package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/qsrahman/chiron/internal/agent"
)

func withHistory(prompts ...string) Model {
	m := newTestModel(&fakeAgent{})
	for _, p := range prompts {
		m.hist.add(p)
	}
	return m
}

func typed(m Model, text string) Model {
	for _, r := range text {
		m, _ = update(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func TestUpAndDownWalkThroughTheHistoryAndBackToAnEmptyInput(t *testing.T) {
	m := withHistory("one", "two", "three")
	var seen []string
	for i := 0; i < 4; i++ { // the fourth Up stays on the oldest
		m, _ = update(m, up)
		seen = append(seen, m.input.Value())
	}
	if want := []string{"three", "two", "one", "one"}; !slices.Equal(seen, want) {
		t.Errorf("Up showed %v, want %v", seen, want)
	}
	seen = nil
	for i := 0; i < 3; i++ {
		m, _ = update(m, down)
		seen = append(seen, m.input.Value())
	}
	if want := []string{"two", "three", ""}; !slices.Equal(seen, want) {
		t.Errorf("Down showed %v, want %v", seen, want)
	}
	m, _ = update(m, down)
	if m.input.Value() != "" {
		t.Error("Down on an empty input must do nothing")
	}
}

func TestUpWithNoHistoryDoesNothing(t *testing.T) {
	m, _ := update(withHistory(), up)
	if m.input.Value() != "" {
		t.Errorf("input = %q", m.input.Value())
	}
}

func TestARecalledPromptCanBeEditedAndThenUpBelongsToTheInput(t *testing.T) {
	m := withHistory("one", "two")
	m, _ = update(m, up)
	m = typed(m, "!")
	if m.input.Value() != "two!" {
		t.Fatalf("input = %q", m.input.Value())
	}
	m, _ = update(m, up)
	if m.input.Value() != "two!" {
		t.Errorf("Up on an edited prompt must not recall: %q", m.input.Value())
	}
}

func TestARecalledPromptIsSentAndRemembered(t *testing.T) {
	m := withHistory("one", "two")
	m, _ = update(m, up)
	m, _ = update(m, up)
	m = typed(m, "!")
	m, _ = update(m, enter)

	if got := m.hist.items; !slices.Equal(got, []string{"one", "two", "one!"}) {
		t.Errorf("history = %v", got)
	}
	if lastUser(m) != "one!" {
		t.Errorf("sent %q", lastUser(m))
	}
}

func lastUser(m Model) string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].role == roleUser {
			return m.entries[i].text
		}
	}
	return ""
}

func TestARecalledCommandDoesNotOpenTheSuggestionList(t *testing.T) {
	m := withHistory("/model other", "hello", "/context")
	m, _ = update(m, up)
	if list, _ := m.suggestions(); len(list) != 0 || m.input.Value() != "/context" {
		t.Fatalf("input %q, %d suggestions", m.input.Value(), len(list))
	}
	m, _ = update(m, up)
	m, _ = update(m, up)
	if m.input.Value() != "/model other" {
		t.Errorf("Up got stuck at %q: the list took the arrow keys", m.input.Value())
	}
}

func TestHistorySkipsEmptyPromptsAndRepeatsInARow(t *testing.T) {
	h := &promptHistory{}
	for _, p := range []string{"a", "a", "  ", "", "b", "a", " a "} {
		h.add(p)
	}
	if want := []string{"a", "b", "a"}; !slices.Equal(h.items, want) {
		t.Errorf("history = %v, want %v", h.items, want)
	}
}

func TestHistoryKeepsTheNewestThousand(t *testing.T) {
	h := &promptHistory{}
	for i := 0; i < maxPrompts+5; i++ {
		h.add(fmt.Sprint("prompt ", i))
	}
	if len(h.items) != maxPrompts || h.items[0] != "prompt 5" {
		t.Errorf("%d items, first %q", len(h.items), h.items[0])
	}
}

func TestHistorySurvivesARestartInAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history")
	h := loadPromptHistory(path)
	h.add("first")
	h.add("two\nlines")
	h.add(`quote " and \ backslash`)

	again := loadPromptHistory(path)
	if want := []string{"first", "two\nlines", `quote " and \ backslash`}; !slices.Equal(again.items, want) {
		t.Errorf("reloaded %q, want %q", again.items, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory: %v %v", info, err)
	}
}

func TestHistoryIgnoresDamagedLinesAndTrimsALongFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	var b strings.Builder
	b.WriteString("not json\n\"\"\n")
	for i := 0; i < 2*maxPrompts+10; i++ {
		fmt.Fprintf(&b, "%q\n", fmt.Sprint("p", i))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	h := loadPromptHistory(path)
	if len(h.items) != maxPrompts || h.items[len(h.items)-1] != fmt.Sprint("p", 2*maxPrompts+9) {
		t.Fatalf("%d items, last %q", len(h.items), h.items[len(h.items)-1])
	}
	data, _ := os.ReadFile(path)
	if lines := strings.Count(string(data), "\n"); lines != maxPrompts {
		t.Errorf("the file was not rewritten: %d lines", lines)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v after the rewrite", info.Mode().Perm())
	}
}

func TestHistoryWithoutAPathWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := loadPromptHistory("")
	h.add("x")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files: %v", entries)
	}
}

func TestSearchFindsAnOldPromptAndPutsItInTheInput(t *testing.T) {
	m := withHistory("write docs", "fix the build", "fix the tests", "hello")
	m, _ = update(m, ctrl('r'))
	if !m.search.open {
		t.Fatal("Ctrl-R did not open the search")
	}
	m = typed(m, "fix")
	if m.hist.items[m.search.match] != "fix the tests" {
		t.Errorf("match = %q, want the newest", m.hist.items[m.search.match])
	}
	if view := ansi.Strip(m.searchView()); !strings.Contains(view, "fix▏") || !strings.Contains(view, "fix the tests") {
		t.Errorf("search row = %q", view)
	}
	m, _ = update(m, ctrl('r'))
	if m.hist.items[m.search.match] != "fix the build" {
		t.Errorf("second Ctrl-R = %q", m.hist.items[m.search.match])
	}
	m, _ = update(m, ctrl('r')) // no older match: stay
	if m.hist.items[m.search.match] != "fix the build" {
		t.Errorf("past the oldest = %q", m.hist.items[m.search.match])
	}

	m, _ = update(m, enter)
	if m.search.open || m.input.Value() != "fix the build" || len(m.entries) != 0 {
		t.Errorf("open %v, input %q, entries %d: Enter must use the match, not send it", m.search.open, m.input.Value(), len(m.entries))
	}
	m, _ = update(m, up)
	if m.input.Value() != "write docs" {
		t.Errorf("Up after a search went to %q, want the prompt before the match", m.input.Value())
	}
}

func TestSearchEditsAndCancels(t *testing.T) {
	m := withHistory("alpha", "beta")
	m = typed(m, "draft")
	m, _ = update(m, ctrl('r'))
	m = typed(m, "zzz")
	if m.search.match != -1 || !strings.Contains(ansi.Strip(m.searchView()), "no match") {
		t.Errorf("match %d, row %q", m.search.match, ansi.Strip(m.searchView()))
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = typed(m, "ALP")
	if m.hist.items[m.search.match] != "alpha" {
		t.Errorf("a search ignores case: %d", m.search.match)
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search.open || m.input.Value() != "draft" {
		t.Errorf("open %v, input %q: Esc must leave the draft alone", m.search.open, m.input.Value())
	}
}

func TestSearchWithNoMatchLeavesTheInputAlone(t *testing.T) {
	m := withHistory("alpha")
	m, _ = update(m, ctrl('r'))
	m = typed(m, "q")
	m, _ = update(m, enter)
	if m.input.Value() != "" || m.search.open {
		t.Errorf("input %q, open %v", m.input.Value(), m.search.open)
	}
}

func TestSearchWithoutHistorySaysSo(t *testing.T) {
	m, _ := update(withHistory(), ctrl('r'))
	if m.search.open || !strings.Contains(m.hint, "No prompt history") {
		t.Errorf("open %v, hint %q", m.search.open, m.hint)
	}
}

func TestTheSearchRowTakesPartOfTheWindowAndTheHelpBarFollows(t *testing.T) {
	m := sized(&fakeAgent{}, 100, 20)
	m.hist.add("alpha")
	before := m.viewport.Height()
	m, _ = update(m, ctrl('r'))
	if m.viewport.Height() != before-1 {
		t.Errorf("viewport %d, want %d", m.viewport.Height(), before-1)
	}
	if !strings.Contains(ansi.Strip(m.helpView()), "older match") {
		t.Errorf("help = %q", ansi.Strip(m.helpView()))
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.viewport.Height() != before {
		t.Error("the window did not get its row back")
	}
}

func TestEditorCommandAndFile(t *testing.T) {
	t.Setenv("EDITOR", "vi")
	t.Setenv("VISUAL", "code --wait")
	cmd, path, err := prepareEditor("my prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if !slices.Equal(cmd.Args, []string{"sh", "-c", `code --wait "$@"`, "sh", path}) {
		t.Errorf("args = %v: $VISUAL wins and keeps its arguments", cmd.Args)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "my prompt" || info.Mode().Perm() != 0o600 {
		t.Errorf("file %q mode %v", data, info.Mode().Perm())
	}
	t.Setenv("VISUAL", "")
	if cmd, path2, _ := prepareEditor(""); !strings.HasPrefix(cmd.Args[2], "vi ") {
		t.Errorf("$EDITOR is the fallback: %v", cmd.Args)
	} else {
		os.Remove(path2)
	}
}

func TestNoEditorSaysWhatToSet(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	m, cmd := update(newTestModel(&fakeAgent{}), ctrl('g'))
	if cmd != nil || !strings.Contains(m.hint, "$EDITOR") {
		t.Errorf("hint %q", m.hint)
	}
}

func TestWhatTheEditorLeavesBecomesTheInput(t *testing.T) {
	m := newTestModel(&fakeAgent{})
	m.input.SetValue("old")
	m, _ = update(m, editorDoneMsg{text: "a long\nprompt\n\n"})
	if m.input.Value() != "a long\nprompt" || len(m.entries) != 0 {
		t.Errorf("input %q, entries %d: the text is put in the input and not sent", m.input.Value(), len(m.entries))
	}
	m, _ = update(m, editorDoneMsg{err: fmt.Errorf("the editor failed: exit status 1")})
	if m.input.Value() != "a long\nprompt" || !strings.Contains(m.hint, "editor failed") {
		t.Errorf("a failed editor must keep the text: %q, hint %q", m.input.Value(), m.hint)
	}
}

func TestCtrlZSuspends(t *testing.T) {
	_, cmd := update(newTestModel(&fakeAgent{}), ctrl('z'))
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.SuspendMsg); !ok {
		t.Errorf("Ctrl-Z returned %T", cmd())
	}
}

func TestALongLineDoesNotCostTheEntriesAfterIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	var b strings.Builder
	b.WriteString("\"old\"\n\"" + strings.Repeat("x", 2<<20) + "\"\n\"newer\"\n\"newest\"") // the last line has no newline
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPromptHistory(path).items; !slices.Equal(got, []string{"old", "newer", "newest"}) {
		t.Errorf("items = %q", got)
	}
}

func TestAHugePromptIsNotRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h := loadPromptHistory(path)
	h.add(strings.Repeat("x", maxPromptBytes+1))
	h.add("small")
	if !slices.Equal(h.items, []string{"small"}) {
		t.Errorf("items = %d long", len(h.items))
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "xxxx") {
		t.Error("the huge prompt reached the file")
	}
}

func TestAnEditorPathWithSpacesAndQuotesRuns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my editors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "ed.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'edited:%s' \"$1\" > \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", `'`+script+`' 'an argument'`) // quoted, because of the space in the path
	cmd, path, err := prepareEditor("old")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running the editor: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(path); string(data) != "edited:an argument" {
		t.Errorf("file after the run: %q: the quoted path and argument, then the file, should reach the script", data)
	}
}

func TestAPromptThatStartsWithASpaceIsNotRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h := loadPromptHistory(path)
	h.add(" my secret token")
	h.add("normal")
	if !slices.Equal(h.items, []string{"normal"}) {
		t.Errorf("items = %q", h.items)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "secret") {
		t.Error("the prompt reached the file")
	}
}

func TestAResumedConversationShowsHowItsCallsEndedAndTheToolsTitles(t *testing.T) {
	a := &fakeAgent{messages: []agent.Message{
		{Role: "tool", CallID: "c1", Name: "shell", Output: "error: boom", Outcome: agent.CallFailed},
		{Role: "tool", CallID: "c2", Name: "docs__query", Output: "x", Outcome: agent.CallOK},
		{Role: "tool", CallID: "c3", Name: "shell", Output: "denied", Outcome: agent.CallDenied},
	}, titles: map[string]string{"docs__query": "docs → query"}}
	m := newTestModel(a)

	if m.entries[0].outcome != agent.CallFailed || m.entries[2].outcome != agent.CallDenied {
		t.Errorf("outcomes = %v %v", m.entries[0].outcome, m.entries[2].outcome)
	}
	if m.entries[1].title != "" {
		t.Fatal("the tool was not known at the start")
	}

	m, _ = update(m, startupMsg{}) // the MCP servers have connected
	if m.entries[1].title != "docs → query" || !strings.Contains(ansi.Strip(m.entries[1].rendered), "docs → query") {
		t.Errorf("title %q, card %q", m.entries[1].title, ansi.Strip(m.entries[1].rendered))
	}
}

func TestTheSessionListSaysWhenItIsCutShort(t *testing.T) {
	var list []agent.SessionInfo
	for i := 0; i < agent.SessionListLimit; i++ {
		list = append(list, agent.SessionInfo{ID: fmt.Sprint("s-", i), Turns: 1})
	}
	m, _ := typeAndEnter(t, newTestModel(&fakeAgent{sessions: list}), "/sessions")
	if !strings.Contains(said(m), "Showing the newest 20") {
		t.Errorf("said:\n%s", said(m))
	}
}
