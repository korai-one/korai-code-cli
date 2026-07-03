# CLAUDE.md

Guidance for Claude Code (and any agent) working in this repository.

## What this repository is

**Korai Code CLI** (`korai`) — a terminal-based AI coding agent written in **Go**,
built to run on the **Korai SDK**: a decentralized, peer-to-peer encrypted
inference network. It is an **original implementation** following the architecture
of an LLM-driven tool-calling loop wrapped in a terminal UI. The Korai SDK is now
a live dependency (`korai-one/korai-sdk-go`) — `KoraiClient` talks to the P2P
network today, alongside an Anthropic backend and a set of local/LAN worker
backends (see the strangler-fig section).

Two documents are authoritative and you must follow them:

- **`AGENTS.md`** — the binding ground rules (the constitution). Read it before
  writing code. Where it and your instinct disagree, it wins.
- **`HANDOFF.md`** — the mission, locked tech decisions, and the phase plan.

### The `src/` reference snapshot has been removed

`src/` was a read-only TypeScript snapshot of another product, kept as
architectural reference. It has been **deleted** from the repo (commit
`b4b4efd`). The AGENTS.md §0 / HANDOFF.md §1 rule still stands in spirit: this is
an **original Go implementation** — do not copy, vendor, or transliterate code
from any external product. Implement described behavior in original Go.

## Build, test, lint

One command gates everything — it **must** pass before every commit:

```
make check   # gofmt -l · goimports -l · go vet · golangci-lint · go test -race
make build   # produces ./bin/korai
```

- Go 1.23+ (the toolchain here is newer). `golangci-lint` v2 config in `.golangci.yml`.
- Tests are stdlib `testing` + `google/go-cmp`. **`testify` is banned.** TUI tests
  use `teatest`. Tests must be hermetic: no network, no real clock, no map-order
  assumptions. ~15K LOC of code, ~10K LOC of tests across 29 internal packages.
- Run a single package's tests with `go test ./internal/<pkg>/`.
- Some packages depend on CGO-free `modernc.org/sqlite`; the release build compiles
  native binaries per-OS (see `.github` release workflow).

## The strangler-fig boundary (the most important convention)

The inference backend is swappable. **All** model access goes through
`internal/apiclient`, behind the `apiclient.Client` interface
(`Complete(ctx, Request) <-chan Event`). Three locked rules (`AGENTS.md` §4 items
8–10) keep the backends decoupled:

1. Every direct `anthropic-sdk-go` call site inside `internal/apiclient` carries a
   `// TODO KORAI SDK` comment — the historical swap points (still present in
   `anthropic.go`). The Korai swap itself is **done**: `korai.go` consumes
   `korai-sdk-go` directly.
2. No backend SDK type crosses the `apiclient` boundary. `apiclient` defines its
   own types (`Request`, `Message`, `ContentBlock`, `TextBlock`, `ToolCallBlock`,
   `ToolResultBlock`, `ImageBlock`, `Event`, `Usage`, …) and converts at the edge.
   This is the anti-corruption layer.
3. `apiclient.Client` is an interface with several implementations, chosen at
   session assembly from the environment. Nothing above `apiclient` knows which
   backend is live.

Backends that implement `apiclient.Client` today:

- **`AnthropicClient`** — the Anthropic API (`ANTHROPIC_API_KEY`).
- **`KoraiClient`** — the Korai P2P network via `korai-sdk-go` (`KORAI_API_KEY`,
  optional `KORAI_BASE_URL`; default orchestrator `https://korai-eu.fly.dev`).
  Also used to reach a co-located worker that exposes only the loopback
  OpenAI-compatible HTTP endpoint.
- **`LocalWorkerClient`** — the direct binary channel (`internal/localproto`) to a
  co-located worker over a **Unix socket** (`NewLocalWorkerClient`) or to a
  home/LAN inference server over **TCP** (`NewLocalWorkerClientTCP`, with an
  optional shared token).

When you add anything that consumes backend data (tokens, model, streaming,
images), route it through `apiclient`'s own types — do not leak SDK types upward.

## Architecture & layout

Dependency direction (must not be violated):
`cmd → tui → engine → { tool, perm, apiclient, mcp, context, config, … }`.
Lower layers never import `tui`/`engine`. Tool packages never import each other.

```
cmd/korai/            # Cobra wiring + session assembly. No business logic in main.
  main.go             #   flags, --print (headless) vs TUI dispatch, logging
  session.go          #   assemble(): backend select → registry → perm → commands → hooks → sessions
  serve.go            #   `korai serve`: run the engine behind a WebSocket endpoint
  mentions.go         #   @-file mention expansion + image attachment for the prompt
internal/
  engine/             # the agent loop: context → stream → tool loop → repeat.
                      #   UI-agnostic; emits events on <-chan engine.Event.
                      #   Options: WithHooks, WithModelSelector, WithUsageRecorder,
                      #   WithSystemSuffix, WithAutoCompact. Enqueue() for mid-turn steering.
  apiclient/          # THE inference boundary (strangler fig). Own types; Client
                      #   interface; AnthropicClient, KoraiClient, LocalWorkerClient;
                      #   ModelSelector; Usage; fenced-tool-call parsing (fence.go).
  tool/               # frozen Tool interface + Registry + Schema[T] helper + Deps (WorkDir, LSP).
  tools/              # one package per tool (see below). tools.go = RegisterAll.
  perm/               # permission engine: Mode, Decision, Rules, Asker, Engine.
  command/            # slash-command contract + built-ins + /model /cost /compact /plan
                      #   /resume /revert /snapshots /about.
  skill/              # markdown skills → slash commands; bundled via go:embed.
  hook/               # config-driven lifecycle hooks (shell commands).
  mcp/                # MCP client; adapts external server tools onto tool.Tool.
  memory/             # file-backed, capped persistent memory store.
  compact/            # conversation summarization via apiclient.Client.
  cost/               # token tracking + USD estimate (prices.go is swappable).
  config/             # settings hierarchy (defaults < user < project < local < flags);
                      #   adds LSP toggle and Checks (RunChecks commands).
  context/            # system-context assembly (workdir, git status, date, AGENTS/CLAUDE.md).
  prompt/             # agent system prompt + Compose with env context + PlanNote.
  session/            # per-turn conversation persistence: SQLite store (sqlite.go) with
                      #   JSONL file-store fallback; Codec seam for at-rest encryption.
  snapshot/           # shadow-git worktree checkpoints for /revert + /snapshots.
  lsp/                # language-server diagnostics (powernap) appended to Edit/Write.
  repomap/            # ranked, budget-fitted repository map via tree-sitter symbols.
  todo/               # session-scoped concurrency-safe todo list (TodoWrite tool).
  patch/              # codex apply_patch format parser (multi-file patches).
  editmatch/          # multi-strategy fuzzy string replacer used by the Edit tool.
  localproto/         # wire contract for the direct local/LAN binary channel.
  localworker/        # discovery + reachability of a co-located/LAN worker.
  proto/              # JSON wire protocol for `korai serve` (client ↔ engine).
  wsasker/            # perm.Asker over WebSocket for serve mode.
  wsevent/            # bridges engine events onto the serve WebSocket wire.
  csync/              # generic concurrency-safe containers.
  tui/                # Bubble Tea REPL (Elm architecture); diff review, @-mentions,
                      #   search, plan mode, snapshots, steering, image attach.
testdata/             # golden files + fixtures.
```

### The engine loop (`internal/engine`)

`Run(ctx, messages, system) <-chan Event` spawns the loop in a goroutine and
streams events. Per iteration: `buildRequest` (stamps the current model from the
selector, appends any system suffix) → `streamTurn` (calls `apiclient.Client.Complete`,
accumulates text + tool calls + usage) → if tool calls, `executeTools` (each gated
by `dispatchTool`) → append to history → repeat until no tool calls. Before each
iteration `drainSteering` folds any mid-turn user input (`Enqueue`) into history.
`WithAutoCompact` summarizes history that grows past `compact.DefaultThreshold`.
Event types: `TextEvent`, `ToolStartEvent`, `ToolResultEvent`, `DoneEvent`
(carries full post-turn history), `CompactedEvent`, `ErrorEvent`. Hooks fire at
`SessionStart`, `PreToolUse` (can veto), `PostToolUse`. The model never imports
the engine.

### Tools (`internal/tool`, `internal/tools/*`)

`tool.Tool` is frozen (coordinator-owned). Each tool is its own package with
`New()`, an `Input` struct (json + jsonschema tags), `InputSchema()` returning
`tool.Schema[Input]()` (inlined, no `$ref`), explicit validation in `Execute`,
and **fail-closed** `ReadOnly`/`ConcurrencySafe`/`CheckPermission` (default
false/ask).

The built-in set (`tools.RegisterAll`): `ReadFile`, `Write`, `Edit`, `ApplyPatch`,
`Bash`, `Grep`, `Glob`, `RepoMap`, `WebFetch`, `WebSearch`. Registered per-session
in `cmd/session.go` because they need runtime dependencies: `Remember` (memory),
`RunChecks` (project verification commands from config), `lsp_diagnostics` +
`lsp_references` (on-demand, only when LSP is enabled), `Task` (sub-agent),
`TodoWrite` (session task list), `ExitPlanMode`.

Two registries: the **sub-agent** set has every tool **except** `Task`,
`TodoWrite`, and `ExitPlanMode`, so a spawned sub-agent cannot recurse or manage
session-level concerns; the **main** set adds those three. `Edit`/`Write` append
LSP diagnostics to their result (via `tool.Deps.LSP`) so the model self-corrects.

**To add a tool:** copy `internal/tools/readfile/` as the template, then add it to
`tools.go` (or register it per-session in `cmd/session.go` if it needs runtime deps).

### Permissions (`internal/perm`)

`Engine.Resolve` order (fail-closed): bypass short-circuits → deny rule → base
`Deny`/`Allow` → base `Ask` resolved by an allow rule or the `Asker`. The TUI's
`Asker` bridges the engine's blocking call to the Elm loop over channels; headless
uses `DenyAsker` (or `AllowAsker` with `--yes`); serve mode uses `wsasker` (round-
trips the prompt to the WebSocket client).

### TUI (`internal/tui`)

Strict Elm: `Update` is pure/fast, all I/O in `tea.Cmd`s, `View` only renders.
Channels (engine events, permission requests, compaction) are bridged into
messages via Cmds. Slash commands are intercepted in `submit()`. Features include
a diff review in the write permission prompt, `@`-file mentions, image attachment,
in-scrollback search, plan mode, snapshots, and mid-turn steering.

### Permission modes & plan mode

`perm.ModeSelector` holds the active mode, shared by the perm engine, `/plan`,
and the TUI (shift+tab cycles default → acceptEdits → plan; a badge shows the
current mode). Plan mode enforces read-only at the tool layer; a dynamic system
suffix (`engine.WithSystemSuffix` → `prompt.PlanNote`) tells the agent to
investigate then call the `ExitPlanMode` tool, which presents a plan for approval
(interactive in the TUI, `--yes` headless) and on approval switches to acceptEdits
so the work proceeds.

### Sessions (`internal/session`) & resume

Conversations auto-save after every turn. The primary store is **SQLite**
(`~/.korai/sessions.db`); if the database cannot be opened, assembly falls back to
a **JSONL file store** (`~/.korai/sessions/<id>.jsonl`) so a storage error never
blocks a session. The `ContentBlock` interface is persisted via a tagged DTO,
keeping `apiclient` free of JSON concerns. Message lines pass through a
`session.Codec` — the seam for at-rest encryption: today the plaintext
`PlainCodec` (`"none"`) is the only one; a future encrypting codec is recorded by
name so `Load` selects the matching decoder. `--continue`/`-c` resumes the latest
session for the cwd, `--resume <id>` a specific one, and `/resume` lists/loads them
live. The engine auto-compacts when history grows past `compact.DefaultThreshold`.

### Snapshots (`internal/snapshot`) & /revert

A shadow-git worktree checkpoint is taken before each turn; `/revert` restores one
and `/snapshots` renders the in-session (label, id) history. The `Manager` is a
no-op when git is absent. Snapshot repos live under `~/.korai/snapshots`, one per
worktree.

### Serve mode (`cmd/korai/serve.go`, `internal/proto`)

`korai serve` runs the same Go engine behind a WebSocket endpoint (`GET /ws`) so a
thin client (Tauri desktop webview, browser, mobile) drives it with no client-side
reimplementation. One session per process; each connection gets its own asker,
engine, and history. The JSON wire protocol is `internal/proto`; `wsevent` bridges
engine events out and `wsasker` round-trips permission prompts. The bound address
is printed to stdout (`KORAI_KODE_LISTEN=…`) for a parent process to discover.
Origin allow-list and an optional `--auth-token` gate browser connections.

### Local & LAN worker backends (`internal/localworker`, `internal/localproto`)

At assembly, a local worker takes precedence over the networked backends:
`--local-worker-url` / `KORAI_LOCAL_WORKER_URL` (loopback OpenAI-HTTP), or a worker
that advertised itself in `~/.korai/local-worker.json` and passes a health probe.
`--local-worker-addr` / `KORAI_LOCAL_WORKER_ADDR` (+ `KORAI_LOCAL_WORKER_TOKEN`)
reaches a home/LAN inference server over the direct binary channel on TCP.
`localworker.Resolve` picks the endpoint; `session.go` maps it to the matching
`apiclient.Client` (Unix-socket direct, TCP direct, or loopback HTTP).

### Slash commands & skills

Built-ins: `/help`, `/clear`, `/quit`, `/tools`, `/about`, `/model`,
`/worker_mode` (switch inference between the local worker and the remote
backend), `/cost`, `/compact`, `/plan`, `/resume`, `/revert`, `/snapshots`.
Bundled skills (`/commit`, `/review`) are embedded via `go:embed` in
`internal/skill/builtins/`. Any `.korai/skills/*.md` becomes a command;
project/user skills override bundled ones by name. **To add a skill:** drop a
markdown file (optional `---\ndescription: …\n---` front matter) in
`internal/skill/builtins/` (bundled) or `.korai/skills/` (project).

## Conventions (from AGENTS.md — enforced by lint)

- Errors: return, don't panic in library code; wrap with `%w`; sentinels are
  `ErrXxx`. Error strings are lowercase (staticcheck ST1005) — note tool error
  prefixes are lowercased (`"bash: …"`, not `"Bash: …"`).
- `ctx context.Context` is the first parameter of anything doing I/O; honor
  cancellation; never store a `Context` in a struct (the TUI model stores a
  `CancelFunc`, which is fine).
- No global mutable state — inject dependencies. Concurrency via `errgroup` +
  channels; shared mutable values are mutex-guarded (e.g. `ModelSelector`,
  `cost.Tracker`, `engine.steer`, `csync` containers) and verified under
  `go test -race`.
- `log/slog` only; library code logs, never prints (the TUI owns the screen — in
  TUI mode logs go to a file/discard so stderr can't corrupt the alt-screen).
- Doc comment on every exported symbol, starting with its name.
- Approved dependencies only (`AGENTS.md` end). Ask before adding any. Current
  notable deps: `korai-one/korai-sdk-go`, `anthropic-sdk-go`, `charm.land/bubbletea`,
  `coder/websocket`, `smacker/go-tree-sitter`, `charmbracelet/x/powernap` (LSP),
  `modernc.org/sqlite`.

## Status

Phases 0–6 of `HANDOFF.md` are complete, plus a large parity/experimental wave on
top: the Korai SDK backend is live; local Unix-socket and LAN-over-TCP worker
channels; `korai serve` (WebSocket engine for web/desktop clients); SQLite session
persistence with JSONL fallback; shadow-git snapshots (`/revert`, `/snapshots`);
language-server diagnostics; tree-sitter `RepoMap`; `ApplyPatch` + fuzzy `Edit`;
`RunChecks`; on-demand LSP query tools; `TodoWrite`; image/vision input; and
mid-turn steering. Deferred with rationale (`AGENTS.md` §7): OAuth (belongs to the
Korai SDK).

The pipeline is exercised by mock-client golden/`teatest` tests plus an assembly
smoke test. Live-model verification depends on a backend/key being present in the
run environment.

## Editing guidance

- Keep changes small and single-purpose; the foundation (`tool`/`perm`/`engine`
  contracts, `Makefile`, `AGENTS.md`) is frozen — change it only deliberately.
- When you touch a tool, keep its logic/validation/permission consistent.
- Run `make check` before every commit; never commit red.
- Develop on the designated feature branch; `origin` is
  `github.com/korai-one/korai-code-cli` (the Go module path is `Nevaero/korai-code-cli`).
