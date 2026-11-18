package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/qsrahman/desk-agent/internal/tools"
)

// callTimeout bounds one call to a server; cancelling the turn ends it sooner.
const callTimeout = 2 * time.Minute

// tool presents one tool of an MCP server as a tools.Tool. It never claims to be read-only: the server's own hint
// says nothing a user can rely on, so every call asks unless the configuration trusts the tool.
type tool struct {
	server   *server
	remote   *sdk.Tool
	name     string         // what the model calls it
	schema   map[string]any // the arguments, as the API takes them
	required []string
	trusted  bool
}

// newTool adapts a remote tool. It fails for a tool whose arguments are not an object, which the API would refuse.
func newTool(s *server, remote *sdk.Tool, name string, trusted bool) (*tool, error) {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if remote.InputSchema != nil {
		data, err := json.Marshal(remote.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("reading the arguments: %w", err)
		}
		if err := json.Unmarshal(data, &schema); err != nil || schema["type"] != "object" {
			return nil, errors.New("the arguments are not a JSON object")
		}
		if _, ok := schema["properties"]; !ok {
			schema["properties"] = map[string]any{} // the API rejects an object without it
		}
	}
	t := &tool{server: s, remote: remote, name: name, schema: schema, trusted: trusted}
	if list, ok := schema["required"].([]any); ok {
		for _, key := range list {
			if k, ok := key.(string); ok {
				t.required = append(t.required, k)
			}
		}
	}
	return t, nil
}

func (t *tool) Name() string   { return t.name }
func (t *tool) ReadOnly() bool { return false }
func (t *tool) Trusted() bool  { return t.trusted }

// Title is what the cards and the approval prompt show instead of the name made for the API.
func (t *tool) Title() string { return t.server.name + " → " + t.remote.Name }

func (t *tool) Spec() responses.ToolUnionParam {
	spec := responses.ToolParamOfFunction(t.name, t.schema, false)
	spec.OfFunction.Description = openai.String(t.remote.Description)
	return spec
}

// Validate checks that the arguments are a JSON object with the keys the schema requires. Types are left to the
// server, whose own error goes back to the model as text.
func (t *tool) Validate(arguments string) error {
	args, err := parseArguments(arguments)
	if err != nil {
		return err
	}
	for _, key := range t.required {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("the required argument %q is missing", key)
		}
	}
	return nil
}

// Describe is the approval prompt: which tool of which server, with what arguments, and how the server is run.
func (t *tool) Describe(arguments string) tools.Prompt {
	return tools.Prompt{Summary: t.Title(), Details: tools.FormatArguments(arguments), Source: t.server.source}
}

func (t *tool) Run(ctx context.Context, arguments string) (string, error) {
	args, err := parseArguments(arguments)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	session, err := t.server.live(ctx)
	if err != nil {
		return "", err
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: t.remote.Name, Arguments: args})
	if err != nil {
		if t.server.lost(ctx, session, err) {
			err = fmt.Errorf("%w: the connection to %s was lost, and the next call starts it again", err, t.server.name)
		}
		return "", err
	}
	text := resultText(result)
	if result.IsError {
		if text == "" {
			text = "the tool reported an error"
		}
		return "", errors.New(text)
	}
	return text, nil
}

func parseArguments(arguments string) (map[string]any, error) {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args == nil {
		return nil, errors.New("the arguments are not a JSON object")
	}
	return args, nil
}
