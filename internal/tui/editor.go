package tui

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg is the end of the editor: the text it left, or why there is none.
type editorDoneMsg struct {
	text string
	err  error
}

// prepareEditor writes the text to a private temporary file and returns the command that edits it. $VISUAL wins
// over $EDITOR, as in other programs. The value is a shell command line, as for git and less, so it may carry
// arguments and quotes ("code --wait", "'/Applications/My Editor/ed'"); the file is its last argument.
func prepareEditor(text string) (*exec.Cmd, string, error) {
	editor := strings.TrimSpace(cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editor == "" {
		return nil, "", errors.New("set $EDITOR (or $VISUAL) to edit a prompt in your editor")
	}
	f, err := os.CreateTemp("", "chiron-prompt-*.md") // mode 0600
	if err != nil {
		return nil, "", err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return nil, "", err
	}
	return exec.Command("sh", "-c", editor+` "$@"`, "sh", f.Name()), f.Name(), nil
}

// editInEditor hands the terminal to the editor with the input's text, and sends the result back as a message.
func (m *Model) editInEditor() tea.Cmd {
	cmd, path, err := prepareEditor(m.input.Value())
	if err != nil {
		m.hint = capitalize(err.Error())
		return nil
	}
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		data, readErr := os.ReadFile(path)
		os.Remove(path)
		if runErr != nil {
			return editorDoneMsg{err: fmt.Errorf("the editor failed: %w", runErr)}
		}
		return editorDoneMsg{text: string(data), err: readErr}
	})
}

// finishEditor puts what the editor left into the input. It is not sent: the user reads it first.
func (m *Model) finishEditor(msg editorDoneMsg) {
	if msg.err != nil {
		m.hint = capitalize(msg.err.Error())
		return
	}
	m.input.SetValue(strings.TrimRight(msg.text, "\n"))
	m.input.CursorEnd()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
