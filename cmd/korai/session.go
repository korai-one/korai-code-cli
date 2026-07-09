package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	korai "github.com/korai-one/korai-sdk-go"
	sdksession "github.com/korai-one/korai-sdk-go/session"
	"github.com/korai-one/korai-sdk-go/session/synchub"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/compact"
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
	rules     perm.Rules
	system    string
	deps      tool.Deps
	closers   []func() error

	fileFinder      func() []string
	mentionExpander func(string) string
	imageAttacher   func(string) []apiclient.ImageBlock

	sessionID      string
	sessionStart   time.Time
	initialHistory []apiclient.Message
	saver          func(id string, created time.Time, msgs []apiclient.Message)
	resumeLoad     func(id string) ([]apiclient.Message, time.Time, error)

	// activeSyncInterval is the cadence at which the long-running loops (TUI,
	// serve) re-persist the open conversation so the background syncer pushes it
	// mid-session. Zero disables the periodic checkpoint (sync is off).
	activeSyncInterval time.Duration

	snapshots *snapshot.Manager
	snaplog   *snapshot.Log
}

// koraiModels is the set the /model command offers: the orchestrator's routing
// aliases. ListModels could fetch the live set, but that needs a network
// round-trip at startup; the aliases are always valid.
var koraiModels = []string{"auto", "fast", "balanced", "deep"}

// defaultKoraiModel is the model used when neither a flag nor config selects one.
const defaultKoraiModel = "auto"

// defaultKoraiBaseURL is the orchestrator the CLI targets when KORAI_BASE_URL is
// not set. It points at the current EU deployment rather than the SDK's own
// cloud default; set KORAI_BASE_URL to override.
const defaultKoraiBaseURL = "https://korai-eu.fly.dev"

// requireAPIKey verifies a Korai API key is present for the networked backend.
// The local-worker path needs none, so callers skip it in that mode. Returns an
// error when KORAI_API_KEY is missing.
func requireAPIKey() error {
	if os.Getenv("KORAI_API_KEY") == "" {
		return fmt.Errorf("no API key set: export KORAI_API_KEY or put it in a .env file")
	}
	return nil
}

// newKoraiClient constructs the KoraiClient, reading the API key and optional
// base URL from the environment.
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
	localURL, useLocal := localworker.Resolve(ctx, localOverride, nil)

	if useLocal {
		slog.Info("using local worker", "url", localURL)
	} else if err := requireAPIKey(); err != nil {
		return nil, err
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

	// Model precedence: --model flag, then config, then the backend default.
	model := opts.model
	if !opts.modelSet && settings.Model != "" {
		model = settings.Model
	}
	if model == "" {
		model = defaultKoraiModel
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
	// The local worker needs no API key; the networked backends read theirs from
	// the environment via newClient.
	var client apiclient.Client
	if useLocal {
		client = apiclient.NewKoraiClient("", localURL, model)
	} else {
		client = newKoraiClient(model)
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

	// Cross-device history sync (opt-in; OFF by default). When enabled it
	// encrypts sessions at rest with the sync content key and starts a
	// background client that replicates them through the blind hub. When
	// disabled (the default) the store uses no codec and no goroutine or network
	// call happens — zero behavior change.
	sessStore, syncCfg := openSyncedStore(ctx, home, syncFileSettings(settings.Sync))

	var activeSyncInterval time.Duration
	if syncCfg.Enabled {
		if syncer, serr := synchub.New(syncCfg, sessStore, slog.Default()); serr != nil {
			slog.Warn("sync client disabled", "error", serr)
		} else if syncer != nil {
			go syncer.Run(ctx)
			slog.Info("cross-device history sync enabled", "interval", syncCfg.Interval)
		}
		// The long-running loops re-persist the open conversation on this cadence
		// so the syncer sweeps it up before the turn that ends it. Only meaningful
		// when sync is on, so it stays zero (disabled) otherwise.
		activeSyncInterval = resolveActiveSyncInterval(settings.Sync)
	}
	sessionID, sessionStart, initialHistory := resolveSession(sessStore, wd, opts)
	saver := func(id string, created time.Time, msgs []apiclient.Message) {
		if err := sessStore.Save(korai.Session{
			ID: id, Created: created, Updated: time.Now(),
			CWD: wd, Model: models.Get(), Tool: session.Tool,
			Messages: session.ToCanonicalMessages(msgs),
		}); err != nil {
			slog.Warn("saving session", "error", err)
		}
	}
	resumeLoad := func(id string) ([]apiclient.Message, time.Time, error) {
		sess, err := sessStore.Load(id)
		if err != nil {
			return nil, time.Time{}, err
		}
		return session.FromCanonicalMessages(sess.Messages), sess.Created, nil
	}

	return &assembled{
		client:    client,
		registry:  registry,
		commands:  buildCommands(home, wd, registry, koraiModels, models, modes, costTracker, sessStore, snapLog),
		models:    models,
		modes:     modes,
		cost:      costTracker,
		compactor: compactor,
		hooks:     buildHooks(settings.Hooks),
		rules:     rules,
		system:    system,
		deps:      deps,
		closers:   closers,

		fileFinder:      workspaceFiles(wd),
		mentionExpander: mentionExpander(wd),
		imageAttacher:   imageAttacher(wd),

		sessionID:      sessionID,
		sessionStart:   sessionStart,
		initialHistory: initialHistory,
		saver:          saver,
		resumeLoad:     resumeLoad,

		activeSyncInterval: activeSyncInterval,

		snapshots: snapMgr,
		snaplog:   snapLog,
	}, nil
}

// syncFileSettings maps the optional config "sync" block onto the synchub
// settings form. A nil block yields the zero value (disabled unless env
// enables it).
func syncFileSettings(s *config.SyncSettings) synchub.FileSettings {
	if s == nil {
		return synchub.FileSettings{}
	}
	return synchub.FileSettings{
		Enabled:  s.Enabled,
		URL:      s.URL,
		SyncID:   s.SyncID,
		Interval: s.Interval,
	}
}

// openSyncedStore resolves the cross-device sync configuration and opens the
// canonical session store with the matching at-rest codec, so that
// encrypted-on-disk sessions synced in from other surfaces (kode web, the
// dashboard) decode on read. It returns the store plus the resolved sync config
// (Enabled=false when sync is off) so the caller can also start the background
// syncer. It never fails: a SQLite open error falls back to the JSONL file
// store, and a bad sync config is logged and treated as disabled. Both assemble
// and the teleport lister open the store through here so they see the same data.
func openSyncedStore(ctx context.Context, home string, fs synchub.FileSettings) (sdksession.Store, synchub.Config) {
	syncCfg, syncErr := synchub.Resolve(home, fs)
	if syncErr != nil {
		slog.Warn("sync configuration ignored", "error", syncErr)
	}
	var syncCodec sdksession.Codec
	if syncCfg.Enabled {
		if c, cerr := sdksession.NewEncryptingCodec(syncCfg.Key); cerr == nil {
			syncCodec = c
		} else {
			slog.Warn("sync encryption disabled", "error", cerr)
			syncCfg.Enabled = false
		}
	}

	// Sessions persist to SQLite in the shared SDK's canonical korai.Session
	// format (teleport-compatible); if the database cannot be opened, fall back
	// to the JSONL file store so a storage error never blocks a session.
	sq, serr := sdksession.NewSQLiteStore(ctx, sessionsDBPath(home))
	if serr == nil {
		if syncCodec != nil {
			sq.WithCodec(syncCodec)
		}
		return sq, syncCfg
	}
	slog.Warn("opening sqlite session store; falling back to file store", "error", serr)
	fs2 := sdksession.NewFileStore(sessionsDir(home))
	if syncCodec != nil {
		fs2.WithCodec(syncCodec)
	}
	return fs2, syncCfg
}

// defaultActiveSyncInterval is the cadence at which the open conversation is
// checkpointed (re-saved so the sync cycle sweeps it up) when neither config nor
// env overrides it.
const defaultActiveSyncInterval = 3 * time.Minute

// minActiveSyncInterval clamps overly aggressive checkpointing so a misconfigured
// value cannot hammer the store or the hub.
const minActiveSyncInterval = 15 * time.Second

// resolveActiveSyncInterval picks the active-session checkpoint cadence:
// KORAI_SYNC_ACTIVE_INTERVAL wins, then the config sync.activeSyncInterval field,
// else the default. Values parse as a Go duration ("3m") or a bare integer of
// seconds; an unparseable value falls back to the default rather than failing
// the session.
func resolveActiveSyncInterval(s *config.SyncSettings) time.Duration {
	raw := strings.TrimSpace(os.Getenv("KORAI_SYNC_ACTIVE_INTERVAL"))
	if raw == "" && s != nil {
		raw = strings.TrimSpace(s.ActiveSyncInterval)
	}
	if raw == "" {
		return defaultActiveSyncInterval
	}
	d, err := parseDurationOrSeconds(raw)
	if err != nil {
		slog.Warn("invalid active-sync interval; using default", "value", raw, "error", err)
		return defaultActiveSyncInterval
	}
	if d < minActiveSyncInterval {
		d = minActiveSyncInterval
	}
	return d
}

// parseDurationOrSeconds accepts a Go duration ("3m") or a bare integer count of
// seconds ("180"), mirroring the hub poll-interval parser.
func parseDurationOrSeconds(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("want a duration like 3m or an integer of seconds")
	}
	return time.Duration(n) * time.Second, nil
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
func resolveSession(store sdksession.Store, wd string, opts runOptions) (id string, created time.Time, history []apiclient.Message) {
	switch {
	case opts.resumeID != "":
		sess, err := store.Load(opts.resumeID)
		if err == nil {
			return sess.ID, sess.Created, session.FromCanonicalMessages(sess.Messages)
		}
		slog.Warn("could not resume session; starting fresh", "id", opts.resumeID, "error", err)
	case opts.cont:
		if sess, ok, err := store.Latest(wd); err == nil && ok {
			return sess.ID, sess.Created, session.FromCanonicalMessages(sess.Messages)
		}
	}
	return session.NewID(), time.Now(), nil
}

// formatSessions renders the saved-session list for /resume.
func formatSessions(store sdksession.Store, wd string) string {
	sessions, err := store.List()
	if err != nil {
		return "could not list sessions: " + err.Error()
	}
	if len(sessions) == 0 {
		return "No saved sessions yet."
	}
	var b strings.Builder
	b.WriteString("Saved sessions (newest first) — /resume <id> to load:")
	shown := 0
	for _, s := range sessions {
		if s.CWD != wd {
			continue
		}
		fmt.Fprintf(&b, "\n  %s  %s  (%d msgs)  %s",
			s.ID, s.Updated.Local().Format("2006-01-02 15:04"), len(s.Messages), firstUserText(s.Messages))
		shown++
	}
	if shown == 0 {
		return "No saved sessions for this directory yet."
	}
	return b.String()
}

// firstUserText returns a short snippet of the first user message in a stored
// canonical session.
func firstUserText(msgs []korai.SessionMessage) string {
	for _, m := range msgs {
		if m.Role != string(apiclient.RoleUser) {
			continue
		}
		for _, blk := range m.Blocks {
			if tb, ok := blk.(korai.TextBlock); ok && strings.TrimSpace(tb.Text) != "" {
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
func buildCommands(home, wd string, registry *tool.Registry, modelList []string, models *apiclient.ModelSelector, modes *perm.ModeSelector, costTracker *cost.Tracker, sessStore sdksession.Store, snapLog *snapshot.Log) *command.Registry {
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
