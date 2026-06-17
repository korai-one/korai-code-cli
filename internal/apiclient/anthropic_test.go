package apiclient

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestConvertTools verifies that a full JSON Schema object is decomposed into
// the properties and required list the API expects.
func TestConvertTools(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to read"}
		},
		"required": ["path"],
		"additionalProperties": false
	}`)

	got, err := convertTools([]ToolDef{{
		Name:        "ReadFile",
		Description: "Reads a file",
		InputSchema: schema,
	}})
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}

	tp := got[0].OfTool
	if tp == nil {
		t.Fatal("OfTool is nil")
	}
	if tp.Name != "ReadFile" {
		t.Errorf("name = %q, want ReadFile", tp.Name)
	}
	if diff := cmp.Diff([]string{"path"}, tp.InputSchema.Required); diff != "" {
		t.Errorf("required mismatch (-want +got):\n%s", diff)
	}
	props, ok := tp.InputSchema.Properties.(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map", tp.InputSchema.Properties)
	}
	if _, ok := props["path"]; !ok {
		t.Errorf("properties missing 'path' key: %v", props)
	}
}

// TestConvertToolsBadSchema verifies that invalid JSON produces an error rather
// than a panic or silent drop.
func TestConvertToolsBadSchema(t *testing.T) {
	t.Parallel()

	_, err := convertTools([]ToolDef{{
		Name:        "Broken",
		InputSchema: json.RawMessage(`{not json`),
	}})
	if err == nil {
		t.Fatal("expected error for invalid schema, got nil")
	}
}

// TestConvertMessages verifies role mapping and content block conversion.
func TestConvertMessages(t *testing.T) {
	t.Parallel()

	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			ToolCallBlock{ID: "c1", Name: "ReadFile", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "c1", Content: "data"},
		}},
	}

	got, err := convertMessages(msgs)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
}

// TestConvertMessagesUnknownRole verifies an unknown role is rejected.
func TestConvertMessagesUnknownRole(t *testing.T) {
	t.Parallel()

	_, err := convertMessages([]Message{{Role: "system", Content: nil}})
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
}
