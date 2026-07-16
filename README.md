# Korai Code CLI

A terminal-based AI coding agent built to run on the **Korai SDK** — a decentralized, peer-to-peer encrypted inference network. Run a full agentic coding loop from your terminal without routing prompts through any centralized API.

---

## What it is

`korai` is a coding agent CLI written in Go. It drives an LLM tool-calling loop that can read and edit files, run shell commands, search codebases, remember facts across sessions, and delegate to sub-agents — all from an interactive terminal UI or in headless `--print` mode.

The inference backend sits behind a strict **strangler-fig boundary** (`internal/apiclient`): the agent talks only to an `apiclient.Client` interface and never to a vendor SDK directly. That interface is implemented by `KoraiClient` — so requests are encrypted end-to-end and routed across the decentralized **Korai P2P network** instead of a single-operator bottleneck — and by local/LAN worker clients, none of whose SDK types cross the boundary. Korai runs on the Korai SDK; there is no third-party API backend.

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| CLI parsing | [Cobra](https://github.com/spf13/cobra) |
| TUI | [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm architecture) |
| Styling / layout | [Lipgloss](https://github.com/charmbracelet/lipgloss) + [stickers](https://github.com/76creates/stickers) |
| TUI components | [Bubbles](https://github.com/charmbracelet/bubbles) |
| Inference boundary | `apiclient.Client` interface — Korai P2P SDK + local/LAN workers |
| MCP | [go-sdk](https://github.com/modelcontextprotocol/go-sdk) (official) |
| Tool schemas | Go structs → [invopop/jsonschema](https://github.com/invopop/jsonschema) |
| Concurrency | `golang.org/x/sync/errgroup` + channels |
| Logging | `log/slog` |

---

## Features

- **Agentic loop** — context assembly → inference → permission-gated tool execution → repeat until done, in an interactive TUI or headless `--print` (with a `--output-format json` JSONL mode for embedding).
- **Tools** — `ReadFile`, `Write`, `Edit` (multi-strategy fuzzy matching), `ApplyPatch` (codex multi-file patches), `Bash`, `Grep`, `Glob`, `RepoMap` (tree-sitter symbol map), `WebFetch`, plus `Remember` (persistent memory), `RunChecks` (project verification commands), `TodoWrite`, `Task` (sub-agent), and on-demand `lsp_diagnostics`/`lsp_references` when a language server is available. (`WebSearch` is present but not yet wired to a provider.)
- **Self-correcting edits** — `Edit`/`Write`/`ApplyPatch` append live language-server diagnostics to their results, so the model fixes errors the same turn.
- **Permission engine** — fail-closed allow / ask / deny with modes `default`, `acceptEdits`, `plan`, `bypassPermissions`. Cycle with **shift+tab**; a badge shows the active mode. Allow/deny rules gate tools by name.
- **Plan mode** — read-only investigation that ends with an `ExitPlanMode` proposal you approve before any edits run.
- **Sessions & resume** — conversations auto-save to a **SQLite** store (`~/.korai/sessions.db`), with a JSONL file-store fallback. Resume the latest with `--continue`/`-c`, a specific one with `--resume <id>`, or browse with `/resume`. Sessions are linear (no branching yet); a `Codec` seam is in place for future at-rest encryption.
- **Snapshots** — a shadow-git checkpoint is taken before each turn; `/revert` restores files to an earlier turn and `/snapshots` lists them (no-op when git is absent).
- **Local & LAN workers** — run inference on a co-located worker (Unix socket or loopback HTTP) or a home/LAN box over TCP; `--local` runs key-free. Switch between the local worker and the Korai network live with `/worker_mode`.
- **Serve mode** — `korai serve` runs the same engine behind a WebSocket endpoint so a desktop/web/mobile client can drive it (one session per process).
- **Vision, steering, condensing** — attach images via `@`-mentions for vision models; type while the agent runs to steer it mid-turn; verbose Bash output is auto-condensed for the model while the full output stays on screen. A context-usage meter shows how close you are to auto-compaction.
- **Auto-compaction** — long conversations are summarized before they overflow the context window; trigger manually with `/compact`.
- **Slash commands** — `/help`, `/clear`, `/quit`, `/tools`, `/about`, `/model`, `/worker_mode`, `/cost`, `/compact`, `/plan`, `/resume`, `/revert`, `/snapshots`. Type `/` for a fuzzy command menu.
- **@-file mentions** — type `@` to fuzzy-pick a workspace file; its content is inlined into the prompt sent to the model.
- **Skills** — markdown files become slash commands. Bundled `/commit` and `/review`; drop `.korai/skills/*.md` to add your own (project and user skills override bundled ones by name).
- **MCP** — connect external stdio MCP servers from config; their tools register alongside the built-ins.
- **Hooks** — run shell commands at `SessionStart`, `PreToolUse` (can veto a call), and `PostToolUse`.
- **Cost tracking** — token usage and a USD estimate via `/cost`.

---

## Architecture

A conversation turn flows through layered packages. Dependency arrows point downward; lower layers never import upper ones.

```
cmd/korai  →  tui  →  engine  →  { tool, perm, apiclient, mcp, context, config, ... }
```

- **`engine`** — the agent loop. Assembles context → streams inference → executes tools (permission-gated) → feeds results back → repeats until no tool calls remain. Headless-first: emits events on a `<-chan engine.Event`. No UI code here.
- **`apiclient`** — the inference boundary (strangler fig). Defines its own `Request`/`Message`/`ContentBlock`/`Event`/`Usage` types; no vendor SDK type crosses it. `KoraiClient` (P2P network) and the local/LAN worker clients implement it.
- **`tool` / `tools/*`** — the `Tool` interface + registry, then one package per tool. Each declares name, description, JSON schema, validation, `Execute`, and `ReadOnly`/`ConcurrencySafe`/`CheckPermission` (fail-closed by default).
- **`perm`** — allow / ask / deny resolution and the shared mode selector.
- **`command` / `skill`** — slash-command contract, built-ins, and markdown skills compiled into commands.
- **`session`** — SQLite session store (with a JSONL file-store fallback) and resume, with a codec seam for future encryption. **`snapshot`** — shadow-git per-turn checkpoints for `/revert`.
- **`compact` / `cost`** — conversation summarization and token/USD accounting (both behind `apiclient`).
- **`memory`** — file-backed, capped persistent memory.
- **`mcp`** — MCP client; maps external MCP tools onto the `Tool` interface.
- **`context` / `prompt`** — system + user context (working dir, git status, project instructions, date) and prompt composition.
- **`config`** — settings hierarchy (defaults < user < project < local < flags) as explicit injected values; no global singleton.
- **`hook`** — config-driven lifecycle shell hooks.
- **`tui`** — Bubble Tea REPL. Strict Elm discipline: `Update` is pure, all I/O in `tea.Cmd`s, `View` is pure rendering. Renders the transcript, streaming output, prompt input, and permission dialogs by consuming engine events.

---

## Build

```sh
# Prerequisites: Go 1.23+, golangci-lint
make check    # gofmt, goimports, vet, golangci-lint, race tests
make build    # produces ./bin/korai
```

---

## Usage

```sh
# Interactive TUI
korai

# Headless / scripting (prompt as a positional arg or piped on stdin)
korai --print "refactor the auth module to use middleware"
git diff | korai --print "write a commit message for this"

# Resume work
korai --continue                 # latest session for this directory
korai --resume 20260619-074412-a1b2c3d4

# With a specific model
korai --model <model-id> --print "explain this codebase"
```

Configuration lives under `.korai/` (project) and `~/.korai/` (user): `settings.json`, `skills/*.md`, `MEMORY.md`, and saved `sessions/`.

**Backend:** set `KORAI_API_KEY` to use the Korai P2P inference network (optionally `KORAI_BASE_URL`); the model defaults to the `auto` routing alias. Or run against a local/LAN worker with `--local` (no key required). The key can live in a `.env` file (gitignored) — see `.env.example`.

---

## Project status

Phases 0–6 are complete: headless engine, the core tool set plus memory, sub-agent, and MCP, the permission engine, the Bubble Tea TUI, services (config / memory / MCP / sessions), and parity features (slash commands, hooks, skills, plan mode, auto-compaction). On top of that, a larger wave: the **Korai P2P backend is live** (`KoraiClient`), local Unix-socket and LAN-over-TCP workers, `korai serve` (WebSocket engine), SQLite sessions with JSONL fallback, shadow-git snapshots, tree-sitter `RepoMap`, language-server diagnostics, `ApplyPatch` + fuzzy `Edit`, image/vision input, and mid-turn steering. **The Anthropic backend and its SDK have been removed — inference is Korai-only + local/LAN workers.** Deferred with rationale: OAuth (belongs to the Korai SDK).

The CLI is verified by mock-client golden and `teatest` tests under `go test -race`, plus an end-to-end assembly smoke test.

See `HANDOFF.md` for the build plan and `AGENTS.md` for contribution ground rules.

| Phase | Status | Description |
|---|---|---|
| 0 — Foundation | ✅ Done | Module, Makefile, Tool interface, Cobra skeleton, end-to-end headless slice |
| 1 — Headless engine | ✅ Done | Full `--print` loop: context → stream → tool loop → result |
| 2 — Core tools | ✅ Done | Bash, Read/Write/Edit, Grep/Glob, WebFetch/WebSearch |
| 3 — Permission engine | ✅ Done | allow/ask/deny + modes, plan mode |
| 4 — TUI | ✅ Done | Bubble Tea REPL, streaming display, permission dialogs |
| 5 — Services | ✅ Done | MCP, config, persistent memory, sessions |
| 6 — Experimental | ✅ Done | Sub-agents, skills, hooks, slash commands |
