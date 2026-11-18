// Package tools defines what the model can call. Built-in tools and MCP tools both implement Tool,
// so the agent loop treats them the same.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

// Tool is a function the model may call.
type Tool interface {
	Name() string
	// Spec is the function definition sent to the model.
	Spec() responses.ToolUnionParam
	// ReadOnly reports that the tool changes nothing outside the process. Read-only calls from one
	// model turn run concurrently; all others run one at a time, in the order the model made them.
	ReadOnly() bool
	// Run executes one call. An error is not fatal: the agent hands its text back to the model
	// so it can correct itself.
	Run(ctx context.Context, arguments string) (string, error)
}

// Prompt is what the user is shown before a call to a tool that is not read-only runs.
type Prompt struct {
	Summary  string // one line: "Run a shell command"
	Details  string // what is about to happen: a command, a diff, the parameters
	GrantKey string // what "allow for session" covers: calls with the same key; empty covers the whole tool
}

// Describer is implemented by a tool that writes its own prompt. For any other tool the agent shows the
// arguments as they are.
type Describer interface {
	Describe(arguments string) Prompt
}

// Validator is implemented by a tool that can check arguments before the user is asked about the call, so that
// a call that could not run is never put to the user.
type Validator interface {
	Validate(arguments string) error
}

// typedTool adapts a plain Go function taking an Args struct into a Tool.
type typedTool[Args any] struct {
	name     string
	readOnly bool
	spec     responses.ToolUnionParam
	schema   *jsonschema.Resolved
	run      func(ctx context.Context, args Args) (string, error)
}

// MustNew builds a Tool from a function. The JSON schema sent to the model comes from the Args struct
// (use `json` tags for names and `jsonschema` tags for descriptions), and the model's arguments are
// decoded into it before run is called. It panics if Args cannot be described as a JSON schema, which is
// a programming error that any test touching the tool will find, like regexp.MustCompile.
func MustNew[Args any](name, description string, readOnly bool, run func(ctx context.Context, args Args) (string, error)) Tool {
	params, schema, err := schemaFor[Args]()
	if err != nil {
		panic(fmt.Sprintf("tool %q: %v", name, err))
	}
	spec := responses.ToolParamOfFunction(name, params, false) // not strict: optional fields are allowed
	spec.OfFunction.Description = openai.String(description)
	return typedTool[Args]{name: name, readOnly: readOnly, spec: spec, schema: schema, run: run}
}

func (t typedTool[Args]) Name() string                   { return t.name }
func (t typedTool[Args]) Spec() responses.ToolUnionParam { return t.spec }
func (t typedTool[Args]) ReadOnly() bool                 { return t.readOnly }

// Validate checks the arguments against the schema sent to the model: a JSON object with the required fields.
func (t typedTool[Args]) Validate(arguments string) error {
	var instance any = map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &instance); err != nil {
			return fmt.Errorf("not valid JSON: %w", err)
		}
	}
	return t.schema.Validate(instance)
}

func (t typedTool[Args]) Run(ctx context.Context, arguments string) (string, error) {
	var args Args
	if arguments != "" {
		// Unknown fields are rejected so the model learns about a misspelt argument instead of silently
		// getting default behaviour.
		dec := json.NewDecoder(strings.NewReader(arguments))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	return t.run(ctx, args)
}

// schemaFor returns the JSON schema of Args as the generic map the API client expects, and the same schema
// ready to validate arguments with.
func schemaFor[Args any]() (map[string]any, *jsonschema.Resolved, error) {
	schema, err := jsonschema.For[Args](nil)
	if err != nil {
		return nil, nil, fmt.Errorf("building schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving schema: %w", err)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding schema: %w", err)
	}
	var params map[string]any
	if err := json.Unmarshal(data, &params); err != nil {
		return nil, nil, fmt.Errorf("decoding schema: %w", err)
	}
	if params["type"] != "object" {
		return nil, nil, fmt.Errorf("Args must be a struct, got schema type %v", params["type"])
	}
	// The API rejects an object schema without "properties", which a tool with no arguments would have.
	if _, ok := params["properties"]; !ok {
		params["properties"] = map[string]any{}
	}
	return params, resolved, nil
}
