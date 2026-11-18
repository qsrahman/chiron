package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type echoArgs struct {
	Text  string `json:"text" jsonschema:"the text to echo"`
	Times int    `json:"times,omitempty" jsonschema:"how often to repeat it"`
}

func echo() Tool {
	return MustNew("echo", "Repeat text.", true, func(_ context.Context, a echoArgs) (string, error) {
		return strings.Repeat(a.Text, max(a.Times, 1)), nil
	})
}

func TestToolSpec(t *testing.T) {
	spec := echo().Spec().OfFunction
	if spec == nil || spec.Name != "echo" || spec.Description.Value != "Repeat text." {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	if spec.Strict.Value {
		t.Error("tools should not be strict, so optional arguments work")
	}

	params, _ := json.Marshal(spec.Parameters)
	for _, want := range []string{`"type":"object"`, `"text"`, `"the text to echo"`, `"times"`} {
		if !strings.Contains(string(params), want) {
			t.Errorf("schema %s is missing %s", params, want)
		}
	}
	// "text" has no omitempty so it is required; "times" has, so it is optional.
	if !strings.Contains(string(params), `"required":["text"]`) {
		t.Errorf("schema %s should require only text", params)
	}
}

func TestToolWithoutArgumentsHasProperties(t *testing.T) {
	spec := MustNew("ping", "No arguments.", true, func(context.Context, struct{}) (string, error) { return "pong", nil }).Spec().OfFunction
	if _, ok := spec.Parameters["properties"]; !ok {
		t.Errorf("schema %v needs a properties object", spec.Parameters)
	}
}

func TestToolRun(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		want      string
		wantErr   string
	}{
		{"decodes arguments", `{"text":"ab","times":2}`, "abab", ""},
		{"optional field may be missing", `{"text":"x"}`, "x", ""},
		{"empty arguments are allowed", ``, "", ""},
		{"bad json", `{"text":`, "", "invalid arguments"},
		{"wrong type", `{"text":5}`, "", "invalid arguments"},
		{"unknown field", `{"text":"x","tims":3}`, "", "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := echo().Run(context.Background(), tt.arguments)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Run() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestMustNewPanicsOnNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic for a non-struct Args type")
		}
	}()
	MustNew("bad", "x", true, func(context.Context, string) (string, error) { return "", nil })
}

func TestRegistry(t *testing.T) {
	r, err := NewRegistry(echo(), MustNew("ping", "p", true, func(context.Context, struct{}) (string, error) { return "", nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("echo"); !ok {
		t.Error("echo not found")
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("unexpected tool found")
	}
	specs := r.Specs()
	if len(specs) != 2 || specs[0].OfFunction.Name != "echo" || specs[1].OfFunction.Name != "ping" {
		t.Errorf("specs not in registration order: %+v", specs)
	}

	if _, err := NewRegistry(echo(), echo()); err == nil {
		t.Error("duplicate names should be rejected")
	}
	if got := (&Registry{}).Specs(); len(got) != 0 {
		t.Errorf("zero Registry has specs: %v", got)
	}
}

func TestValidate(t *testing.T) {
	v := echo().(Validator)
	tests := []struct {
		name, arguments string
		wantErr         bool
	}{
		{"valid", `{"text":"a"}`, false},
		{"optional field left out", `{"text":"a","times":2}`, false},
		{"required field missing", `{"times":2}`, true},
		{"empty arguments", ``, true},
		{"wrong type", `{"text":5}`, true},
		{"not JSON", `{"text":`, true},
		{"not an object", `["a"]`, true},
		{"unknown field", `{"text":"a","tiems":2}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := v.Validate(tt.arguments); (err != nil) != tt.wantErr {
				t.Errorf("Validate(%q) = %v, wantErr %v", tt.arguments, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAcceptsNoArgumentsForAToolWithNone(t *testing.T) {
	none := MustNew("none", "Takes nothing.", true, func(context.Context, struct{}) (string, error) { return "", nil })
	if err := none.(Validator).Validate(``); err != nil {
		t.Errorf("Validate(\"\") = %v", err)
	}
}
