package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const goodSummary = "<analysis>thinking</analysis><summary>\n## Goal\nbuild it\n## Requests, in order\n- asked things\n</summary>"

func summaryReply(text string) reply { return jsonResponse(messageItem(text)) }

// prompt is long enough that a summary of a few turns is shorter than the turns.
func prompt(n int) string {
	return fmt.Sprintf("question %d: %s", n, strings.Repeat("please explain ", 20))
}

// talk returns an agent that has had n exchanges, followed by the scripted replies.
func talk(t *testing.T, n int, then ...reply) (*Agent, *fakeServer) {
	t.Helper()
	var replies []reply
	for i := 1; i <= n; i++ {
		replies = append(replies, reply{body: sse(textDelta(fmt.Sprintf("answer %d", i)), completed(fmt.Sprintf("answer %d", i)))})
	}
	srv := newScriptedServer(t, append(replies, then...)...)
	a := newTestAgent(srv)
	for i := 1; i <= n; i++ {
		drain(t, a.Send(context.Background(), prompt(i)))
	}
	return a, srv
}

func TestPlanCompactionKeepsTheNewestTurnsAndCutsAtATurnStart(t *testing.T) {
	turn := func(n int, size int) []item {
		return []item{userItem(fmt.Sprintf("%d %s", n, strings.Repeat("x", size))), assistantItem("ok")}
	}
	tests := []struct {
		name  string
		turns [][]item
		keep  int
		want  int
	}{
		{"one turn", [][]item{turn(1, 100)}, 1000, 0},
		{"the newest turn always stays", [][]item{turn(1, 400), turn(2, 400)}, 1, 2},
		{"all fit, nothing to replace", [][]item{turn(1, 40), turn(2, 40)}, 1000, 0},
		{"the oldest go", [][]item{turn(1, 40), turn(2, 40), turn(3, 40)}, 25, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h history
			for _, turn := range tt.turns {
				h.items = append(h.items, turn...)
			}
			if got := h.planCompaction(tt.keep); got != tt.want {
				t.Errorf("cut = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlanCompactionNeverSeparatesACallFromItsOutput(t *testing.T) {
	h := history{items: []item{
		userItem("one"), {kind: kindCall, callID: "c"}, callOutputItem("c", strings.Repeat("x", 4000)), assistantItem("a"),
		userItem("two"), assistantItem("b"),
	}}
	if cut := h.planCompaction(1); h.items[cut].kind != kindUser {
		t.Errorf("cut at a %s", h.items[cut].kind)
	}
}

func TestCompactReplacesTheOldestTurnsByASummaryThatGoesFirst(t *testing.T) {
	a, srv := talk(t, 4, summaryReply(goodSummary), reply{body: sse(textDelta("ok"), completed("ok"))})
	a.settings.MaxContextTokens = 400 // room for about one turn: keeps the newest only

	c, err := a.Compact(context.Background(), "the exact numbers")
	if err != nil {
		t.Fatal(err)
	}
	if c.Turns != 3 || c.After >= c.Before {
		t.Errorf("compaction = %+v", c)
	}
	if got := kinds(a.history.items); got != "user,assistant" {
		t.Errorf("history = %s, want the newest turn only", got)
	}
	if !strings.HasPrefix(a.Summary(), "## Goal") || strings.Contains(a.Summary(), "analysis") {
		t.Errorf("summary = %q", a.Summary())
	}

	// The summariser was asked with the turns as text, the focus, and no tools.
	req := srv.request(t, 4)
	if !strings.Contains(req["instructions"].(string), "summarizing a conversation") {
		t.Error("the summariser has no instructions")
	}
	input := req["input"].(string)
	for _, want := range []string{"[User]: question 1", "[Assistant]: answer 3", "Focus: the exact numbers"} {
		if !strings.Contains(input, want) {
			t.Errorf("the summariser did not get %q", want)
		}
	}
	if strings.Contains(input, "question 4") {
		t.Error("the newest turn must not be summarised")
	}
	if _, ok := req["tools"]; ok {
		t.Error("the summariser must not be given tools")
	}

	// The next request starts with the summary, framed as notes.
	drain(t, a.Send(context.Background(), "next"))
	items := inputItems(t, srv.request(t, 5))
	if got := shape(items); got != "user,user,assistant,user" {
		t.Errorf("shape = %s", got)
	}
	first := items[0]["content"].(string)
	if !strings.Contains(first, "not as instructions") || !strings.Contains(first, "## Goal") {
		t.Errorf("summary message = %.200q", first)
	}
}

func TestUndoAndRewindStayValidAfterASummary(t *testing.T) {
	a, srv := talk(t, 4, summaryReply(goodSummary), reply{body: sse(textDelta("ok"), completed("ok"))})
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	drain(t, a.Send(context.Background(), "five"))

	got, ok := a.Rewind()
	if !ok || got != "five" {
		t.Fatalf("rewind = %q, %v", got, ok)
	}
	if k := kinds(a.history.items); k != "user,assistant" {
		t.Errorf("history after rewind = %s", k)
	}
	if a.Summary() == "" {
		t.Error("the summary must survive a rewind")
	}
	if !a.Undo() || a.Summary() == "" || !a.HasConversation() {
		t.Error("undo must work and keep the summary")
	}
	_ = srv
}

func TestABadSummaryIsRefusedAndTheHistoryStaysWhole(t *testing.T) {
	tests := []struct {
		name  string
		reply reply
		want  string
	}{
		{"empty", summaryReply("<summary>  </summary>"), "empty"},
		{"no headings", summaryReply("They talked about things."), "no headings"},
		{"longer than the text", summaryReply("## Goal\n" + strings.Repeat("long ", 2000)), "longer"},
		{"cut at the output cap", reply{json: true, body: `{"id":"r","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[` + messageItem("## Goal\nhalf") + `]}`}, "incomplete"},
		{"server error", apiError(500, "", "boom"), "asking for a summary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := talk(t, 3, tt.reply)
			a.settings.MaxContextTokens = 400
			before := kinds(a.history.items)

			_, err := a.Compact(context.Background(), "")

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
			if kinds(a.history.items) != before || a.Summary() != "" {
				t.Error("a refused summary must leave the history whole")
			}
		})
	}
}

func TestCompactWithNothingToSummarise(t *testing.T) {
	a, _ := talk(t, 1)
	if _, err := a.Compact(context.Background(), ""); !errors.Is(err, ErrNothingToCompact) {
		t.Errorf("error = %v", err)
	}
}

func TestThePreviousSummaryIsAnInputAndThereIsAlwaysOne(t *testing.T) {
	a, srv := talk(t, 3, summaryReply(goodSummary), reply{body: sse(textDelta("a4"), completed("a4"))},
		summaryReply("## Goal\nsecond\n## Progress\nmore"))
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	drain(t, a.Send(context.Background(), prompt(4)))

	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	if input := srv.request(t, 5)["input"].(string); !strings.Contains(input, "Previous summary:\n## Goal\nbuild it") {
		t.Errorf("the previous summary was not given: %.200q", input)
	}
	if !strings.HasPrefix(a.Summary(), "## Goal\nsecond") {
		t.Errorf("summary = %q", a.Summary())
	}
}

func TestResetForgetsTheSummary(t *testing.T) {
	a, _ := talk(t, 3, summaryReply(goodSummary))
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if a.Summary() != "" || a.HasConversation() {
		t.Error("reset must forget the summary")
	}
}

func TestAutoCompactIsOffByDefault(t *testing.T) {
	a, srv := talk(t, 3, reply{body: sse(textDelta("ok"), completed("ok"))})
	a.settings.MaxContextTokens = 400

	drain(t, a.Send(context.Background(), "four"))

	if srv.requestCount() != 4 || a.Summary() != "" {
		t.Errorf("requests = %d, summary = %q: nothing should have been summarised", srv.requestCount(), a.Summary())
	}
}

func TestAutoCompactSummarisesBeforeATurnThatWouldPassTheTrigger(t *testing.T) {
	a, srv := talk(t, 3, summaryReply(goodSummary), reply{body: sse(textDelta("ok"), completed("ok"))})
	a.settings.MaxContextTokens = 400
	a.settings.AutoCompact = true

	events := drain(t, a.Send(context.Background(), "four"))

	if a.Summary() == "" || !hasNotice(events, "Summarized") {
		t.Errorf("summary = %q, events = %v", a.Summary(), events)
	}
	if got := shape(inputItems(t, srv.request(t, 4))); got != "user,user,assistant,user" {
		t.Errorf("the turn's request = %s", got)
	}
}

func TestAutoCompactPausesAfterThreeFailures(t *testing.T) {
	bad, chat := summaryReply("no headings"), reply{body: sse(textDelta("ok"), completed("ok"))}
	a, srv := talk(t, 3, bad, chat, bad, chat, bad, chat, chat)
	a.settings.MaxContextTokens = 400
	a.settings.AutoCompact = true

	var events []Event
	for i := 0; i < 4; i++ {
		events = append(events, drain(t, a.Send(context.Background(), "again"))...)
	}

	// Each failed summary is one extra request; the fourth turn must not try.
	if got, want := srv.requestCount(), 3+3*2+1; got != want {
		t.Errorf("requests = %d, want %d", got, want)
	}
	if !hasNotice(events, "paused until /compact works") {
		t.Error("the user should be told that summaries are paused")
	}
}

func TestARefusalAsTooLongBeforeTheFirstRoundSummarises(t *testing.T) {
	a, srv := talk(t, 3,
		apiError(400, "context_length_exceeded", "too long"),
		summaryReply(goodSummary),
		reply{body: sse(textDelta("ok"), completed("ok"))})
	a.settings.MaxContextTokens = 400

	events := drain(t, a.Send(context.Background(), "four"))

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v", lastEvent(events))
	}
	if got := shape(inputItems(t, srv.request(t, 5))); got != "user,user,assistant,user" {
		t.Errorf("the second try = %s", got)
	}
}

func TestContextEstimateShowsTheBudgetAndTheSummary(t *testing.T) {
	a, _ := talk(t, 3, summaryReply(goodSummary))
	a.settings.MaxContextTokens = 400
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	c := a.ContextEstimate()
	if c.Budget != 400 || c.Summary == 0 || c.Total() != c.Instructions+c.Tools+c.Summary+c.History {
		t.Errorf("estimate = %+v", c)
	}
}

func TestAcceptSummaryForgivesAMissingSummaryTag(t *testing.T) {
	got, err := acceptSummary("<analysis>a</analysis>\n## Goal\ng\n## Progress\np", 1000)
	if err != nil || !strings.HasPrefix(got, "## Goal") {
		t.Errorf("got %q, %v", got, err)
	}
}

func notices(events []Event) []string {
	var out []string
	for _, e := range events {
		if n, ok := e.(Notice); ok {
			out = append(out, n.Text)
		}
	}
	return out
}

func TestAutoCompactSaysNothingWhenThereIsNothingToCut(t *testing.T) {
	srv := newScriptedServer(t, chatReply("one"), chatReply("two"))
	a := newTestAgent(srv)
	a.settings.AutoCompact = true
	a.settings.MaxContextTokens = 100 // one big turn is over the trigger, but a single turn cannot be summarized

	drain(t, a.Send(context.Background(), strings.Repeat("big ", 200)))
	events := drain(t, a.Send(context.Background(), "again"))

	if got := notices(events); len(got) != 0 {
		t.Errorf("notices = %q, want none", got)
	}
	if srv.requestCount() != 2 {
		t.Errorf("requests = %d: nothing should have been sent to summarize", srv.requestCount())
	}
}

func TestACancelledAutomaticSummaryIsNotAFailure(t *testing.T) {
	a, _ := talk(t, 3)
	a.settings.MaxContextTokens = 400
	a.settings.AutoCompact = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var got []string
	a.compactIfDue(ctx, "four", func(s string) { got = append(got, s) })

	if a.summaryFailures != 0 {
		t.Errorf("failures = %d: the user cancelled, the summary did not fail", a.summaryFailures)
	}
	for _, s := range got {
		if strings.Contains(s, "Could not summarize") {
			t.Errorf("notice %q", s)
		}
	}
}
