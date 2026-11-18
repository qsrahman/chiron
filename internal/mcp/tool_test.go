package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qsrahman/desk-agent/internal/tools"
)

// adapted returns the adapter for one tool of an in-memory server.
func adapted(t *testing.T, toolName string, trusted bool) *tool {
	t.Helper()
	s := connected(t, "srv")
	for _, remote := range s.tools {
		if remote.Name == toolName {
			a, err := newTool(s, remote, apiName("srv", toolName), trusted)
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
	}
	t.Fatalf("the test server has no tool %q", toolName)
	return nil
}

func TestAdapterDescribesItselfToTheAPI(t *testing.T) {
	a := adapted(t, "echo", false)

	spec := a.Spec().OfFunction
	if spec.Name != "srv__echo" || spec.Description.Value != "Repeat the text." {
		t.Errorf("spec = %+v", spec)
	}
	if spec.Parameters["type"] != "object" || spec.Parameters["properties"] == nil {
		t.Errorf("parameters = %v", spec.Parameters)
	}
	if a.Name() != "srv__echo" || a.Title() != "srv → echo" {
		t.Errorf("name %q, title %q", a.Name(), a.Title())
	}
}

func TestAdapterNeverClaimsToBeReadOnlyAndTrustsOnlyWhatIsConfigured(t *testing.T) {
	if a := adapted(t, "echo", false); a.ReadOnly() || a.Trusted() {
		t.Error("an MCP tool is neither read-only nor trusted by default")
	}
	if a := adapted(t, "echo", true); a.ReadOnly() || !a.Trusted() {
		t.Error("an autoApprove tool is trusted but still runs alone")
	}
}

func TestAdapterIsAToolWithItsOwnPrompt(t *testing.T) {
	var a tools.Tool = adapted(t, "echo", false)

	if _, ok := a.(tools.Describer); !ok {
		t.Fatal("the adapter should describe its own calls")
	}
	p := a.(tools.Describer).Describe(`{"text":"hi"}`)
	if p.Summary != "srv → echo" || p.Details != "text: hi" || p.Source != "in memory" || p.GrantKey != "" {
		t.Errorf("prompt = %+v", p)
	}
}

func TestAdapterValidatesTheShapeOfTheArguments(t *testing.T) {
	a := adapted(t, "echo", false)
	tests := []struct {
		arguments string
		wantErr   bool
	}{
		{`{"text":"x"}`, false},
		{`{"text":5}`, false}, // types are left to the server
		{`{}`, true},          // the required key is missing
		{``, true},
		{`["x"]`, true},
		{`not json`, true},
	}
	for _, tt := range tests {
		if err := a.Validate(tt.arguments); (err != nil) != tt.wantErr {
			t.Errorf("Validate(%q) = %v, wantErr %v", tt.arguments, err, tt.wantErr)
		}
	}
}

func TestAdapterCallsTheServer(t *testing.T) {
	out, err := adapted(t, "echo", false).Run(context.Background(), `{"text":"hi"}`)

	if err != nil || out != "echo: hi" {
		t.Errorf("Run = %q, %v", out, err)
	}
}

func TestAnErrorResultBecomesAToolError(t *testing.T) {
	_, err := adapted(t, "fail", false).Run(context.Background(), `{"text":"x"}`)

	if err == nil || err.Error() != "it broke" {
		t.Errorf("err = %v, want the server's text as an error", err)
	}
}

func TestCancellingStopsTheCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapted(t, "echo", false).Run(ctx, `{"text":"x"}`)

	if err == nil {
		t.Error("a cancelled context should fail the call")
	}
}

func TestAToolWithoutASchemaGetsAnEmptyObject(t *testing.T) {
	a, err := newTool(&server{name: "s"}, &sdk.Tool{Name: "bare"}, "s__bare", false)
	if err != nil {
		t.Fatal(err)
	}
	props, ok := a.Spec().OfFunction.Parameters["properties"].(map[string]any)
	if a.Spec().OfFunction.Parameters["type"] != "object" || !ok || len(props) != 0 {
		t.Errorf("parameters = %v", a.Spec().OfFunction.Parameters)
	}
}

// What the dialog shows is what the tool receives: the model's JSON escapes are decoded.
func TestThePromptShowsTheArgumentsDecoded(t *testing.T) {
	a := adapted(t, "echo", false)

	p := a.Describe(`{"text":"pwd \u0026\u0026 ls -la","times":2}`)

	if p.Details != "text: pwd && ls -la\ntimes: 2" {
		t.Errorf("details = %q", p.Details)
	}
}
