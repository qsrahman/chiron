package mcp

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResultText(t *testing.T) {
	tests := []struct {
		name string
		in   *sdk.CallToolResult
		want string
	}{
		{"text parts are joined", &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "a"}, &sdk.TextContent{Text: "b"}}}, "a\nb"},
		{"structured content when there is no text", &sdk.CallToolResult{StructuredContent: map[string]any{"k": 1}}, "{\n  \"k\": 1\n}"},
		{"text wins over structured content", &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "t"}}, StructuredContent: map[string]any{"k": 1}}, "t"},
		{"an image becomes a placeholder", &sdk.CallToolResult{Content: []sdk.Content{&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 12*1024)}}}, "[image: image/png, 12 KB]"},
		{"audio too", &sdk.CallToolResult{Content: []sdk.Content{&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 300)}}}, "[audio: audio/wav, 300 bytes]"},
		{"a resource link keeps its address", &sdk.CallToolResult{Content: []sdk.Content{&sdk.ResourceLink{URI: "file:///a.txt", Name: "a"}}}, "[resource: file:///a.txt]"},
		{"an embedded text resource is included", &sdk.CallToolResult{Content: []sdk.Content{&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///a", Text: "inside"}}}}, "inside"},
		{"an embedded blob is a placeholder", &sdk.CallToolResult{Content: []sdk.Content{&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///b", MIMEType: "x/y", Blob: make([]byte, 2048)}}}}, "[resource: file:///b, 2 KB]"},
		{"nothing", &sdk.CallToolResult{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resultText(tt.in); got != tt.want {
				t.Errorf("resultText = %q, want %q", got, tt.want)
			}
		})
	}
}
