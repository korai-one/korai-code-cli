package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/compact"
	"github.com/Nevaero/korai-code-cli/internal/config"
	appctx "github.com/Nevaero/korai-code-cli/internal/context"
	"github.com/Nevaero/korai-code-cli/internal/cost"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/hook"
	"github.com/Nevaero/korai-code-cli/internal/mcp"
	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/prompt"
	"github.com/Nevaero/korai-code-cli/internal/session"
	"github.com/Nevaero/korai-code-cli/internal/skill"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools"
	agenttool "github.com/Nevaero/korai-code-cli/internal/tools/agent"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
)

// assembled is the wired-up session: everything the engine needs, plus the
// resolved permission policy, slash commands, lifecycle hooks, and any
// resources to release on shutdown.
type assembled struct {
	client    apiclient.Client
	registry  *tool.Registry
	commands  *command.Registry
	models    *apiclient.ModelSelector
	modes     *perm.ModeSelector
	cost      *cost.Tracker
	compactor func(context.Context, []apiclient.Message) ([]apiclient.Message, error)
	hooks     engine.HookFunc
	rules     perm.Rules
	system    string
	deps      tool.Deps
	closers   []func() error

	sessionID      string
	sessionStart   time.Time
	initialHistory []apiclient.Message
	saver          func(id string, created time.Time, msgs []apiclient.Message)
	resumeLoad     func(id string) ([]apiclient.Message, time.Time, error)
}

// availableModels is the set the /model command switches between. The active
// model is always selectable even if it is not in this list.
var availableModels = []string{
	"claude-opus-4-8",
	"claude-sonnet-4-6",
	"claude-haiku-4-5",
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
func assemble(ctx context.Context, opts runOptions, planApprover plantool.Approver) (*assembled, error) {
	// Load .env from the working directory as a fallback for local development.
	// godotenv.Load does not override variables already set in the real
	// environment, so an exported key still takes precedence; a missing file is
	// not an error.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("loading .env: %w", err)
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set (export it or put it in a .env file)")
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
	models := apiclient.NewModelSelector(model)
	modes := perm.NewModeSelector(mode)
	costTracker := cost.NewTracker()
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
	// ExitPlanMode lives only in the main registry (never the sub-agent set).
	registry.Register(plantool.New(modes, planApprover))

	compactor := func(cctx context.Context, history []apiclient.Message) ([]apiclient.Message, error) {
		return compact.Compact(cctx, client, history, compact.DefaultKeepRecent)
	}

	// Session persistence: resolve the session to use (resume / continue / new).
	sessStore := session.NewStore(sessionsDir(home))
	sessionID, sessionStart, initialHistory := resolveSession(sessStore, wd, opts)
	saver := func(id string, created time.Time, msgs []apiclient.Message) {
		if err := sessStore.Save(session.Record{
			ID: id, Created: created, Updated: time.Now(),
			CWD: wd, Model: models.Get(), Messages: msgs,
		}); err != nil {
			slog.Warn("saving session", "error", err)
		}
	}
	resumeLoad := func(id string) ([]apiclient.Message, time.Time, error) {
		rec, err := sessStore.Load(id)
		if err != nil {
			return nil, time.Time{}, err
		}
		return rec.Messages, rec.Created, nil
	}

	return &assembled{
		client:    client,
		registry:  registry,
		commands:  buildCommands(home, wd, registry, models, modes, costTracker, sessStore),
		models:    models,
		modes:     modes,
		cost:      costTracker,
		compactor: compactor,
		hooks:     buildHooks(settings.Hooks),
		rules:     rules,
		system:    system,
		deps:      deps,
		closers:   closers,

		sessionID:      sessionID,
		sessionStart:   sessionStart,
		initialHistory: initialHistory,
		saver:          saver,
		resumeLoad:     resumeLoad,
	}, nil
}

// sessionsDir returns the directory where sessions are stored.
func sessionsDir(home string) string {
	return filepath.Join(home, ".korai", "sessions")
}

// resolveSession picks the session to use: an explicit --resume id, the latest
// session for the directory with --continue, or a fresh session otherwise. On
// any resume failure it logs and falls back to a new session.
func resolveSession(store *session.Store, wd string, opts runOptions) (id string, created time.Time, history []apiclient.Message) {
	switch {
	case opts.resumeID != "":
		rec, err := store.Load(opts.resumeID)
		if err == nil {
			return rec.ID, rec.Created, rec.Messages
		}
		slog.Warn("could not resume session; starting fresh", "id", opts.resumeID, "error", err)
	case opts.cont:
		if rec, ok, err := store.Latest(wd); err == nil && ok {
			return rec.ID, rec.Created, rec.Messages
		}
	}
	return session.NewID(), time.Now(), nil
}

// formatSessions renders the saved-session list for /resume.
func formatSessions(store *session.Store, wd string) string {
	records, err := store.List()
	if err != nil {
		return "could not list sessions: " + err.Error()
	}
	if len(records) == 0 {
		return "No saved sessions yet."
	}
	var b strings.Builder
	b.WriteString("Saved sessions (newest first) — /resume <id> to load:")
	shown := 0
	for _, r := range records {
		if r.CWD != wd {
			continue
		}
		fmt.Fprintf(&b, "\n  %s  %s  (%d msgs)  %s",
			r.ID, r.Updated.Local().Format("2006-01-02 15:04"), len(r.Messages), firstUserText(r.Messages))
		shown++
	}
	if shown == 0 {
		return "No saved sessions for this directory yet."
	}
	return b.String()
}

// firstUserText returns a short snippet of the first user message.
func firstUserText(msgs []apiclient.Message) string {
	for _, m := range msgs {
		if m.Role != apiclient.RoleUser {
			continue
		}
		for _, blk := range m.Content {
			if tb, ok := blk.(apiclient.TextBlock); ok && strings.TrimSpace(tb.Text) != "" {
				s := strings.TrimSpace(tb.Text)
				if len(s) > 50 {
					s = s[:50] + "…"
				}
				return s
			}
		}
	}
	return ""
}

// buildCommands assembles the slash-command registry: built-ins, /model, /cost,
// /compact, the bundled skills, and skills discovered from the project and user
// skill directories (which override bundled ones of the same name).
func buildCommands(home, wd string, registry *tool.Registry, models *apiclient.ModelSelector, modes *perm.ModeSelector, costTracker *cost.Tracker, sessStore *session.Store) *command.Registry {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg, func() []string {
		tools := registry.All()
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		return names
	})
	reg.Register(command.NewModelCommand(availableModels, models))
	reg.Register(command.NewCostCommand(costTracker.Summary))
	reg.Register(command.NewCompactCommand())
	reg.Register(command.NewPlanCommand(func() string { return togglePlan(modes) }))
	reg.Register(command.NewResumeCommand(func() string { return formatSessions(sessStore, wd) }))

	// Bundled skills first, then discovered skills (which override by name).
	if builtins, err := skill.Builtins(); err != nil {
		slog.Warn("loading bundled skills", "error", err)
	} else {
		skill.Register(reg, builtins)
	}

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

// headlessPlanApprover resolves ExitPlanMode without a UI: it approves when the
// operator passed --yes, otherwise rejects (fail-closed), mirroring the
// headless permission asker.
type headlessPlanApprover struct{ autoYes bool }

// ApprovePlan implements plantool.Approver.
func (a headlessPlanApprover) ApprovePlan(context.Context, string) (bool, error) {
	return a.autoYes, nil
}

// planSuffix returns a function that supplies the plan-mode system prompt
// addendum while the session is in plan mode, and nothing otherwise.
func planSuffix(modes *perm.ModeSelector) func() string {
	return func() string {
		if modes.Get() == perm.ModePlan {
			return prompt.PlanNote()
		}
		return ""
	}
}

// togglePlan flips the selector between plan mode and default mode and returns
// the resulting mode name. Wired into the /plan command.
func togglePlan(modes *perm.ModeSelector) string {
	if modes.Get() == perm.ModePlan {
		modes.Set(perm.ModeDefault)
	} else {
		modes.Set(perm.ModePlan)
	}
	return modes.Get().String()
}

// Spawn runs the sub-agent loop for prompt and returns the concatenated text.
func (s *subAgentSpawner) Spawn(ctx context.Context, prompt string) (string, error) {
	permEngine := perm.NewEngine(perm.NewModeSelector(s.mode), s.rules, perm.DenyAsker{})
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
