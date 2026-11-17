package tools

import (
	"fmt"

	"github.com/openai/openai-go/v3/responses"
)

// Registry looks tools up by name. The zero value is an empty registry.
type Registry struct {
	byName map[string]Tool
	list   []Tool // registration order, so the model always sees the same tool list
}

// NewRegistry returns a registry of the given tools. Names must be unique.
func NewRegistry(tools ...Tool) (*Registry, error) {
	r := &Registry{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if _, dup := r.byName[t.Name()]; dup {
			return nil, fmt.Errorf("duplicate tool name %q", t.Name())
		}
		r.byName[t.Name()] = t
		r.list = append(r.list, t)
	}
	return r, nil
}

// Get returns the tool with the given name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Specs returns the function definitions to send with a request.
func (r *Registry) Specs() []responses.ToolUnionParam {
	specs := make([]responses.ToolUnionParam, 0, len(r.list))
	for _, t := range r.list {
		specs = append(specs, t.Spec())
	}
	return specs
}
