package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/config"
	appctx "github.com/Nevaero/korai-code-cli/internal/context"
	"github.com/Nevaero/korai-code-cli/internal/mcp"
	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/prompt"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
)

// assembled is the wired-up session: everything the engine needs, plus the
// resolved permission policy and any resources to release on shutdown.
type assembled struct {
	client   apiclient.Client
	registry *tool.Registry
	rules    perm.Rules
	mode     perm.Mode
	system   string
	deps     tool.Deps
	closers  []func() error
}

// close releases session resources (e.g. MCP server connections).
func (a *assembled) close() {
	for _, c := range a.closers {
		if err := c(); err != nil {
			slog.Warn("closing session resource", "error", err)
		}
	}
}

// assemble loads config, resolves model and permission mode (flags override the
// config file), builds the tool registry (built-ins + memory + MCP servers),
// and composes the system prompt with persistent memory. The API key is read
// from the environment.
func assemble(ctx context.Context, opts runOptions) (*assembled, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	home, _ := os.UserHomeDir() // empty home just means no user-level settings

	settings, err := config.DefaultPaths(home, wd).Load()
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	model := opts.model
	if !opts.modelSet && settings.Model != "" {
		model = settings.Model
	}
	mode := opts.permMode
	if !opts.permModeSet {
		if m, perr := perm.ParseMode(settings.PermissionMode); perr == nil {
			mode = m
		}
	}

	deps := tool.Deps{WorkDir: wd}
	registry := tool.NewRegistry()
	tools.RegisterAll(registry)

	// Persistent memory: a Remember tool plus injection of existing notes.
	store := memory.NewStore(filepath.Join(wd, ".korai", "MEMORY.md"))
	registry.Register(memtool.New(store))

	var closers []func() error
	closers = append(closers, connectMCPServers(ctx, settings.MCPServers, registry)...)

	system := prompt.Compose(appctx.Build(ctx, wd))
	if mem, rerr := store.Read(); rerr == nil && strings.TrimSpace(mem) != "" {
		system += "\n\n# Memory\n\n" + mem
	}

	return &assembled{
		client:   apiclient.NewAnthropicClient(apiKey, model),
		registry: registry,
		rules:    perm.Rules{Allow: settings.Permissions.Allow, Deny: settings.Permissions.Deny},
		mode:     mode,
		system:   system,
		deps:     deps,
		closers:  closers,
	}, nil
}

// connectMCPServers connects to each configured MCP server and registers its
// tools. Connection is fail-open: a server that cannot be reached is logged and
// skipped rather than aborting startup. Returns closers for the live servers.
func connectMCPServers(ctx context.Context, specs map[string]config.MCPServerSpec, registry *tool.Registry) []func() error {
	var closers []func() error
	for name, spec := range specs {
		conn, err := mcp.ConnectStdio(ctx, name, spec.Command, spec.Args, spec.Env)
		if err != nil {
			slog.Warn("skipping MCP server", "server", name, "error", err)
			continue
		}
		for _, t := range conn.Tools() {
			registry.Register(t)
		}
		closers = append(closers, conn.Close)
		slog.Debug("connected MCP server", "server", name, "tools", len(conn.Tools()))
	}
	return closers
}
