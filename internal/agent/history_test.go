package agent

import (
	"testing"
)

func call(id string) item   { return item{kind: kindCall, callID: id} }
func output(id string) item { return item{kind: kindCallOutput, callID: id} }
func reasoning() item       { return item{kind: "reasoning"} }

func TestSaveKeepsToolWorkOnlyForTheNewestTurn(t *testing.T) {
	var h history
	turn1 := []item{userItem("q1"), reasoning(), call("a"), output("a"), assistantItem("r1")}
	h.save(turn1)
	if got := kinds(h.items); got != "user,reasoning,function_call,function_call_output,assistant" {
		t.Fatalf("after one turn the whole turn is kept, got %s", got)
	}

	turn2 := append(h.begin("q2"), assistantItem("r2"))
	h.save(turn2)
	// Turn 1 keeps its messages only; turn 2, the newest, keeps all it has (here, no tool work).
	if got := kinds(h.items); got != "user,assistant,user,assistant" {
		t.Errorf("after two turns = %s, want the messages of both", got)
	}
}

func TestSaveDropsCallsAndOutputsTogether(t *testing.T) {
	var h history
	h.save([]item{userItem("q1"), call("a"), call("b"), output("a"), output("b"), assistantItem("r1")})
	h.save(append(h.begin("q2"), call("c"), output("c"), assistantItem("r2")))

	for _, it := range h.items {
		if it.kind == kindCall || it.kind == kindCallOutput {
			if it.callID != "c" {
				t.Errorf("the older turn's %s %q should be gone", it.kind, it.callID)
			}
		}
	}
	if got := kinds(h.items); got != "user,assistant,user,function_call,function_call_output,assistant" {
		t.Errorf("history = %s", got)
	}
}

func TestBeginDoesNotChangeTheHistory(t *testing.T) {
	var h history
	h.save([]item{userItem("q1"), assistantItem("r1")})

	turn := h.begin("q2")

	if len(turn) != 3 || len(h.items) != 2 {
		t.Errorf("turn has %d items and history %d: begin must work on a copy", len(turn), len(h.items))
	}
	turn[0] = userItem("changed")
	if h.items[0].kind != kindUser {
		t.Error("the copy shares memory with the history")
	}
}

func TestAnswerOpenCallsAddsOnlyWhatIsMissing(t *testing.T) {
	items := []item{userItem("q"), call("a"), call("b"), call("c"), output("b")}

	got := answerOpenCalls(items)

	if len(got) != len(items)+2 {
		t.Fatalf("got %d items, want %d: %s", len(got), len(items)+2, kinds(got))
	}
	answered := map[string]int{}
	for _, it := range got {
		if it.kind == kindCallOutput {
			answered[it.callID]++
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if answered[id] != 1 {
			t.Errorf("call %s has %d outputs, want exactly 1", id, answered[id])
		}
	}
}
