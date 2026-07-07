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
	"github.com/Nevaero/korai-code-cli/internal/condense"
	"github.com/Nevaero/korai-code-cli/internal/config"
	appctx "github.com/Nevaero/korai-code-cli/internal/context"
	"github.com/Nevaero/korai-code-cli/internal/cost"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/hook"
	"github.com/Nevaero/korai-code-cli/internal/localworker"
	"github.com/Nevaero/korai-code-cli/internal/lsp"
	"github.com/Nevaero/korai-code-cli/internal/mcp"
	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/prompt"
	"github.com/Nevaero/korai-code-cli/internal/session"
	"github.com/Nevaero/korai-code-cli/internal/skill"
	"github.com/Nevaero/korai-code-cli/internal/snapshot"
	todostore "github.com/Nevaero/korai-code-cli/internal/todo"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools"
	agenttool "github.com/Nevaero/korai-code-cli/internal/tools/agent"
	checktool "github.com/Nevaero/korai-code-cli/internal/tools/check"
	diagnosticstool "github.com/Nevaero/korai-code-cli/internal/tools/diagnostics"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
	referencestool "github.com/Nevaero/korai-code-cli/internal/tools/references"
	todotool "github.com/Nevaero/korai-code-cli/internal/tools/todo"
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
	condense  engine.ToolResultFilter
	rules     perm.Rules
	system    string
	deps      tool.Deps
	closers   []func() error

	// authGate reports whether the session may run a turn: nil when the active
	// backend is ready, or an actionable error when a remote turn needs an API
	// key that is not set. Consulted before the first prompt, not at startup, so
	// the CLI can launch without a key (see --local for a key-free local run).
	authGate func() error

	fileFinder      func() []string
	mentionExpander func(string) string
	imageAttacher   func(string) []apiclient.ImageBlock

	sessionID      string
	sessionStart   time.Time
	initialHistory []apiclient.Message
	saver          func(id string, created time.Time, msgs []apiclient.Message)
	resumeLoad     func(id string) ([]apiclient.Message, time.Time, error)

	snapshots *snapshot.Manager
	snaplog   *snapshot.Log
}

// koraiModels is the set the /model command offers: the orchestrator's routing
// aliases. ListModels could fetch the live set, but that needs a network
// round-trip at startup; the aliases are always valid.
var koraiModels = []string{"auto", "fast", "balanced", "deep"}

// koraiDefaultModel is the model used when neither a flag nor config selects one.
const koraiDefaultModel = "auto"

// remoteConfigured reports whether the networked Korai backend can be reached:
// it needs KORAI_API_KEY. A keyless session still assembles (a local worker, or
// auth deferred to the first remote turn), so callers treat the error as
// non-fatal rather than aborting startup.
func remoteConfigured() error {
	if os.Getenv("KORAI_API_KEY") == "" {
		return fmt.Errorf("no API key set: export KORAI_API_KEY, put it in a .env file, or run against a local worker (--local)")
	}
	return nil
}

// defaultKoraiBaseURL is the orchestrator the CLI targets when KORAI_BASE_URL is
// not set. It points at the current EU deployment rather than the SDK's own
// cloud default; set KORAI_BASE_URL to override.
const defaultKoraiBaseURL = "https://korai-eu.fly.dev"

// newKoraiClient constructs the networked Korai backend client, reading the key
// and the optional base URL from the environment.
func newKoraiClient(model string) apiclient.Client {
	baseURL := os.Getenv("KORAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultKoraiBaseURL
	}
	return apiclient.NewKoraiClient(os.Getenv("KORAI_API_KEY"), baseURL, model)
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

	// Local worker takes precedence over the networked backends: an explicit
	// --local-worker-url / KORAI_LOCAL_WORKER_URL override, otherwise a worker
	// that advertised itself and answers a health probe. When present, inference
	// goes straight to the loopback worker — no orchestrator, no network.
	localOverride := opts.localWorkerURL
	if localOverride == "" {
		localOverride = os.Getenv("KORAI_LOCAL_WORKER_URL")
	}
	// A dedicated home/LAN inference server is reached over the direct binary
	// channel on TCP: an explicit address (flag or env) plus an optional token.
	lanAddr := opts.localWorkerAddr
	if lanAddr == "" {
		lanAddr = os.Getenv("KORAI_LOCAL_WORKER_ADDR")
	}
	lanToken := os.Getenv("KORAI_LOCAL_WORKER_TOKEN")
	endpoint, useLocal := localworker.Resolve(ctx, localOverride, lanAddr, lanToken, nil)

	// --local demands a local worker and never touches a remote key: if none
	// resolves, fail fast with guidance rather than silently going keyless-remote.
	if opts.local && !useLocal {
		return nil, fmt.Errorf("--local: no local worker found (start one, or set --local-worker-url / --local-worker-addr, or KORAI_LOCAL_WORKER_URL / KORAI_LOCAL_WORKER_ADDR)")
	}

	// The networked backend is resolved best-effort so /worker_mode can switch to
	// it even when the session starts on a local worker. A missing key is no
	// longer fatal here: a keyless session still assembles, and the auth check is
	// deferred to the first remote turn (see authGate below).
	remoteErr := remoteConfigured()

	if useLocal {
		if endpoint.IsDirect() {
			slog.Info("using local worker", "network", endpoint.Network, "address", endpoint.Address)
		} else {
			slog.Info("using local worker", "url", endpoint.URL)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	home, _ := os.UserHomeDir() // empty home just means no user-level settings

	settings, err := config.DefaultPaths(home, wd).LoadContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	// Model precedence: --model flag, then config, then the backend default.
	model := opts.model
	if !opts.modelSet && settings.Model != "" {
		model = settings.Model
	}
	if model == "" {
		model = koraiDefaultModel
	}
	mode := opts.permMode
	if !opts.permModeSet {
		if m, perr := perm.ParseMode(settings.PermissionMode); perr == nil {
			mode = m
		}
	}

	// Language-server diagnostics: enabled unless explicitly disabled in config.
	// A no-op when no server is on PATH for the edited file, so default-on is
	// safe. Edit/Write append its output to their result for model self-correction.
	lspEnabled := settings.LSP == nil || *settings.LSP
	lspMgr := lsp.New(lspEnabled)
	deps := tool.Deps{WorkDir: wd, LSP: lspMgr}
	// Build whichever backends are available and expose both through a
	// ClientSelector so /worker_mode can switch between them at runtime. The local
	// worker needs no API key; the networked backend reads its key via newKoraiClient.
	var localClient apiclient.Client
	if useLocal {
		switch {
		case endpoint.IsDirect() && endpoint.Network == "tcp":
			// Direct binary channel to a home/LAN inference server (hop 1 over TCP).
			localClient = apiclient.NewLocalWorkerClientTCP(endpoint.Address, endpoint.Token, model)
		case endpoint.IsDirect():
			// Direct binary channel to a co-located worker (hop 1 over a unix socket).
			localClient = apiclient.NewLocalWorkerClient(endpoint.Address, model)
		default:
			// Co-located worker exposing only the loopback OpenAI-HTTP endpoint.
			localClient = apiclient.NewKoraiClient("", endpoint.URL, model)
		}
	}
	var remoteClient apiclient.Client
	if remoteErr == nil {
		remoteClient = newKoraiClient(model)
	}
	activeMode := apiclient.WorkerRemote
	if useLocal {
		activeMode = apiclient.WorkerLocal
	}
	clientSelector := apiclient.NewClientSelector(activeMode, localClient, remoteClient)
	var client apiclient.Client = clientSelector

	// authGate defers the API-key requirement to the first prompt: a session on a
	// local worker (or one that later switches to it) needs no key, but a remote
	// turn does. It re-reads the active mode each call so /worker_mode is honored.
	authGate := func() error {
		if clientSelector.Mode() == string(apiclient.WorkerRemote) && remoteClient == nil {
			return remoteErr
		}
		return nil
	}
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
	// RunChecks runs the project's configured verification commands; the LSP
	// query tools are on-demand reads. All are available to sub-agents too.
	subRegistry.Register(checktool.New(settings.Checks))
	if lspMgr.Enabled() {
		subRegistry.Register(diagnosticstool.New(lspMgr))
		subRegistry.Register(referencestool.New(lspMgr))
	}

	var closers []func() error
	// Teardown must stop the servers even if the session context is already
	// cancelled, so it intentionally uses a fresh context.
	closers = append(closers, func() error { lspMgr.Shutdown(context.Background()); return nil }) //nolint:contextcheck
	closers = append(closers, connectMCPServers(ctx, settings.MCPServers, subRegistry)...)

	registry := tool.NewRegistry()
	for _, t := range subRegistry.All() {
		registry.Register(t)
	}
	spawner := &subAgentSpawner{
		client: client, registry: subRegistry, mode: mode, rules: rules, deps: deps, system: system,
	}
	registry.Register(agenttool.New(spawner))
	// TodoWrite tracks the session's task list; it is a top-level concern, not
	// something a spawned sub-agent should manage, so it lives in the main set.
	registry.Register(todotool.New(&todostore.List{}))
	// ExitPlanMode lives only in the main registry (never the sub-agent set).
	registry.Register(plantool.New(modes, planApprover))

	compactor := func(cctx context.Context, history []apiclient.Message) ([]apiclient.Message, error) {
		return compact.Compact(cctx, client, history, compact.DefaultKeepRecent)
	}

	// Shadow-git snapshots: a worktree checkpoint is taken before each turn and
	// /revert restores one. The Manager is a no-op when git is absent; the Log
	// is the in-session (label, id) history /snapshots renders and /revert reads.
	snapMgr := snapshot.New(ctx, wd, snapshotsDir(home))
	snapLog := &snapshot.Log{}

	// Session persistence: resolve the session to use (resume / continue / new).
	// Sessions persist to SQLite; if the database cannot be opened, fall back to
	// the JSONL file store so a storage error never blocks a session.
	var sessStore session.Store
	if sq, serr := session.NewSQLiteStore(ctx, sessionsDBPath(home)); serr == nil {
		sessStore = sq
	} else {
		slog.Warn("opening sqlite session store; falling back to file store", "error", serr)
		sessStore = session.NewFileStore(sessionsDir(home))
	}
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
		commands:  buildCommands(home, wd, registry, koraiModels, models, modes, costTracker, sessStore, snapLog, clientSelector),
		models:    models,
		modes:     modes,
		cost:      costTracker,
		compactor: compactor,
		hooks:     buildHooks(settings.Hooks),
		condense:  buildCondenser(settings.Condense),
		rules:     rules,
		system:    system,
		deps:      deps,
		closers:   closers,
		authGate:  authGate,

		fileFinder:      workspaceFiles(wd),
		mentionExpander: mentionExpander(wd),
		imageAttacher:   imageAttacher(wd),

		sessionID:      sessionID,
		sessionStart:   sessionStart,
		initialHistory: initialHistory,
		saver:          saver,
		resumeLoad:     resumeLoad,

		snapshots: snapMgr,
		snaplog:   snapLog,
	}, nil
}

// sessionsDir returns the directory where JSONL sessions are stored (the
// fallback store).
func sessionsDir(home string) string {
	return filepath.Join(home, ".korai", "sessions")
}

// sessionsDBPath returns the SQLite database file backing sessions.
func sessionsDBPath(home string) string {
	return filepath.Join(home, ".korai", "sessions.db")
}

// snapshotsDir returns the directory where shadow-git snapshot repos are kept,
// one per worktree (the Manager namespaces by worktree path beneath it).
func snapshotsDir(home string) string {
	return filepath.Join(home, ".korai", "snapshots")
}

// resolveSession picks the session to use: an explicit --resume id, the latest
// session for the directory with --continue, or a fresh session otherwise. On
// any resume failure it logs and falls back to a new session.
func resolveSession(store session.Store, wd string, opts runOptions) (id string, created time.Time, history []apiclient.Message) {
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
func formatSessions(store session.Store, wd string) string {
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

// aboutText is the message shown by /about: version and a one-line description.
func aboutText() string {
	return fmt.Sprintf(
		"Korai Code CLI %s\nAI coding agent on the Korai P2P inference network.\nhttps://github.com/Nevaero/korai-code-cli",
		version,
	)
}

// buildCommands assembles the slash-command registry: built-ins, /model, /cost,
// /compact, the bundled skills, and skills discovered from the project and user
// skill directories (which override bundled ones of the same name).
func buildCommands(home, wd string, registry *tool.Registry, modelList []string, models *apiclient.ModelSelector, modes *perm.ModeSelector, costTracker *cost.Tracker, sessStore session.Store, snapLog *snapshot.Log, workers command.WorkerSelector) *command.Registry {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg, func() []string {
		tools := registry.All()
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		return names
	})
	reg.Register(command.NewAboutCommand(aboutText()))
	reg.Register(command.NewModelCommand(modelList, models))
	reg.Register(command.NewWorkerCommand(workers))
	reg.Register(command.NewCostCommand(costTracker.Summary))
	reg.Register(command.NewCompactCommand())
	reg.Register(command.NewPlanCommand(
		func() string { return togglePlan(modes) },
		func() { modes.Set(perm.ModePlan) },
	))
	reg.Register(command.NewResumeCommand(func() string { return formatSessions(sessStore, wd) }))
	reg.Register(command.NewRevertCommand())
	reg.Register(command.NewSnapshotsCommand(snapLog.Render))

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

// buildCondenser turns the condense settings into an engine tool-result filter.
// Condensing is on by default (nil settings): it only shrinks verbose output
// from targeted tools and never hides anything from the terminal. It returns nil
// only when explicitly disabled, so the engine then skips filtering entirely.
func buildCondenser(cs *config.CondenseSettings) engine.ToolResultFilter {
	if cs != nil && cs.Enabled != nil && !*cs.Enabled {
		return nil
	}
	cfg := condense.Config{}
	if cs != nil {
		cfg.Tools = cs.Tools
		cfg.MaxLines = cs.MaxLines
		cfg.HeadLines = cs.HeadLines
		cfg.TailLines = cs.TailLines
	}
	f := condense.New(cfg)
	return func(toolName string, r tool.Result) string {
		return f.Apply(toolName, r.Content)
	}
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

// ApprovePlan implements plantool.Approver. With --yes it approves and switches
// to acceptEdits so the plan can be carried out; otherwise it rejects
// (fail-closed). Headless runs collect no feedback.
func (a headlessPlanApprover) ApprovePlan(context.Context, string) (plantool.Decision, string, error) {
	if a.autoYes {
		return plantool.ApproveAcceptEdits, "", nil
	}
	return plantool.Reject, "", nil
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
