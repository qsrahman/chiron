package mcp

import (
	"encoding/json"
	"regexp"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var apiAccepts = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// FuzzNames holds that whatever a server calls its tools, the API accepts the names and no two are the same.
func FuzzNames(f *testing.F) {
	f.Add("a", "b__c", "a__b", "c")
	f.Add("s", "", "s", "")
	f.Add("s", "tööl/x y", "s", "t__l/x y")
	long := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	f.Add(long, "one", long, "two")
	f.Fuzz(func(t *testing.T, server1, tool1, server2, tool2 string) {
		used := map[string]bool{}
		for range 12 { // enough to reach two-digit suffixes
			for _, pair := range [][2]string{{server1, tool1}, {server2, tool2}} {
				name := uniqueName(apiName(pair[0], pair[1]), used)
				if !apiAccepts.MatchString(name) || used[name] {
					t.Fatalf("bad or repeated name %q for %q", name, pair)
				}
				used[name] = true
			}
		}
	})
}

// FuzzTool gives the adapter any schema and any arguments.
func FuzzTool(f *testing.F) {
	f.Add(`{"type":"object"}`, `{}`)
	f.Add(`{"type":"object","required":[1,"a"],"properties":null}`, `{"a":1}`)
	f.Add(`null`, `null`)
	f.Add(`{"type":["object"]}`, `[]`)
	f.Add(`"x"`, `{"a":`)
	f.Fuzz(func(t *testing.T, schema, arguments string) {
		var decoded any
		if json.Unmarshal([]byte(schema), &decoded) != nil {
			return
		}
		tool, err := newTool(&server{name: "s"}, &sdk.Tool{Name: "n", InputSchema: decoded}, "s__n", false)
		if err != nil {
			return
		}
		tool.Spec()
		tool.Describe(arguments)
		_ = tool.Validate(arguments)
	})
}
