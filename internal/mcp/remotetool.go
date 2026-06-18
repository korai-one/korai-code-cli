package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// caller is the slice of *mcpsdk.ClientSession the adapter invokes. Declaring it
// as an interface keeps the adapter unit-testable with a fake.
type caller interface {
	CallTool(ctx context.Context, params *mcpsdk.CallToolParams) (*mcpsdk.CallToolResult, error)
}

// remoteTool adapts a single MCP server tool onto the tool.Tool interface.
type remoteTool struct {
	name       string // namespaced "<server>__<tool>"
	remoteName string // the server's own tool name
	desc       string
	schema     *jsonschema.Schema
	session    caller
}

// newRemoteTool builds an adapter for one MCP tool, parsing its JSON Schema.
func newRemoteTool(server string, t *mcpsdk.Tool, session caller) (*remoteTool, error) {
	schema, err := parseSchema(t.InputSchema)
	if err != nil {
		return nil, err
	}
	return &remoteTool{
		name:       QualifyName(server, t.Name),
		remoteName: t.Name,
		desc:       t.Description,
		schema:     schema,
		session:    session,
	}, nil
}

// parseSchema converts the server-provided schema (any) into a jsonschema.Schema
// by round-tripping through JSON. A nil schema yields an empty object schema.
func parseSchema(raw any) (*jsonschema.Schema, error) {
	if raw == nil {
		return &jsonschema.Schema{Type: "object"}, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshaling input schema: %w", err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing input schema: %w", err)
	}
	return &s, nil
}

// Name returns the namespaced tool name.
func (t *remoteTool) Name() string { return t.name }

// Description returns the server-provided tool description.
func (t *remoteTool) Description(context.Context) string { return t.desc }

// InputSchema returns the tool's JSON schema as advertised by the server.
func (t *remoteTool) InputSchema() *jsonschema.Schema { return t.schema }

// ReadOnly is false: MCP tools are external and may have side effects, so the
// adapter is fail-closed.
func (t *remoteTool) ReadOnly() bool { return false }

// ConcurrencySafe is false for the same fail-closed reason as ReadOnly.
func (t *remoteTool) ConcurrencySafe() bool { return false }

// CheckPermission gates MCP tools conservatively: allowed only under
// bypassPermissions, otherwise the user is asked (external code, unknown effects).
func (t *remoteTool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	if mode == perm.ModeBypassPermissions {
		return perm.DecisionAllow
	}
	return perm.DecisionAsk
}

// Execute forwards the call to the MCP server and converts its result.
func (t *remoteTool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var args any
	if err := json.Unmarshal(raw, &args); err != nil {
		return tool.Result{}, fmt.Errorf("mcp %q: invalid input: %w", t.name, err)
	}

	res, err := t.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      t.remoteName,
		Arguments: args,
	})
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("mcp call failed: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: flattenContent(res.Content), IsError: res.IsError}, nil
}

// flattenContent concatenates the text parts of an MCP result. Non-text content
// is summarized so the model at least sees that something was returned.
func flattenContent(content []mcpsdk.Content) string {
	var b strings.Builder
	for _, c := range content {
		switch v := c.(type) {
		case *mcpsdk.TextContent:
			b.WriteString(v.Text)
		default:
			fmt.Fprintf(&b, "[%T]", c)
		}
	}
	return b.String()
}
