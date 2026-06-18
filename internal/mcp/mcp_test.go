package mcp_test

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nevaero/korai-code-cli/internal/mcp"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

// startServer spins up an in-process MCP server exposing a single "echo" tool
// and returns a client transport connected to it via an in-memory pipe.
func startServer(t *testing.T) mcpsdk.Transport {
	t.Helper()

	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "echo",
		Description: "echoes its input",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoArgs) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + in.Text}},
		}, nil, nil
	})

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	return clientT
}

func TestConnectAndAdaptTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conn, err := mcp.Connect(ctx, "demo", startServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	tools := conn.Tools()
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	echo := tools[0]

	if echo.Name() != "demo__echo" {
		t.Errorf("name = %q, want demo__echo", echo.Name())
	}
	if echo.Description(ctx) != "echoes its input" {
		t.Errorf("description = %q", echo.Description(ctx))
	}
	if echo.ReadOnly() || echo.ConcurrencySafe() {
		t.Error("MCP tools must be fail-closed (ReadOnly/ConcurrencySafe false)")
	}
	if d := echo.CheckPermission(ctx, nil, perm.ModeDefault); d != perm.DecisionAsk {
		t.Errorf("default permission = %v, want ask", d)
	}
	if d := echo.CheckPermission(ctx, nil, perm.ModeBypassPermissions); d != perm.DecisionAllow {
		t.Errorf("bypass permission = %v, want allow", d)
	}
	if echo.InputSchema() == nil {
		t.Error("InputSchema should not be nil")
	}
}

func TestRemoteToolExecute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conn, err := mcp.Connect(ctx, "demo", startServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	echo := conn.Tools()[0]
	res, err := echo.Execute(ctx, []byte(`{"text":"hi"}`), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if res.Content != "echo: hi" {
		t.Errorf("content = %q, want %q", res.Content, "echo: hi")
	}
}

func TestQualifiedNameRoundTrip(t *testing.T) {
	t.Parallel()
	q := mcp.QualifyName("server", "tool")
	if q != "server__tool" {
		t.Fatalf("QualifyName = %q", q)
	}
	s, tn, ok := mcp.ParseQualifiedName(q)
	if !ok || s != "server" || tn != "tool" {
		t.Errorf("ParseQualifiedName(%q) = %q,%q,%v", q, s, tn, ok)
	}
	if _, _, ok := mcp.ParseQualifiedName("nodelim"); ok {
		t.Error("ParseQualifiedName should fail without a delimiter")
	}
}
