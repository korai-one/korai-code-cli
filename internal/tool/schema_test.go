package tool_test

import (
	"encoding/json"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

type sampleInput struct {
	Path  string `json:"path" jsonschema:"required,description=A file path"`
	Limit int    `json:"limit" jsonschema:"description=Max lines"`
}

// TestSchemaInlined verifies that Schema produces a self-contained schema with
// top-level properties (no $ref/$defs), the required list, and type "object".
func TestSchemaInlined(t *testing.T) {
	t.Parallel()

	s := tool.Schema[sampleInput]()

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
		Ref        string         `json:"$ref"`
		Defs       map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != "object" {
		t.Errorf("type = %q, want object", decoded.Type)
	}
	if decoded.Ref != "" {
		t.Errorf("schema should be inlined, got $ref = %q", decoded.Ref)
	}
	if decoded.Defs != nil {
		t.Errorf("schema should be inlined, got $defs = %v", decoded.Defs)
	}
	if _, ok := decoded.Properties["path"]; !ok {
		t.Errorf("missing 'path' property: %v", decoded.Properties)
	}
	if _, ok := decoded.Properties["limit"]; !ok {
		t.Errorf("missing 'limit' property: %v", decoded.Properties)
	}
	if len(decoded.Required) != 1 || decoded.Required[0] != "path" {
		t.Errorf("required = %v, want [path]", decoded.Required)
	}
}
