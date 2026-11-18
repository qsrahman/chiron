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

// typedTool adapts a plain Go function taking an Args struct into a Tool.
type typedTool[Args any] struct {
	name     string
	readOnly bool
	spec     responses.ToolUnionParam
	run      func(ctx context.Context, args Args) (string, error)
}

// MustNew builds a Tool from a function. The JSON schema sent to the model comes from the Args struct
// (use `json` tags for names and `jsonschema` tags for descriptions), and the model's arguments are
// decoded into it before run is called. It panics if Args cannot be described as a JSON schema, which is
// a programming error that any test touching the tool will find, like regexp.MustCompile.
func MustNew[Args any](name, description string, readOnly bool, run func(ctx context.Context, args Args) (string, error)) Tool {
	params, err := schemaFor[Args]()
	if err != nil {
		panic(fmt.Sprintf("tool %q: %v", name, err))
	}
	spec := responses.ToolParamOfFunction(name, params, false) // not strict: optional fields are allowed
	spec.OfFunction.Description = openai.String(description)
	return typedTool[Args]{name: name, readOnly: readOnly, spec: spec, run: run}
}

func (t typedTool[Args]) Name() string                   { return t.name }
func (t typedTool[Args]) Spec() responses.ToolUnionParam { return t.spec }
func (t typedTool[Args]) ReadOnly() bool                 { return t.readOnly }

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

// schemaFor returns the JSON schema of Args as the generic map the API client expects.
func schemaFor[Args any]() (map[string]any, error) {
	schema, err := jsonschema.For[Args](nil)
	if err != nil {
		return nil, fmt.Errorf("building schema: %w", err)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encoding schema: %w", err)
	}
	var params map[string]any
	if err := json.Unmarshal(data, &params); err != nil {
		return nil, fmt.Errorf("decoding schema: %w", err)
	}
	if params["type"] != "object" {
		return nil, fmt.Errorf("Args must be a struct, got schema type %v", params["type"])
	}
	// The API rejects an object schema without "properties", which a tool with no arguments would have.
	if _, ok := params["properties"]; !ok {
		params["properties"] = map[string]any{}
	}
	return params, nil
}
