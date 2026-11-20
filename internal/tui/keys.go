package tui

import "charm.land/bubbles/v2/key"

// keyMap holds the bindings used both to react to keys and to draw the help bar, so the two cannot drift.
type keyMap struct {
	send      key.Binding
	newline   key.Binding
	scroll    key.Binding
	interrupt key.Binding
	eof       key.Binding
	search    key.Binding
	editor    key.Binding
	suspend   key.Binding

	// Used while the suggestion list is open.
	up, down, complete, dismiss key.Binding

	// Used while the session picker is open.
	remove key.Binding

	// Used while the approval modal is open.
	yes, always, no, prev, next key.Binding

	// Used while the input is empty: choosing a tool card, taking a queued prompt back, and recalling a prompt.
	cardNext, cardPrev, toggle, unqueue, history key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		newline:   key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"), key.WithHelp("ctrl+j", "new line")),
		scroll:    key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "scroll")),
		interrupt: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		eof:       key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "quit on an empty input")),
		search:    key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "search earlier prompts")),
		editor:    key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "edit the prompt in $EDITOR")),
		suspend:   key.NewBinding(key.WithKeys("ctrl+z"), key.WithHelp("ctrl+z", "suspend")),
		up:        key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "previous")),
		down:      key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "next")),
		complete:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		dismiss:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		cardNext:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "tool cards")),
		cardPrev:  key.NewBinding(key.WithKeys("shift+tab")),
		toggle:    key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "expand or collapse")),
		unqueue:   key.NewBinding(key.WithKeys("up")),
		history:   key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "recall earlier prompts")),
		remove:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		yes:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "allow")),
		always:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "allow for session")),
		no:        key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n", "deny")),
		prev:      key.NewBinding(key.WithKeys("left", "shift+tab"), key.WithHelp("←/→", "choose")),
		next:      key.NewBinding(key.WithKeys("right", "tab")),
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

// approvalHelp is what the help bar shows while the approval modal is open.
func (k keyMap) approvalHelp() []key.Binding {
	confirm := k.send
	confirm.SetHelp("enter", "confirm")
	return []key.Binding{k.yes, k.always, k.no, k.prev, confirm}
}

// cardHelp is what the help bar shows while a tool card is selected.
func (k keyMap) cardHelp() []key.Binding {
	next := k.cardNext
	next.SetHelp("tab", "next card")
	done := k.dismiss
	done.SetHelp("esc", "done")
	return []key.Binding{k.toggle, next, done}
}
