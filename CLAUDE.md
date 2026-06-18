# CLAUDE.md

Guidance for Claude Code (and any agent) working in this repository.

## What this repository is

**Korai Code CLI** (`korai`) — a terminal-based AI coding agent written in **Go**,
built to run on the **Korai SDK**: a decentralized, peer-to-peer encrypted
inference network. It is an **original implementation** following the architecture
of an LLM-driven tool-calling loop wrapped in a terminal UI.

Two documents are authoritative and you must follow them:

- **`AGENTS.md`** — the binding ground rules (the constitution). Read it before
  writing code. Where it and your instinct disagree, it wins.
- **`HANDOFF.md`** — the mission, locked tech decisions, and the phase plan.

### The `src/` directory is reference-only — never copy from it

`src/` is a TypeScript snapshot of another product, kept **only** as architectural
reference. Per `AGENTS.md` §0 and `HANDOFF.md` §1: do **not** copy, paste, vendor,
or transliterate any of it into the Go code. Implement described behavior in
original Go. If you find yourself translating a `src/` file line-by-line, stop.

## Build, test, lint

One command gates everything — it **must** pass before every commit:

```
make check   # gofmt -l · goimports -l · go vet · golangci-lint · go test -race
make build   # produces ./bin/korai
```

- Go 1.23+ (the toolchain here is newer). `golangci-lint` v2 config in `.golangci.yml`.
- Tests are stdlib `testing` + `google/go-cmp`. **`testify` is banned.** TUI tests
  use `teatest`. Tests must be hermetic: no network, no real clock, no map-order
  assumptions. ~4.9K LOC of code, ~4K LOC of tests across 27 packages.
- Run a single package's tests with `go test ./internal/<pkg>/`.

## The strangler-fig boundary (the most important convention)

The inference backend is swappable. **All** model access goes through
`internal/apiclient`, behind the `apiclient.Client` interface. Three locked rules
(`AGENTS.md` §4 items 8–10) keep the eventual Korai SDK swap clean:

1. Every direct `anthropic-sdk-go` call site inside `internal/apiclient` carries a
   `// TODO KORAI SDK` comment — these are the exact swap points.
2. No `anthropic.*` type crosses the `apiclient` boundary. `apiclient` defines its
   own types (`Request`, `Message`, `ContentBlock`, `Event`, `Usage`, …) and
   converts at the edge. This is the anti-corruption layer.
3. `apiclient.Client` is an interface; `AnthropicClient` implements it today, a
   `KoraiClient` will implement it later. Nothing above `apiclient` knows which
   backend is live.

When you add anything that consumes backend data (tokens, model, streaming),
route it through `apiclient`'s own types — do not leak SDK types upward.

## Architecture & layout

Dependency direction (must not be violated):
`cmd → tui → engine → { tool, perm, apiclient, mcp, context, config, … }`.
Lower layers never import `tui`/`engine`. Tool packages never import each other.

```
cmd/korai/            # Cobra wiring + session assembly. No business logic in main.
  main.go             #   flags, --print (headless) vs TUI dispatch, logging
  session.go          #   assemble(): config → registry → perm → commands → hooks
internal/
  engine/             # the agent loop: context → stream → tool loop → repeat.
                      #   UI-agnostic; emits events on <-chan engine.Event.
                      #   Options: WithHooks, WithModelSelector, WithUsageRecorder.
  apiclient/          # THE inference boundary (strangler fig). Own types; Client
                      #   interface; AnthropicClient; ModelSelector; Usage.
  tool/               # frozen Tool interface + Registry + Schema[T] helper.
  tools/              # one package per tool (see below). tools.go = RegisterAll.
  perm/               # permission engine: Mode, Decision, Rules, Asker, Engine.
  command/            # slash-command contract + built-ins + /model /cost /compact.
  skill/              # markdown skills → slash commands; bundled via go:embed.
  hook/               # config-driven lifecycle hooks (shell commands).
  mcp/                # MCP client; adapts external server tools onto tool.Tool.
  memory/             # file-backed, capped persistent memory store.
  compact/            # conversation summarization via apiclient.Client.
  cost/               # token tracking + USD estimate (prices.go is swappable).
  config/             # settings hierarchy (defaults < user < project < local < flags).
  context/            # system-context assembly (workdir, git status, date, AGENTS.md).
  prompt/             # agent system prompt + Compose with env context.
  tui/                # Bubble Tea REPL (Elm architecture).
testdata/             # golden files + fixtures.
src/                  # TS reference snapshot — READ-ONLY, never copy from.
```

### The engine loop (`internal/engine`)

`Run(ctx, messages, system) <-chan Event` spawns the loop in a goroutine and
streams events. Per iteration: `buildRequest` (stamps the current model from the
selector) → `streamTurn` (calls `apiclient.Client.Complete`, accumulates text +
tool calls + usage) → if tool calls, `executeTools` (each gated by `dispatchTool`)
→ append to history → repeat until no tool calls. `DoneEvent.Messages` carries the
full post-turn history so the REPL keeps context. Hooks fire at `SessionStart`,
`PreToolUse` (can veto), `PostToolUse`. The model never imports the engine.

### Tools (`internal/tool`, `internal/tools/*`)

`tool.Tool` is frozen (coordinator-owned). Each tool is its own package with
`New()`, an `Input` struct (json + jsonschema tags), `InputSchema()` returning
`tool.Schema[Input]()` (inlined, no `$ref`), explicit validation in `Execute`,
and **fail-closed** `ReadOnly`/`ConcurrencySafe`/`CheckPermission` (default
false/ask). The built-in set (`tools.RegisterAll`): `ReadFile`, `Write`, `Edit`,
`Bash`, `Grep`, `Glob`, `WebFetch`, `WebSearch`. Plus `Remember` (memory) and
`Task` (sub-agent), registered per-session in `cmd/session.go` because they need
runtime dependencies. **To add a tool:** copy `internal/tools/readfile/` as the
template, then add it to `tools.go`.

### Permissions (`internal/perm`)

`Engine.Resolve` order (fail-closed): bypass short-circuits → deny rule → base
`Deny`/`Allow` → base `Ask` resolved by an allow rule or the `Asker`. The TUI's
`Asker` bridges the engine's blocking call to the Elm loop over channels; headless
uses `DenyAsker` (or `AllowAsker` with `--yes`).

### TUI (`internal/tui`)

Strict Elm: `Update` is pure/fast, all I/O in `tea.Cmd`s, `View` only renders.
Channels (engine events, permission requests, compaction) are bridged into
messages via Cmds. Slash commands are intercepted in `submit()`.

### Permission modes & plan mode

`perm.ModeSelector` holds the active mode, shared by the perm engine, `/plan`,
and the TUI (shift+tab cycles default → acceptEdits → plan; a badge shows the
current mode). Plan mode enforces read-only at the tool layer; a dynamic system
suffix (`engine.WithSystemSuffix`) tells the agent to investigate then call the
`ExitPlanMode` tool, which presents a plan for approval (interactive in the TUI,
`--yes` headless) and on approval switches to acceptEdits so the work proceeds.

### Slash commands & skills

Built-ins: `/help`, `/clear`, `/quit`, `/tools`, `/model`, `/cost`, `/compact`, `/plan`.
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
  `cost.Tracker`) and verified under `go test -race`.
- `log/slog` only; library code logs, never prints (the TUI owns the screen — in
  TUI mode logs go to a file/discard so stderr can't corrupt the alt-screen).
- Doc comment on every exported symbol, starting with its name.
- Approved dependencies only (`AGENTS.md` end). Ask before adding any.

## Status

Phases 0–6 of `HANDOFF.md` are complete: headless engine, 8 core tools + memory +
sub-agent + MCP, permission engine, Bubble Tea TUI, services (config/memory/MCP),
and experimental/parity features (slash commands, hooks, skills, Task sub-agent,
`/model` `/cost` `/compact`). Deferred with rationale (`AGENTS.md` §7): OAuth
(belongs to the Korai SDK), LSP (off the MVP path).

**Not yet verified against a live model** — there is no API key/network in the
build environment. Correctness is proven by mock-client golden/`teatest` tests
plus an assembly smoke test that reaches a real auth error. A live `--print`
smoke test is the natural next step once a backend or key is available.

## Editing guidance

- Keep changes small and single-purpose; the foundation (`tool`/`perm`/`engine`
  contracts, `Makefile`, `AGENTS.md`) is frozen — change it only deliberately.
- When you touch a tool, keep its logic/validation/permission consistent.
- Run `make check` before every commit; never commit red.
- Develop on the designated feature branch; push to `nevaero/korai-code-cli`.
