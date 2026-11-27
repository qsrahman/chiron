// Package agent runs conversations with the OpenAI Responses API and reports progress as events.
package agent

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/qsrahman/chiron/internal/tools"
)

// Settings are the options of a conversation that the user can set.
type Settings struct {
	Model           string
	ReasoningEffort string // "" sends none: models that do not reason reject the setting
	Streaming       bool   // false sends one request and shows the whole reply at once
	AutoApprove     bool   // allows every call without asking (--yes)
	MaxOutputTokens int    // most tokens the model may generate for one reply; 0 sends no cap

	MaxContextTokens int  // budget for a whole request; 0 means defaultMaxContextTokens, and Settings() returns that
	AutoCompact      bool // summarise the oldest turns before a turn that would pass compactAt of the budget
}

// Agent holds one conversation. It is not safe for concurrent use: wait for the channel returned by Send to
// close before calling Send again.
type Agent struct {
	client       openai.Client
	settings     Settings
	instructions string
	tools        *tools.Registry
	idleTimeout  time.Duration
	busyDelay    time.Duration // the pause before a retry after a fault that passes; a field so tests can shorten it
	history      history
	grants       map[grant]bool // what the user allowed for the session; lasts until the program exits
	lastInput    int64          // input tokens the server counted for the last request; 0 until one is known

	todos  *tools.TodoList // the model's checklist; cleared by Reset, saved with the session
	saving *saving         // nil when sessions are off

	summaryFailures int // automatic summaries in a row that were refused or failed

	loopWarnAt int    // the latest step may appear this often in the window before the model is told; tests raise it to leave the warning out
	loopNote   string // what the instructions say while the model repeats itself; only during a turn
}

// New returns an agent with an empty history. registry holds the tools the model may call; use
// &tools.Registry{} for none.
func New(client openai.Client, settings Settings, instructions string, registry *tools.Registry) *Agent {
	if settings.MaxContextTokens <= 0 {
		settings.MaxContextTokens = defaultMaxContextTokens
	}
	return &Agent{client: client, settings: settings, instructions: instructions, tools: registry, idleTimeout: defaultIdleTimeout, busyDelay: defaultBusyDelay, grants: map[grant]bool{}, todos: &tools.TodoList{}, loopWarnAt: loopWarnRepeats}
}

// Send starts a turn for the user's text and streams events on the returned channel.
//
// The channel is closed after a final Done or Error event. The caller must read it until it is closed; cancel
// ctx to stop a turn early. A turn that fails before any tool ran leaves the history as it was, so it can
// simply be retried; one that stopped after tool work is kept with a note (see turn.go).
func (a *Agent) Send(ctx context.Context, text string) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		err := a.run(ctx, text, events)
		// Saved before the last event, so a user who quits as soon as the turn ends loses nothing.
		note, saveErr := a.persist()
		if note != "" {
			events <- Notice{Text: note}
		}
		if saveErr != nil {
			events <- Notice{Text: "Could not save the session: " + saveErr.Error()}
		}
		if err != nil {
			events <- Error{Err: err}
			return
		}
		events <- Done{}
	}()
	return events
}
