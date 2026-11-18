package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
)

// described is an error with a message written for the user that still unwraps to the original, so
// errors.Is and errors.As keep working.
type described struct {
	msg string
	err error
}

func (d *described) Error() string { return d.msg }
func (d *described) Unwrap() error { return d.err }

// statusHints say what to check for the usual causes of an API error.
var statusHints = map[int]string{
	http.StatusUnauthorized:    "Check the API key (OPENAI_API_KEY, or api_key in the config file).",
	http.StatusForbidden:       "Check the API key and that it may use this model.",
	http.StatusNotFound:        "Check the model and base_url in the config file.",
	http.StatusTooManyRequests: "The account is out of quota, or rate limited.",
}

var overflowWording = regexp.MustCompile(`(context|token).*(length|window|limit|exceed)`)

// isContextOverflow reports whether a server refused a request for being longer than its context window.
// Servers word it in their own way; OpenAI sends the code context_length_exceeded.
func isContextOverflow(err error) bool {
	var api *openai.Error
	if !errors.As(err, &api) || (api.StatusCode != http.StatusBadRequest && api.StatusCode != http.StatusRequestEntityTooLarge) {
		return false
	}
	if api.Code == "context_length_exceeded" {
		return true
	}
	text := strings.ToLower(api.Message)
	return strings.Contains(text, "too long") || overflowWording.MatchString(text)
}

// describeError returns err with a message for the user. The SDK's own text for an API error is only the
// HTTP status, so the server's message and code are added, with a hint for the usual causes. A cancellation is
// returned unchanged: it is not an error to explain.
func describeError(err error) error {
	var already *described
	var api *openai.Error
	var urlErr *url.Error
	switch {
	case errors.Is(err, context.Canceled), errors.As(err, &already):
		return err
	case errors.As(err, &api):
		message := api.Message
		if message == "" {
			message = http.StatusText(api.StatusCode)
		}
		code := ""
		if api.Code != "" {
			code = " (" + api.Code + ")"
		}
		text := fmt.Sprintf("API error %d%s: %s", api.StatusCode, code, message)
		hint := statusHints[api.StatusCode]
		if isContextOverflow(err) {
			hint = "The conversation is too long for the model."
		}
		if hint != "" {
			text += " " + hint
		}
		return &described{text, err}
	case errors.As(err, &urlErr):
		// The URL itself is left out of the text: it may carry what should not be shown.
		return &described{fmt.Sprintf("%v. Check base_url in the config file and the network.", urlErr.Err), err}
	}
	return err
}

// reasonOf is the short reason in the note that closes a turn that stopped after some work. The model reads
// it, so it names the kind of failure and nothing a server wrote.
func reasonOf(err error) string {
	var api *openai.Error
	var cut *streamCut
	switch {
	case errors.Is(err, context.Canceled):
		return "interrupted by the user"
	case errors.Is(err, errStreamIdle):
		return "the model stopped sending data"
	case errors.As(err, &api):
		return fmt.Sprintf("API error %d", api.StatusCode)
	case errors.As(err, &cut):
		return cut.reason
	}
	return "an error occurred"
}
