package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// resultText turns what a tool returned into the text the model reads: the text parts joined, or the structured
// result when there is no text. Images and audio cannot be shown, so they become a short placeholder; a resource
// link keeps its address, and an embedded resource its text.
func resultText(r *sdk.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		switch c := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, c.Text)
		case *sdk.ImageContent:
			parts = append(parts, fmt.Sprintf("[image: %s, %s]", c.MIMEType, size(len(c.Data))))
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio: %s, %s]", c.MIMEType, size(len(c.Data))))
		case *sdk.ResourceLink:
			parts = append(parts, fmt.Sprintf("[resource: %s]", c.URI))
		case *sdk.EmbeddedResource:
			parts = append(parts, embedded(c.Resource))
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" && r.StructuredContent != nil {
		if data, err := json.MarshalIndent(r.StructuredContent, "", "  "); err == nil {
			return string(data)
		}
	}
	return text
}

func embedded(r *sdk.ResourceContents) string {
	switch {
	case r == nil:
		return "[resource]"
	case r.Text != "":
		return r.Text
	}
	return fmt.Sprintf("[resource: %s, %s]", r.URI, size(len(r.Blob)))
}

// size is a byte count for a placeholder: "512 bytes", "12 KB".
func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+1023)/1024)
}
