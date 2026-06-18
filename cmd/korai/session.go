package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/config"
	appctx "github.com/Nevaero/korai-code-cli/internal/context"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/hook"
	"github.com/Nevaero/korai-code-cli/internal/mcp"
	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/prompt"
	"github.com/Nevaero/korai-code-cli/internal/skill"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools"
	agenttool "github.com/Nevaero/korai-code-cli/internal/tools/agent"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
)

// assembled is the wired-up session: everything the engine needs, plus the
// resolved permission policy, slash commands, lifecycle hooks, and any
// resources to release on shutdown.
type assembled struct {
	client   apiclient.Client
	registry *tool.Registry
	commands *command.Registry
	hooks    engine.HookFunc
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
// config file), builds the tool registry (built-ins + memory + MCP + the Task
// sub-agent tool), the slash-command registry (built-ins + skills), and the
// lifecycle hook runner, and composes the system prompt with persistent memory.
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
	client := apiclient.NewAnthropicClient(apiKey, model)
	rules := perm.Rules{Allow: settings.Permissions.Allow, Deny: settings.Permissions.Deny}

	system := prompt.Compose(appctx.Build(ctx, wd))
	store := memory.NewStore(filepath.Join(wd, ".korai", "MEMORY.md"))
	if mem, rerr := store.Read(); rerr == nil && strings.TrimSpace(mem) != "" {
		system += "\n\n# Memory\n\n" + mem
	}

	// Two registries: the sub-agent set has every tool EXCEPT Task, so a
	// spawned sub-agent cannot recurse into more sub-agents. The main set is the
	// sub-agent set plus the Task tool.
	subRegistry := tool.NewRegistry()
	tools.RegisterAll(subRegistry)
	subRegistry.Register(memtool.New(store))

	var closers []func() error
	closers = append(closers, connectMCPServers(ctx, settings.MCPServers, subRegistry)...)

	registry := tool.NewRegistry()
	for _, t := range subRegistry.All() {
		registry.Register(t)
	}
	spawner := &subAgentSpawner{
		client: client, registry: subRegistry, mode: mode, rules: rules, deps: deps, system: system,
	}
	registry.Register(agenttool.New(spawner))

	return &assembled{
		client:   client,
		registry: registry,
		commands: buildCommands(home, wd, registry),
		hooks:    buildHooks(settings.Hooks),
		rules:    rules,
		mode:     mode,
		system:   system,
		deps:     deps,
		closers:  closers,
	}, nil
}

// buildCommands assembles the slash-command registry: built-ins plus skills
// discovered from the project and user skill directories.
func buildCommands(home, wd string, registry *tool.Registry) *command.Registry {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg, func() []string {
		tools := registry.All()
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		return names
	})

	dirs := []string{filepath.Join(wd, ".korai", "skills")}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".korai", "skills"))
	}
	skills, err := skill.Discover(dirs)
	if err != nil {
		slog.Warn("discovering skills", "error", err)
	}
	skill.Register(reg, skills)
	return reg
}

// buildHooks translates the configured hook specs into an engine hook function.
// Returns nil when no hooks are configured.
func buildHooks(specs map[string][]config.HookSpec) engine.HookFunc {
	if len(specs) == 0 {
		return nil
	}
	converted := make(map[string][]hook.Spec, len(specs))
	for event, list := range specs {
		for _, s := range list {
			converted[event] = append(converted[event], hook.Spec{Command: s.Command})
		}
	}
	return hook.New(converted).Fire
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

// subAgentSpawner runs a sub-agent to completion and returns its final text.
// It implements agenttool.Spawner. The sub-agent runs autonomously with a
// fail-closed asker (an "ask" tool is denied), so it can only use tools that
// are allowed without prompting in the active mode.
type subAgentSpawner struct {
	client   apiclient.Client
	registry *tool.Registry
	mode     perm.Mode
	rules    perm.Rules
	deps     tool.Deps
	system   string
}

// Spawn runs the sub-agent loop for prompt and returns the concatenated text.
func (s *subAgentSpawner) Spawn(ctx context.Context, prompt string) (string, error) {
	permEngine := perm.NewEngine(s.mode, s.rules, perm.DenyAsker{})
	eng := engine.New(s.client, s.registry, permEngine, s.deps)
	messages := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: prompt}}},
	}

	var out strings.Builder
	for evt := range eng.Run(ctx, messages, s.system) {
		switch v := evt.(type) {
		case engine.TextEvent:
			out.WriteString(v.Text)
		case engine.ErrorEvent:
			return "", v.Err
		}
	}
	return out.String(), nil
}
