package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/qsrahman/chiron/internal/tools"
)

func TestShortenKeepsTheStartAndTheEndAndSaysHowMuchIsHidden(t *testing.T) {
	text := strings.Repeat("a", keepHead) + strings.Repeat("m", 5000) + strings.Repeat("z", keepTail)
	got := shorten(text)
	if !strings.HasPrefix(got, strings.Repeat("a", keepHead)) || !strings.HasSuffix(got, strings.Repeat("z", keepTail)) {
		t.Error("the start and the end must stay")
	}
	if !strings.Contains(got, "[... 5000 characters hidden; call the tool again if you need them ...]") {
		t.Errorf("the marker is missing or wrong: %q", got[keepHead:keepHead+100])
	}
	if short := strings.Repeat("x", shortenOver); shorten(short) != short {
		t.Error("a text that is not over the limit must stay as it is")
	}
	if got := shorten(strings.Repeat("é", 20000)); strings.Contains(got, "�") {
		t.Error("the cut split a character")
	}
}

func outputsItems(texts ...string) []item {
	var items []item
	for i, s := range texts {
		items = append(items, callOutputItem(string(rune('a'+i)), s))
	}
	return items
}

func TestDropOutputsGoesOldestFirstAndNotPastUpto(t *testing.T) {
	big := strings.Repeat("x", 12000)
	items := outputsItems(big, big, big)
	freed := dropOutputs(items, 2, 1, false)
	if freed <= 0 {
		t.Fatal("nothing was freed")
	}
	first, _ := outputText(items[0])
	second, _ := outputText(items[1])
	third, _ := outputText(items[2])
	if !strings.Contains(first, "characters hidden") || second != big || third != big {
		t.Errorf("only the oldest result should be shortened (needed 1 token): %d %d %d chars", len(first), len(second), len(third))
	}
}

func TestDropOutputsUsesPlaceholdersOnlyWhenAllowed(t *testing.T) {
	small := strings.Repeat("x", 4000) // nothing to shorten
	items := outputsItems(small, small)
	if dropOutputs(items, 2, 10_000, false) != 0 {
		t.Error("without placeholders a short result stays")
	}
	if dropOutputs(items, 2, 10_000, true) == 0 {
		t.Fatal("with placeholders results are replaced")
	}
	for i, it := range items {
		if text, _ := outputText(it); text != droppedOutput {
			t.Errorf("item %d = %.30q, want the placeholder", i, text)
		}
		if it.callID != string(rune('a'+i)) {
			t.Errorf("item %d lost its call id", i)
		}
	}
}

func bigTool(size int) tools.Tool {
	return tools.MustNew("big", "returns a lot", true, func(context.Context, nameArgs) (string, error) {
		return strings.Repeat("x", size), nil
	})
}

func TestEarlierResultsAreShortenedWhenTheNextRequestWouldPassTheBudget(t *testing.T) {
	srv := newFakeServer(t,
		sse(completedWith(functionCall("c1", "big", nameArgs{"a"}))),
		sse(completedWith(functionCall("c2", "big", nameArgs{"b"}))),
		sse(textDelta("done"), completed("done")),
	)
	a := newAgentWithTools(t, srv, bigTool(15000))
	a.settings.MaxContextTokens = 6000 // two whole results are about 7,500 tokens

	events := drain(t, a.Send(context.Background(), "go"))

	if err, ok := lastEvent(events).(Error); ok {
		t.Fatalf("the turn failed: %v", err.Err)
	}
	outs := outputsOf(t, srv.request(t, 2))
	first, _ := outs[0]["output"].(string)
	second, _ := outs[1]["output"].(string)
	if !strings.Contains(first, "characters hidden") {
		t.Error("the earlier result should have been shortened")
	}
	if strings.Contains(second, "characters hidden") {
		t.Error("the latest result is what the model reads next and must stay whole")
	}
	if !hasNotice(events, "older tool results") {
		t.Error("the user should be told")
	}
}

func hasNotice(events []Event, part string) bool {
	for _, e := range events {
		if n, ok := e.(Notice); ok && strings.Contains(n.Text, part) {
			return true
		}
	}
	return false
}

func TestNothingIsShortenedUnderTheBudget(t *testing.T) {
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "big", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, bigTool(15000))

	drain(t, a.Send(context.Background(), "go"))

	if out, _ := outputsOf(t, srv.request(t, 1))[0]["output"].(string); strings.Contains(out, "hidden") {
		t.Error("a result under the budget was shortened")
	}
}

func TestATurnThatCannotFitIsKeptWithItsResultsDropped(t *testing.T) {
	srv := newFakeServer(t, callsThenAnswer(functionCall("c1", "big", nameArgs{"a"}))...)
	a := newAgentWithTools(t, srv, bigTool(15000))
	a.settings.MaxContextTokens = 600

	events := drain(t, a.Send(context.Background(), "go"))

	err := lastError(t, events)
	if !strings.Contains(err.Error(), "context budget") || !strings.Contains(err.Error(), "max_context_tokens") {
		t.Errorf("error = %v", err)
	}
	if srv.requestCount() != 1 {
		t.Errorf("the model was asked %d times, want 1", srv.requestCount())
	}
	if got := kinds(a.history.items); got != "user,function_call,function_call_output,assistant" {
		t.Errorf("history = %s", got)
	}
	if out, _ := outputText(a.history.items[2]); out != droppedOutput {
		t.Errorf("the kept result is %.40q, want the placeholder", out)
	}
	if !strings.Contains(lastNote(a), "its results passed the context budget") {
		t.Errorf("note = %q", lastNote(a))
	}
}

func TestARefusalAsTooLongShortensEarlierResultsAndTriesAgain(t *testing.T) {
	srv := newScriptedServer(t,
		reply{body: sse(completedWith(functionCall("c1", "big", nameArgs{"a"})))},
		apiError(400, "context_length_exceeded", "too long"),
		reply{body: sse(textDelta("done"), completed("done"))},
	)
	a := newAgentWithTools(t, srv, bigTool(20000))

	events := drain(t, a.Send(context.Background(), "go"))

	if _, ok := lastEvent(events).(Done); !ok {
		t.Fatalf("last event = %#v", lastEvent(events))
	}
	if out, _ := outputsOf(t, srv.request(t, 2))[0]["output"].(string); !strings.Contains(out, "characters hidden") {
		t.Error("the second try should carry a shorter result")
	}
	if got := retries(events); len(got) != 1 || !strings.Contains(got[0].Reason, "too long for the model") {
		t.Errorf("the user should be told: %+v", got)
	}
}

func TestARefusalIsRetriedOnlyOnce(t *testing.T) {
	srv := newScriptedServer(t,
		reply{body: sse(completedWith(functionCall("c1", "big", nameArgs{"a"})))},
		apiError(400, "context_length_exceeded", "too long"),
	)
	a := newAgentWithTools(t, srv, bigTool(20000))

	events := drain(t, a.Send(context.Background(), "go"))

	lastError(t, events)
	if srv.requestCount() != 3 {
		t.Errorf("requests = %d, want 3 (the first round, the refused one, one more)", srv.requestCount())
	}
}

func TestTheServersCountCorrectsTheEstimate(t *testing.T) {
	// The estimate says a few hundred tokens; the server counted the first request as far larger.
	counted := terminal("response.completed", "completed", `,"usage":{"input_tokens":5000,"output_tokens":10,"total_tokens":5010}`,
		functionCall("c1", "big", nameArgs{"a"}))
	srv := newFakeServer(t, sse(counted), sse(textDelta("done"), completed("done")))
	a := newAgentWithTools(t, srv, bigTool(9000)) // about 2,250 tokens
	a.settings.MaxContextTokens = 7000

	events := drain(t, a.Send(context.Background(), "go"))

	if out, _ := outputsOf(t, srv.request(t, 1))[0]["output"].(string); !strings.Contains(out, "characters hidden") {
		t.Error("5,000 counted plus the result passes 7,000: the result should have been shortened")
	}
	if !hasNotice(events, "this round's tool results shortened") {
		t.Error("the user should be told")
	}
}
