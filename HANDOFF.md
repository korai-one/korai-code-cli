# Korai Code CLI — Session Handoff

> **You are the session taking over this project.** Read this file top to bottom
> before doing anything. It is self-contained: it tells you what Korai Code CLI is,
> the decisions already made, the rules you must follow, and exactly what to build
> first. Its companion is `AGENTS.md` (the ground rules) — treat that as binding law.

---

## 0. Mission

Build **Korai Code CLI** (`korai`): a terminal-based AI coding agent written in
**Go**, with a state-of-the-art TUI. It is an **original reimplementation** that
follows the *architecture* of a mature agentic CLI — an LLM-driven tool-calling
loop wrapped in an interactive terminal UI — plus a few experimental capabilities
(multi-agent coordination, proactive/scheduled agents, persistent memory,
verification agents).

Deliverable for the first milestone: a **working headless agent + a basic
interactive TUI + the core tools**, dogfoodable end-to-end. Full feature parity is
explicitly *not* the first milestone.

---

## 1. Provenance & the hard IP boundary (read this twice)

The architectural understanding behind this project came from analyzing a
**leaked proprietary source snapshot** of another product, held in a separate
research repository. That snapshot is **not** part of this project and must never
enter it.

**Rules — non-negotiable:**

- **Do NOT copy, paste, vendor, or line-by-line transliterate** any of that
  proprietary source into `korai-code-cli`.
- Build **original Go** guided by the *functional architecture* described here and
  by **public** resources: the official Anthropic Go SDK, the public MCP
  specification, the public Charm/Bubble Tea docs, and standard Go practice.
- If you ever feel like you're translating a specific source file rather than
  implementing a described behavior, **stop** — you've crossed the line.
- Korai is your own work product. Keep it that way; it's what makes the project
  clean to publish.

(The earlier ground-rules file, `AGENTS.md`, was drafted with TS-port language for
a *different* repo. In Korai, wherever it says "port the TS" or "cite the TS
source," read that as **"implement the described behavior"** — there is no source
to cite here. Everything else in `AGENTS.md` applies verbatim.)

---

## 2. Locked technology decisions

| Concern | Choice | Notes |
|---|---|---|
| Language | **Go 1.23+** | single static binary, great concurrency story |
| CLI parsing | **`spf13/cobra`** | |
| TUI framework | **Bubble Tea** (Elm architecture) | SOTA Go TUI; proven by other AI-agent CLIs |
| Styling/layout | **Lipgloss** + **`stickers`** (flexbox) | |
| TUI components | **Bubbles** (viewport, textinput, spinner, list) | |
| LLM API | **`anthropics/anthropic-sdk-go`** (official) | streaming + tool use |
| MCP | **`modelcontextprotocol/go-sdk`** (official) | |
| Tool input schemas | Go structs → **`invopop/jsonschema`** | validation is explicit code |
| Concurrency | **`golang.org/x/sync/errgroup`** + channels | |
| Logging | **`log/slog`** | structured; library code logs, never prints |
| Tests | stdlib `testing` + **`google/go-cmp`** | **`testify` is banned** for consistency |

**Two decisions still open — resolve before the relevant phase:**

1. **Image/PDF rendering in the TUI.** Bubble Tea can't render inline images.
   If inline images are in scope, evaluate **`vaxis`** (kitty-graphics/sixel) as the
   renderer instead. Default until decided: **Bubble Tea, no inline-image code.**
2. **Cloud/remote sessions.** The reference architecture has an IDE bridge + remote
   session infra. Default: **out of scope** for Korai's first milestones (local
   terminal only). Revisit only if explicitly requested.

---

## 3. The architecture to build

A conversation turn is one trip through an LLM tool-calling loop. Build it in these
layers (dependency arrows point downward; lower layers never import upper ones):

```
cmd/korai  →  tui  →  engine  →  { tool, perm, apiclient, mcp, context, config }
```

- **`engine`** — the agent loop. Assemble context → call the model (streaming) →
  if the response contains tool calls, execute them (permission-gated) → feed
  results back → repeat until no tool calls remain. **UI-agnostic and
  headless-first:** it emits events on a `<-chan engine.Event`; the TUI is just one
  consumer, `--print` mode is another. No UI code in the engine.
- **`tool`** — the frozen `Tool` interface (see `AGENTS.md` §5) + a registry. Each
  tool: name, model-facing description, struct-generated input schema, explicit
  validation, `Execute(ctx, …)`, and `ReadOnly`/`ConcurrencySafe`/`CheckPermission`
  declarations (fail-closed: default `false`/`ask`).
- **`tools/*`** — one package per tool. First milestone set: **Bash** (with sandbox
  + permission logic), **Read/Write/Edit**, **Grep/Glob**, **WebFetch/WebSearch**.
- **Streaming executor** — read-only/concurrency-safe tools run in parallel,
  mutating tools serially, every call permission-gated. This is where Go's
  goroutines+channels shine; make it clean.
- **`perm`** — allow/ask/deny decisions and modes (`default`, `plan`, `acceptEdits`,
  `bypassPermissions`). Values passed into the engine, never globals.
- **`apiclient`** — the only package that talks to the Anthropic SDK.
- **`mcp`** — MCP client; maps external MCP tools onto the `Tool` interface.
- **`context`** — assembles system + user context (working dir, git status, project
  instructions file, date) prepended to the conversation.
- **`config`** — settings hierarchy + app state as explicit, injected values
  (no global mutable singleton).
- **`tui`** — Bubble Tea. Strict Elm discipline: `Update` is pure/fast, all I/O in
  `tea.Cmd`s, `View` is pure rendering, no business logic. Renders the transcript,
  prompt input, streaming output, and permission prompts by consuming engine events.

Experimental features (later phases): multi-agent **coordinator** (spawn worker
agents via a Task tool, synthesize results — a natural fit for goroutines),
**scheduled/proactive** agents, **persistent memory**, a **verification** sub-agent
that runs the app/tests to confirm a change works.

---

## 4. Ground rules (binding)

`AGENTS.md` (companion file in this directory) is the constitution. The essentials:

1. **Build is always green** — `make check` (gofmt, goimports, build, vet,
   golangci-lint, `go test -race`) must pass before every commit. Never commit red.
2. **Behavior over syntax** — implement *what* the architecture does, idiomatically.
3. **Not verified ⇒ not done** — code that compiles isn't done; code with passing
   unit/golden tests is.
4. **Stay in your lane** — agents edit only their assigned module; the foundation
   (`tool`/`perm`/`engine` contracts, `Makefile`, `AGENTS.md`) is frozen and changes
   only through the coordinator.
5. **No globals, `ctx` first-arg and honored, wrapped errors (`%w`), `slog` only,
   approved-deps-only.** Full detail in `AGENTS.md`.

**Verification without a reference implementation:** rely on unit tests for all
branching logic (validation, permission decisions, parsing, layout) and golden
tests for engine/tool I/O. `teatest` for TUI golden-frame snapshots. Tests must be
hermetic — no network, injected clock, no map-ordering assumptions.

---

## 5. The build plan (phases)

- **Phase 0 — Foundation (do this first; see §6).** Module, Makefile, frozen `Tool`
  interface, Cobra skeleton, one compiling end-to-end headless slice. *No fan-out
  until this is green.*
- **Phase 1 — Headless engine.** The full loop in `--print` mode: context → stream →
  tool loop → result. No UI. **Milestone: a usable headless coding agent.**
- **Phase 2 — Core tools.** Bash, Read/Write/Edit, Grep/Glob, WebFetch/WebSearch.
- **Phase 3 — Permission engine.** allow/ask/deny + modes; headless first.
- **Phase 4 — TUI.** Bubble Tea REPL, streaming display, permission dialogs. The
  long pole; re-architect into Elm, don't transliterate.
- **Phase 5 — Services.** MCP, OAuth login, config/settings, persistent memory, LSP.
- **Phase 6 — Experimental + parity.** Coordinator/sub-agents, skills, hooks, slash
  commands, cross-platform polish.

**Effort framing:** with a disciplined coordinator + worker swarm, Phases 0–3 plus a
basic Phase 4 are achievable in roughly a week. Full parity is not — the TUI and
verification are inherently serial. Optimize for a dogfoodable MVP, then iterate.

---

## 6. Phase 0 — your first actions

Do these in order. Each step must leave the build green.

1. `go mod init github.com/Nevaero/korai-code-cli` (Go 1.23+).
2. Create the **layout**: `cmd/korai/`, `internal/{engine,tool,tools,perm,apiclient,mcp,context,config,tui}/`, `testdata/`.
3. Write the **`Makefile`** with the `check` target (gofmt -l, goimports -l, build,
   vet, golangci-lint run, `go test -race ./...`) and add `.golangci.yml`
   (errcheck, govet, staticcheck, ineffassign, unused, gocritic, revive, bodyclose,
   contextcheck, errorlint).
4. Copy `AGENTS.md` to the **repo root** so every agent/tool auto-loads it.
5. Define the **frozen `Tool` interface** in `internal/tool` exactly as specified in
   `AGENTS.md` §5, plus a minimal registry and `Deps`/`Result` types.
6. Wire the **Cobra skeleton** in `cmd/korai` (`korai`, `korai --print "<prompt>"`,
   `--model`, `--help`). No business logic in `cmd`.
7. Build **one end-to-end vertical slice** that compiles, runs, and is verifiable:
   `--print` sends a prompt through `apiclient` to the model, streams the response
   to stdout, and executes **one trivial read-only tool** (e.g. a `ReadFile` tool)
   through the engine's tool loop. Add a golden test for it.
8. Add a fresh **`README.md`** (Korai Code CLI — what it is, build, run) and CI that
   runs `make check`.

When step 7 is green and runnable, Phase 0 is done — *then* fan out workers per
`AGENTS.md` §8.

---

## 7. What NOT to do

- Do **not** import the leaked proprietary source (see §1). Original Go only.
- Do **not** add dependencies outside `AGENTS.md`'s allowlist without coordinator
  sign-off.
- Do **not** put business logic in the TUI or let the engine know about the TUI.
- Do **not** introduce global mutable state, package import cycles, or `testify`.
- Do **not** start multi-agent fan-out before the Phase-0 slice is green.
- Skip for now (confirm scope before building): cloud/remote/IDE-bridge infra,
  telemetry, the auto-updater, inline image rendering.

---

*Companion files in this directory: `AGENTS.md` (binding ground rules).
Start at §6.*
