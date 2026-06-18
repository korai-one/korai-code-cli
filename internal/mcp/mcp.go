// Package mcp connects to external MCP (Model Context Protocol) servers and
// adapts their tools onto the tool.Tool interface, so the engine treats an MCP
// tool exactly like a built-in one.
package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// clientName/clientVersion identify Korai to MCP servers during the handshake.
const (
	clientName    = "korai-code-cli"
	clientVersion = "0.1.0"
)

// Connection is a live link to one MCP server plus the tools it exposes.
type Connection struct {
	name    string
	session *mcpsdk.ClientSession
	tools   []tool.Tool
}

// ConnectStdio launches the given command as a stdio MCP server and connects to
// it over the process's stdin/stdout.
func ConnectStdio(ctx context.Context, name, command string, args []string, env map[string]string) (*Connection, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = mergeEnv(env)
	}
	return Connect(ctx, name, &mcpsdk.CommandTransport{Command: cmd})
}

// Connect performs the MCP handshake over transport, lists the server's tools,
// and adapts each onto tool.Tool. Adapted tools are namespaced "<server>__<tool>"
// to avoid collisions across servers and with built-ins.
func Connect(ctx context.Context, name string, transport mcpsdk.Transport) (*Connection, error) {
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: clientName, Version: clientVersion}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %q: %w", name, err)
	}

	tools, err := adaptTools(ctx, name, cs)
	if err != nil {
		_ = cs.Close()
		return nil, err
	}
	return &Connection{name: name, session: cs, tools: tools}, nil
}

// Name returns the configured server name.
func (c *Connection) Name() string { return c.name }

// Tools returns the adapted tools exposed by the server.
func (c *Connection) Tools() []tool.Tool { return c.tools }

// Close shuts down the session and the underlying server process.
func (c *Connection) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// adaptTools lists the server's tools and wraps each as a tool.Tool.
func adaptTools(ctx context.Context, server string, cs *mcpsdk.ClientSession) ([]tool.Tool, error) {
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp list tools %q: %w", server, err)
	}
	out := make([]tool.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		adapted, err := newRemoteTool(server, t, cs)
		if err != nil {
			return nil, fmt.Errorf("mcp adapt tool %q/%q: %w", server, t.Name, err)
		}
		out = append(out, adapted)
	}
	return out, nil
}

func mergeEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// QualifyName builds the namespaced tool name for an MCP tool.
func QualifyName(server, toolName string) string {
	return server + "__" + toolName
}

// ParseQualifiedName splits a namespaced MCP tool name back into its parts.
func ParseQualifiedName(qualified string) (server, toolName string, ok bool) {
	server, toolName, found := strings.Cut(qualified, "__")
	if !found {
		return "", "", false
	}
	return server, toolName, true
}
