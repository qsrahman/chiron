package mcp

import (
	"fmt"
	"regexp"
)

const (
	// maxNameLength is the longest function name the API takes that every endpoint accepts.
	maxNameLength = 64
	// maxTools is the most tools one request may carry.
	maxTools = 128
)

var outsideName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// apiName makes the name the model calls a tool by: the server, two underscores and the tool, with what the API does
// not allow replaced by an underscore, cut to the length it allows.
func apiName(server, tool string) string {
	name := outsideName.ReplaceAllString(server+"__"+tool, "_")
	return name[:min(len(name), maxNameLength)]
}

// uniqueName returns name, or name with a number added when it is already used, still within the length limit.
// Two different pairs can give the same name ("a" and "b__c" against "a__b" and "c"), and the API rejects a
// request with two tools of one name.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("_%d", n)
		candidate := name[:min(len(name), maxNameLength-len(suffix))] + suffix
		if !used[candidate] {
			return candidate
		}
	}
}
