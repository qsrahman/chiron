package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

const (
	// maxPrompts is how many prompts are remembered.
	maxPrompts = 1000
	// maxPromptBytes is the longest prompt that is remembered. A pasted file is not worth recalling, and a line that
	// long would be a burden to every start.
	maxPromptBytes = 64 << 10
)

// promptHistory is the prompts the user has sent, oldest first. With a path they live in a private file, one JSON
// string a line (a prompt may span lines); without one they are kept for this run only. A file that cannot be
// written costs the user the recall of the next run, nothing more, so write errors are ignored.
type promptHistory struct {
	path  string
	items []string
}

// loadPromptHistory reads the file; a missing or damaged one is an empty history. A file that has grown past twice
// the limit is rewritten with the newest prompts.
func loadPromptHistory(path string) *promptHistory {
	h := &promptHistory{path: path}
	if path == "" {
		return h
	}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()
	lines := 0
	// A reader, not a scanner: a scanner stops at a line longer than its buffer and everything after it would be lost.
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lines++
			var text string
			if json.Unmarshal(line, &text) == nil && text != "" && len(text) <= maxPromptBytes {
				h.items = append(h.items, text)
			}
		}
		if err != nil {
			break
		}
	}
	if len(h.items) > maxPrompts {
		h.items = slices.Clone(h.items[len(h.items)-maxPrompts:])
	}
	if lines > 2*maxPrompts {
		h.rewrite()
	}
	return h
}

// add remembers a prompt, unless it is empty, starts with a space or is the same as the last one.
func (h *promptHistory) add(text string) {
	if strings.HasPrefix(text, " ") {
		return // as in a shell: a prompt that starts with a space is not remembered, for what should leave no trace
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > maxPromptBytes || (len(h.items) > 0 && h.items[len(h.items)-1] == text) {
		return
	}
	h.items = append(h.items, text)
	if len(h.items) > maxPrompts {
		h.items = h.items[1:]
	}
	if h.path == "" {
		return
	}
	line, err := json.Marshal(text)
	if err != nil || os.MkdirAll(filepath.Dir(h.path), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (h *promptHistory) rewrite() {
	var b strings.Builder
	for _, text := range h.items {
		line, _ := json.Marshal(text)
		b.Write(line)
		b.WriteByte('\n')
	}
	if os.WriteFile(h.path, []byte(b.String()), 0o600) == nil {
		_ = os.Chmod(h.path, 0o600) // WriteFile keeps the mode of a file that already exists
	}
}

// recallState is where the user is in the history: the prompt shown, and its place. It is only in effect while
// the input still holds exactly that prompt, so editing it ends the walk.
type recallState struct {
	pos   int
	shown string
}

// recalling reports whether the input holds a recalled prompt that has not been edited.
func (m *Model) recalling() bool {
	return m.recall.shown != "" && m.input.Value() == m.recall.shown
}

// recallKey handles Up and Down for the history. From an empty input Up shows the newest prompt; while a recalled
// prompt is unedited Up goes one older, and Down one newer, and past the newest it empties the input again.
func (m *Model) recallKey(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, m.keys.up):
		if len(m.hist.items) == 0 {
			return false
		}
		pos := len(m.hist.items) - 1
		if m.recalling() {
			pos = max(0, m.recall.pos-1)
		}
		m.showRecalled(pos)
	case key.Matches(msg, m.keys.down) && m.recalling():
		if m.recall.pos+1 >= len(m.hist.items) {
			m.input.Reset()
			m.recall = recallState{}
			return true
		}
		m.showRecalled(m.recall.pos + 1)
	default:
		return false
	}
	return true
}

func (m *Model) showRecalled(pos int) {
	text := m.hist.items[pos]
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.recall = recallState{pos: pos, shown: text}
	m.suggest.dismissed = text // a recalled command must not open the list, which would take the arrow keys
}
