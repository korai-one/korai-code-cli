package apiclient

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParseToolFences covers the happy path, multiple calls, surrounding prose,
// an empty body, and malformed (unterminated) fences left as text.
func TestParseToolFences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		wantClean string
		wantCalls []fenceCall
	}{
		{
			name:      "prose and one call",
			in:        "I'll read it.\n<tool:read_file>{\"path\":\"x\"}</tool>",
			wantClean: "I'll read it.",
			wantCalls: []fenceCall{{Name: "read_file", Input: json.RawMessage(`{"path":"x"}`)}},
		},
		{
			name:      "two calls",
			in:        "<tool:glob>{\"pattern\":\"**/*.go\"}</tool><tool:grep>{\"q\":\"foo\"}</tool>",
			wantClean: "",
			wantCalls: []fenceCall{
				{Name: "glob", Input: json.RawMessage(`{"pattern":"**/*.go"}`)},
				{Name: "grep", Input: json.RawMessage(`{"q":"foo"}`)},
			},
		},
		{
			name:      "empty body becomes empty object",
			in:        "<tool:list_files></tool>",
			wantClean: "",
			wantCalls: []fenceCall{{Name: "list_files", Input: json.RawMessage(`{}`)}},
		},
		{
			// Open-weight models often mirror the open tag and emit a named
			// close. It must parse identically to the bare close.
			name:      "named closing tag",
			in:        "I'll run it.\n<tool:RunCommand>{\"command\":\"ls\"}</tool:RunCommand>",
			wantClean: "I'll run it.",
			wantCalls: []fenceCall{{Name: "RunCommand", Input: json.RawMessage(`{"command":"ls"}`)}},
		},
		{
			name:      "mixed bare and named closes",
			in:        "<tool:glob>{\"pattern\":\"*\"}</tool><tool:Bash>{\"command\":\"ls\"}</tool:Bash>",
			wantClean: "",
			wantCalls: []fenceCall{
				{Name: "glob", Input: json.RawMessage(`{"pattern":"*"}`)},
				{Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
			},
		},
		{
			// "</toolbox>" is not a closer: the fence stays unterminated and is
			// left as text rather than being mis-closed.
			name:      "lookalike close is not a closer",
			in:        "before <tool:read_file>{\"path\":\"x\"}</toolbox>",
			wantClean: "before <tool:read_file>{\"path\":\"x\"}</toolbox>",
			wantCalls: nil,
		},
		{
			name:      "no fences",
			in:        "just text",
			wantClean: "just text",
			wantCalls: nil,
		},
		{
			name:      "unterminated fence kept as text",
			in:        "before <tool:read_file>{\"path\":\"x\"}",
			wantClean: "before <tool:read_file>{\"path\":\"x\"}",
			wantCalls: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clean, calls := parseToolFences(tc.in)
			if clean != tc.wantClean {
				t.Errorf("clean = %q, want %q", clean, tc.wantClean)
			}
			if diff := cmp.Diff(tc.wantCalls, calls); diff != "" {
				t.Errorf("calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRenderRoundTrip checks that a call rendered to a fence parses back to the
// same name and input — the property the history replay relies on.
func TestRenderRoundTrip(t *testing.T) {
	t.Parallel()

	fence := renderToolCallFence("edit_file", json.RawMessage(`{"path":"a.go","old":"x","new":"y"}`))
	clean, calls := parseToolFences("noted\n" + fence)
	if clean != "noted" {
		t.Errorf("clean = %q, want noted", clean)
	}
	if len(calls) != 1 || calls[0].Name != "edit_file" {
		t.Fatalf("calls = %+v, want one edit_file call", calls)
	}
	var got map[string]any
	if err := json.Unmarshal(calls[0].Input, &got); err != nil {
		t.Fatalf("input not json: %v", err)
	}
	if got["path"] != "a.go" || got["new"] != "y" {
		t.Errorf("round-tripped input = %+v", got)
	}
}

// TestRenderToolInstructions checks the prompt addendum names every tool and
// states its arguments as a compact signature, and that no tools yields an
// empty string.
func TestRenderToolInstructions(t *testing.T) {
	t.Parallel()

	if got := renderToolInstructions(nil); got != "" {
		t.Errorf("no tools should render empty, got %q", got)
	}

	got := renderToolInstructions([]ToolDef{
		{Name: "read_file", Description: "Read a file.", InputSchema: json.RawMessage(`{ "type": "object", "properties": { "path": {"type":"string","description":"file to read"} }, "required": ["path"], "additionalProperties": false }`)},
		{Name: "bash", Description: "Run a command.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	for _, want := range []string{
		"<tool:tool_name>", // teaches the exact fence syntax via the example
		"## read_file",
		"Read a file.",
		"Args: path*:string — file to read", // compact signature, not JSON Schema
		"## bash",
		"Run a command.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing %q\n---\n%s", want, got)
		}
	}
	// A property-less schema renders no Args line at all, and the JSON Schema
	// scaffolding never reaches the prompt.
	for _, unwanted := range []string{"additionalProperties", `"type":"object"`, "Args: \n"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("instructions should not contain %q\n---\n%s", unwanted, got)
		}
	}
}

// TestRenderSchema covers the compact signature renderer: required markers,
// the short type vocabulary, nesting, enums, and schemas with nothing to say.
func TestRenderSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", ``, ""},
		{"no properties", `{"type":"object"}`, ""},
		{"required marker", `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a"]}`, "a*:string; b:string"},
		{"type vocabulary", `{"properties":{"n":{"type":"integer"},"f":{"type":"number"},"ok":{"type":"boolean"},"xs":{"type":"array","items":{"type":"string"}}}}`, "n:int; f:number; ok:bool; xs:string[]"},
		{"enum", `{"properties":{"s":{"type":"string","enum":["a","b"]}}}`, "s:string(a|b)"},
		{"nested object array", `{"properties":{"items":{"type":"array","items":{"type":"object","properties":{"k":{"type":"string"}},"required":["k"]}}}}`, "items:{k*:string}[]"},
		{"trailing period dropped", `{"properties":{"a":{"type":"string","description":"does a thing."}}}`, "a:string — does a thing"},
		{"malformed", `{not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := renderSchema(json.RawMessage(tt.in)); got != tt.want {
				t.Errorf("renderSchema(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRenderSchemaPreservesOrder pins that arguments render in schema order,
// not the shuffle a map iteration would give.
func TestRenderSchemaPreservesOrder(t *testing.T) {
	t.Parallel()

	const schema = `{"type":"object","properties":{"zebra":{"type":"string"},"apple":{"type":"string"},"middle":{"type":"string"}}}`
	const want = "zebra:string; apple:string; middle:string"
	for i := 0; i < 20; i++ {
		if got := renderSchema(json.RawMessage(schema)); got != want {
			t.Fatalf("renderSchema = %q, want %q", got, want)
		}
	}
}

// TestRenderToolResultText covers the result and error labels.
func TestRenderToolResultText(t *testing.T) {
	t.Parallel()

	if got := renderToolResultText("grep", "3 matches", false); got != "[TOOL RESULT: grep]\n3 matches" {
		t.Errorf("result = %q", got)
	}
	if got := renderToolResultText("bash", "exit 1", true); got != "[TOOL ERROR: bash]\nexit 1" {
		t.Errorf("error = %q", got)
	}
	if got := renderToolResultText("", "x", false); !strings.HasPrefix(got, "[TOOL RESULT: tool]") {
		t.Errorf("empty name should fall back to 'tool', got %q", got)
	}
}
