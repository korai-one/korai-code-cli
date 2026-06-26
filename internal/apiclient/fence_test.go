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
// inlines its compacted schema, and that no tools yields an empty string.
func TestRenderToolInstructions(t *testing.T) {
	t.Parallel()

	if got := renderToolInstructions(nil); got != "" {
		t.Errorf("no tools should render empty, got %q", got)
	}

	got := renderToolInstructions([]ToolDef{
		{Name: "read_file", Description: "Read a file.", InputSchema: json.RawMessage(`{ "type": "object", "properties": { "path": {"type":"string"} } }`)},
		{Name: "bash", Description: "Run a command.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	for _, want := range []string{
		"<tool:tool_name>", // teaches the exact fence syntax via the example
		"## read_file",
		"Read a file.",
		`{"type":"object","properties":{"path":{"type":"string"}}}`, // compacted schema
		"## bash",
		"Run a command.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing %q\n---\n%s", want, got)
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
