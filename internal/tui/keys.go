package tui

import "charm.land/bubbles/v2/key"

// keyMap holds the bindings used both to react to keys and to draw the help bar, so the two cannot drift.
type keyMap struct {
	send      key.Binding
	newline   key.Binding
	scroll    key.Binding
	interrupt key.Binding
	eof       key.Binding

	// Used while the suggestion list is open.
	up, down, complete, dismiss key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		newline:   key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"), key.WithHelp("ctrl+j", "new line")),
		scroll:    key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "scroll")),
		interrupt: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		eof:       key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "quit on an empty input")),
		up:        key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "previous")),
		down:      key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "next")),
		complete:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		dismiss:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
	}
}

// helpBindings returns what the help bar shows for the current state. Ctrl-C changes meaning with the
// state, so its description is set here.
func (k keyMap) helpBindings(busy, cancelling bool) []key.Binding {
	interrupt := k.interrupt
	switch {
	case cancelling:
		interrupt.SetHelp("ctrl+c", "quit now")
	case busy:
		interrupt.SetHelp("ctrl+c", "cancel")
	}
	if busy {
		return []key.Binding{interrupt, k.scroll}
	}
	return []key.Binding{k.send, k.newline, k.scroll, interrupt}
}
