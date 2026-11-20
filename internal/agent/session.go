package agent

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/chiron/internal/session"
)

// saving is where the conversation is written after every change, and what identifies the file.
type saving struct {
	store    *session.Store
	workDir  string
	endpoint string // scheme and host of the server: reasoning items only replay to the endpoint that wrote them
	id       string // "" until there is something to save, so an empty run writes nothing
	created  time.Time
	title    string    // the first prompt of the conversation
	stamp    time.Time // modification time of the file as this process last wrote or read it
}

// SessionListLimit is how many sessions a list shows: the newest.
const SessionListLimit = 20

// EnableSessions makes the agent save the conversation after every turn and every command that changes it. The
// file is named when there is first something to save. A failed save is reported by the turn that made it; the next
// save writes the whole conversation again, so a command that cannot save loses nothing.
func (a *Agent) EnableSessions(store *session.Store, workDir, endpoint string) {
	a.saving = &saving{store: store, workDir: workDir, endpoint: endpoint}
}

// saveSession writes the conversation after a command changed it. Its errors are ignored on purpose: the next turn saves
// everything again and reports a failure then.
func (a *Agent) saveSession() { _, _ = a.persist() }

// persist writes the conversation to its file. If another process has written that file since this one did, the
// conversation goes on in a file of its own instead of overwriting the other's turns, and the note says so.
func (a *Agent) persist() (note string, err error) {
	s := a.saving
	if s == nil || (s.id == "" && !a.HasConversation()) {
		return "", nil
	}
	now := time.Now()
	if s.id != "" {
		if cur, err := s.store.Stamp(s.id); err == nil && !cur.IsZero() && !cur.Equal(s.stamp) {
			note = fmt.Sprintf("Another chiron has written session %s: this conversation goes on in a new file", s.id)
			s.id = ""
		}
	}
	if s.id == "" {
		s.id, s.created = session.NewID(now), now
	}
	if s.title == "" {
		s.title = a.firstPrompt()
	}
	f := session.File{
		ID: s.id, Dir: s.workDir, Model: a.settings.Model, Endpoint: s.endpoint,
		Created: s.created, Updated: now, Summary: a.history.summary, Title: s.title, Replaced: a.history.replaced,
	}
	f.Turns = f.Replaced
	for _, it := range a.history.items {
		raw, err := json.Marshal(it.param)
		if err != nil {
			return note, fmt.Errorf("encoding the conversation: %w", err)
		}
		f.Items = append(f.Items, session.Item{Kind: it.kind, CallID: it.callID, Raw: raw})
		if it.kind == kindUser {
			f.Turns++
		}
	}
	s.stamp, err = s.store.Save(f)
	return note, err
}

// firstPrompt is the first user message of the history, "" if there is none.
func (a *Agent) firstPrompt() string {
	for _, it := range a.history.items {
		if it.kind == kindUser {
			if f, ok := parse(it); ok {
				return messageText(f.Content)
			}
		}
	}
	return ""
}

// SessionID is the name of the file the conversation is saved in, or "" if it is not saved (yet).
func (a *Agent) SessionID() string {
	if a.saving == nil {
		return ""
	}
	return a.saving.id
}

// ResumeSession makes the saved session with this ID the current conversation, in place of the one in memory, which
// is already saved. Call it between turns. Nothing changes when the session cannot be read or does not hold up.
func (a *Agent) ResumeSession(id string) error {
	if a.saving == nil {
		return ErrSessionsOff
	}
	f, err := a.saving.store.Load(id)
	if err != nil {
		return err
	}
	return a.Resume(f, a.saving.endpoint)
}

// Resume makes a saved conversation the current one. Call it between turns. Reasoning items are left out
// when another model or server wrote them, because they only make sense to the one that did. A file whose calls and
// outputs do not match is refused, since the API would refuse the history.
func (a *Agent) Resume(f session.File, endpoint string) error {
	keepReasoning := f.Model == a.settings.Model && f.Endpoint == endpoint
	var items []item
	called := map[string]bool{}
	for _, raw := range f.Items {
		it, err := itemFromFile(raw)
		if err != nil {
			return fmt.Errorf("session %s: %w", f.ID, err)
		}
		switch it.kind {
		case kindReasoning:
			if !keepReasoning {
				continue
			}
		case kindCall:
			called[it.callID] = true
		case kindCallOutput:
			if !called[it.callID] {
				return fmt.Errorf("session %s: a result with no call (%s)", f.ID, it.callID)
			}
		}
		items = append(items, it)
	}
	a.history = history{items: answerOpenCalls(items), summary: f.Summary, replaced: f.Replaced}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].kind == kindUser {
			if p, ok := parse(items[i]); ok {
				a.history.lastPrompt, a.history.lastSaved = messageText(p.Content), true
			}
			break
		}
	}
	a.lastInput, a.summaryFailures = 0, 0
	if a.saving != nil {
		a.saving.id, a.saving.created = f.ID, f.Created // goes on in the same file
		a.saving.title = cmp.Or(f.Title, a.firstPrompt())
		a.saving.stamp, _ = a.saving.store.Stamp(f.ID)
	}
	return nil
}

// itemFromFile rebuilds an item. Messages and results are rebuilt from their text so that shortening and
// summaries treat them like new ones; the rest is replayed as it was written.
func itemFromFile(si session.Item) (item, error) {
	var f fields
	if err := json.Unmarshal(si.Raw, &f); err != nil {
		return item{}, fmt.Errorf("an item is damaged: %w", err)
	}
	switch si.Kind {
	case kindUser:
		return userItem(messageText(f.Content)), nil
	case kindAssistant:
		return assistantItem(messageText(f.Content)), nil
	case kindCallOutput:
		return callOutputItem(si.CallID, messageText(f.Output)), nil
	case kindReasoning, kindCall, "message":
		return item{kind: si.Kind, callID: si.CallID, param: param.Override[responses.ResponseInputItemUnionParam](si.Raw)}, nil
	}
	return item{}, fmt.Errorf("an item of unknown kind %q", si.Kind)
}

// Message is one thing said or done in the conversation, for showing it again after a resume.
type Message struct {
	Role      string // "user", "assistant" or "tool"
	Text      string // for the first two
	CallID    string // for a tool call
	Name      string
	Arguments string
	Output    string // what the call returned, "" if the call has no result in the history
	Title     string // what to show instead of Name, as for ToolStarted; empty if the tool is not known (yet)
	Outcome   Outcome
}

// Messages lists the conversation in order. Older turns hold only their messages; the newest holds its tool calls.
func (a *Agent) Messages() []Message {
	var out []Message
	at := map[string]int{} // call id to its place in out
	for _, it := range a.history.items {
		f, ok := parse(it)
		if !ok {
			continue
		}
		switch f.Type {
		case "message":
			role := "assistant"
			if f.Role == "user" {
				role = "user"
			}
			if text := strings.TrimSpace(messageText(f.Content)); text != "" {
				out = append(out, Message{Role: role, Text: text})
			}
		case kindCall:
			at[f.CallID] = len(out)
			out = append(out, Message{Role: "tool", CallID: f.CallID, Name: f.Name, Arguments: f.Arguments, Title: a.ToolTitle(f.Name)})
		case kindCallOutput:
			if i, ok := at[f.CallID]; ok {
				out[i].Output = messageText(f.Output)
				out[i].Outcome = outcomeOf(out[i].Output)
			}
		}
	}
	return out
}

// SessionInfo describes a saved session for a list.
type SessionInfo struct {
	ID      string
	Updated time.Time
	Prompt  string // the first thing the user asked
	Turns   int
	Current bool
}

// ErrSessionsOff is returned by ListSessions when saving is not enabled.
var ErrSessionsOff = errors.New("sessions are off (sessions: false in the config file, or --no-save)")

// ErrCurrentSession is returned by DeleteSession for the session the conversation is saved in: the next save would
// write its file again.
var ErrCurrentSession = errors.New("that is the session in use: /reset first, then delete it")

// DeleteSession deletes a saved session. The session in use cannot be deleted.
func (a *Agent) DeleteSession(id string) error {
	if a.saving == nil {
		return ErrSessionsOff
	}
	if id == a.saving.id {
		return ErrCurrentSession
	}
	return a.saving.store.Delete(id)
}

// ListSessions lists the saved sessions of this directory, the newest first and at most SessionListLimit of them.
func (a *Agent) ListSessions() ([]SessionInfo, error) {
	if a.saving == nil {
		return nil, ErrSessionsOff
	}
	infos, err := a.saving.store.List(func(i session.Info) bool { return i.Dir == a.saving.workDir }, SessionListLimit)
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, i := range infos {
		out = append(out, SessionInfo{ID: i.ID, Updated: i.Updated, Current: i.ID == a.saving.id, Prompt: i.Title, Turns: i.Turns})
	}
	return out, nil
}
