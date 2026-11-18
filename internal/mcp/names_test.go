package mcp

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errAny = errors.New("failed")

func TestAPIName(t *testing.T) {
	long := strings.Repeat("x", 100)
	tests := []struct{ server, tool, want string }{
		{"docs", "search", "docs__search"},
		{"docs", "get.page/v2", "docs__get_page_v2"},
		{"my-server", "a_b", "my-server__a_b"},
		{"srv", "日本語", "srv_____"},
		{"srv", long, ("srv__" + long)[:64]},
	}
	for _, tt := range tests {
		if got := apiName(tt.server, tt.tool); got != tt.want {
			t.Errorf("apiName(%q, %q) = %q, want %q", tt.server, tt.tool, got, tt.want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	used := map[string]bool{"a__b__c": true, "a__b__c_2": true}
	if got := uniqueName("a__b__c", used); got != "a__b__c_3" {
		t.Errorf("got %q", got)
	}
	if got := uniqueName("free", used); got != "free" {
		t.Errorf("got %q", got)
	}
	full := strings.Repeat("y", 64)
	got := uniqueName(full, map[string]bool{full: true})
	if len(got) != 64 || !strings.HasSuffix(got, "_2") {
		t.Errorf("a name at the limit became %q (%d characters)", got, len(got))
	}
}

func remoteTools(names ...string) []*sdk.Tool {
	var out []*sdk.Tool
	for _, n := range names {
		out = append(out, &sdk.Tool{Name: n, InputSchema: map[string]any{"type": "object"}})
	}
	return out
}

func names(h *Hub, builtin []string) ([]string, []string) {
	list, notices := h.Tools(builtin)
	var out []string
	for _, t := range list {
		out = append(out, t.Name())
	}
	return out, notices
}

// "a" with "b__c" and "a__b" with "c" both give a__b__c; the API rejects two tools of one name.
func TestNamesAreUniqueAndFollowTheServerOrderNotTheSpeed(t *testing.T) {
	h := &Hub{servers: []*server{
		{name: "a", tools: remoteTools("b__c")},
		{name: "a__b", tools: remoteTools("c")},
	}}

	got, notices := names(h, nil)

	if strings.Join(got, ",") != "a__b__c,a__b__c_2" || len(notices) != 0 {
		t.Errorf("names = %v, notices = %v", got, notices)
	}
}

func TestAServerThatFailedOffersNothing(t *testing.T) {
	h := &Hub{servers: []*server{
		{name: "bad", tools: remoteTools("x"), err: errAny},
		{name: "good", tools: remoteTools("y")},
	}}

	if got, _ := names(h, nil); strings.Join(got, ",") != "good__y" {
		t.Errorf("names = %v", got)
	}
}

func TestABuiltInToolKeepsItsName(t *testing.T) {
	h := &Hub{servers: []*server{{name: "a", tools: remoteTools("b", "c")}}}

	got, notices := names(h, []string{"a__b"})

	if strings.Join(got, ",") != "a__c" || len(notices) != 1 || !strings.Contains(notices[0], "built-in") {
		t.Errorf("names = %v, notices = %v", got, notices)
	}
}

func TestTheToolCountIsLimited(t *testing.T) {
	var many []string
	for i := 0; i < maxTools+5; i++ {
		many = append(many, "t"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	h := &Hub{servers: []*server{{name: "s", tools: remoteTools(many...)}}}

	got, notices := names(h, []string{"builtin1", "builtin2"})

	if len(got) != maxTools-2 || len(notices) != 7 || !strings.Contains(notices[0], "at most 128") {
		t.Errorf("%d tools and %d notices; want %d tools within the limit", len(got), len(notices), maxTools-2)
	}
}

func TestAToolWhoseArgumentsAreNotAnObjectIsLeftOut(t *testing.T) {
	h := &Hub{servers: []*server{{name: "s", tools: []*sdk.Tool{
		{Name: "ok", InputSchema: map[string]any{"type": "object"}},
		{Name: "odd", InputSchema: map[string]any{"type": "string"}},
	}}}}

	got, notices := names(h, nil)

	if strings.Join(got, ",") != "s__ok" || len(notices) != 1 || !strings.Contains(notices[0], "odd") {
		t.Errorf("names = %v, notices = %v", got, notices)
	}
}
