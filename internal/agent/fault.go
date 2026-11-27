package agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
)

// streamFault is an error a server reported inside a response that it had accepted: an error event in the stream, or
// a response that failed. The HTTP status says nothing about it, so its code and words decide what can be done.
type streamFault struct {
	code, message string
	text          string // what the user is told
}

func (f *streamFault) Error() string { return f.text }

func (f *streamFault) overflow() bool { return saysOverflow(f.code, f.message) }

// transientCodes are the codes of faults that a second try may get past: OpenAI's, those of compatible servers, and
// the HTTP statuses some of them give as the code.
var transientCodes = map[string]bool{
	"server_error": true, "rate_limit_exceeded": true, "api_error": true, "overloaded_error": true,
	"rate_limit_error": true, "internal_server_error": true, "service_unavailable": true, "timeout": true,
	"429": true, "500": true, "502": true, "503": true, "504": true, "529": true,
}

var transientWording = regexp.MustCompile(`overloaded|rate.?limit|(service|temporarily) unavailable|(internal|server) error|timed? ?out`)

// transient reports whether the same request may work if sent again. A code decides when there is one; the words only
// when there is none.
func (f *streamFault) transient() bool {
	if f.code != "" {
		return transientCodes[f.code]
	}
	return transientWording.MatchString(strings.ToLower(f.message))
}

// newStreamError is the fault of an error event.
func newStreamError(code, message string) *streamFault {
	text := "Stream error: " + message
	if code != "" {
		text = "Stream error: " + code + ": " + message
	}
	return &streamFault{code: code, message: message, text: text}
}

// faultOfEvent reads the error the library found in the stream: an event whose data has an "error" member, which is
// an object with a code or a type and a message, or just text.
func faultOfEvent(e *ssestream.StreamError) *streamFault {
	var data struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(e.Event.Data, &data) != nil {
		return newStreamError("", e.Message)
	}
	code := ""
	if object, ok := data.Error.(map[string]any); ok {
		switch c := object["code"].(type) {
		case string:
			code = c
		case float64:
			code = strconv.Itoa(int(c))
		}
		if code == "" {
			code, _ = object["type"].(string)
		}
	}
	return newStreamError(code, serverMessage(data.Error, 0))
}

// retryableFault turns a response that failed for a fault that passes, or for being too long, into that fault, so that
// it can be acted on. Any other response is the caller's to judge, and gives nil.
func retryableFault(r *responses.Response) error {
	if r.Status != responses.ResponseStatusFailed {
		return nil
	}
	f := &streamFault{code: string(r.Error.Code), message: r.Error.Message, text: responseProblem(r)}
	if f.overflow() || f.transient() {
		return f
	}
	return nil
}

// retryReason reports whether err is worth sending the same request again, with the words that say why.
func retryReason(err error) (reason string, busy, ok bool) {
	var cut *streamCut
	var fault *streamFault
	switch {
	case errors.As(err, &cut):
		return cut.reason, false, true
	case errors.As(err, &fault) && !fault.overflow() && fault.transient():
		return fault.text, true, true
	}
	return "", false, false
}
