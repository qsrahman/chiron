package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3/option"
)

const (
	maxErrorBody    = 64 << 10 // how much of an error response is read
	maxErrorMessage = 1000     // how much of what a server says about an error is kept, in characters
)

// ReadableErrors is a client middleware that rewrites the body of an error response into OpenAI's shape. The library
// reads only the "error" member: for a compatible server that puts text there it loses the HTTP status behind a JSON
// error, and for one that puts its message elsewhere it keeps no message at all.
func ReadableErrors(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if err != nil || res.StatusCode < http.StatusBadRequest {
		return res, err
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	_ = res.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the error response: %w", err)
	}

	rewritten := openAIError(body)
	res.Body = io.NopCloser(bytes.NewReader(rewritten))
	res.ContentLength = int64(len(rewritten))
	res.Header.Del("Content-Length")
	return res, nil
}

// openAIError is {"error": {message, code, type}} for whatever a server sent.
func openAIError(body []byte) []byte {
	var parsed any
	if json.Unmarshal(body, &parsed) != nil {
		parsed = string(body) // plain text
	}
	out := map[string]string{"message": serverMessage(parsed, 0)}
	if object, ok := parsed.(map[string]any); ok {
		if inner, ok := object["error"].(map[string]any); ok {
			object = inner
		}
		// Only text: some servers repeat the HTTP status here as a number.
		out["code"], _ = object["code"].(string)
		out["type"], _ = object["type"].(string)
	}
	rewritten, err := json.Marshal(map[string]any{"error": out})
	if err != nil {
		return body
	}
	return rewritten
}

// serverMessage is what a server says about an error, or "" if nothing is found. OpenAI puts it in error.message;
// compatible servers also send {"error": "..."}, detail (text, or a list with msg), an errors list, a list around the
// whole, or plain text. A web page, as a proxy sends, is not a message.
func serverMessage(v any, depth int) string {
	if depth > 4 { // the shapes above nest no deeper
		return ""
	}
	switch v := v.(type) {
	case string:
		text := strings.TrimSpace(v)
		if strings.HasPrefix(text, "<") {
			return ""
		}
		if runes := []rune(text); len(runes) > maxErrorMessage {
			return string(runes[:maxErrorMessage])
		}
		return text
	case []any:
		if len(v) > 0 {
			return serverMessage(v[0], depth+1)
		}
	case map[string]any:
		for _, key := range []string{"message", "error", "detail", "errors", "msg"} {
			if found := serverMessage(v[key], depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}
