package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/qsrahman/chiron/internal/tools"
)

func TestErrorBodiesOfCompatibleServersAreRead(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"openai", `{"error":{"message":"bad thing","code":"invalid_value"}}`, "API error 400 (invalid_value): bad thing"},
		{"error is text", `{"error":"bad thing"}`, "API error 400: bad thing"},
		{"flat", `{"object":"error","message":"bad thing","code":400}`, "API error 400: bad thing"},
		{"detail", `{"detail":"bad thing"}`, "API error 400: bad thing"},
		{"detail list", `{"detail":[{"loc":["body"],"msg":"bad thing"}]}`, "API error 400: bad thing"},
		{"errors list", `{"errors":[{"message":"bad thing"}]}`, "API error 400: bad thing"},
		{"list around it", `[{"error":{"message":"bad thing"}}]`, "API error 400: bad thing"},
		{"plain text", "bad thing\n", "API error 400: bad thing"},
		{"web page", "<html><body>nginx</body></html>", "API error 400: Bad Request"},
		{"nothing", "", "API error 400: Bad Request"},
		{"long", strings.Repeat("é", 2000), "API error 400: " + strings.Repeat("é", maxErrorMessage)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newScriptedServer(t, reply{status: 400, json: true, body: tt.body})
			client := openai.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0),
				option.WithMiddleware(ReadableErrors))
			a := New(client, testSettings("m"), "", &tools.Registry{})

			err := lastError(t, drain(t, a.Send(context.Background(), "hi")))

			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err, tt.want)
			}
		})
	}
}
