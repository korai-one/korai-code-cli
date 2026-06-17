# Korai Code CLI

A terminal-based AI coding agent built on the **Korai SDK** — a decentralized, peer-to-peer encrypted inference network. Run a full agentic coding loop from your terminal without routing prompts through any centralized API.

---

## What it is

`korai` is a coding agent CLI written in Go. It drives an LLM tool-calling loop that can read and edit files, run shell commands, search codebases, and coordinate multi-agent tasks — all from an interactive terminal UI or in headless `--print` mode.

Unlike cloud-only coding assistants, inference runs over the **Korai P2P network**: requests are encrypted end-to-end, routed across decentralized nodes, and never pass through a single-operator bottleneck.

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| CLI parsing | [Cobra](https://github.com/spf13/cobra) |
| TUI | [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm architecture) |
| Styling / layout | [Lipgloss](https://github.com/charmbracelet/lipgloss) + [stickers](https://github.com/76creates/stickers) |
| TUI components | [Bubbles](https://github.com/charmbracelet/bubbles) |
| Inference network | Korai SDK (decentralized P2P encrypted inference) |
| MCP | [go-sdk](https://github.com/modelcontextprotocol/go-sdk) (official) |
| Tool schemas | Go structs → [invopop/jsonschema](https://github.com/invopop/jsonschema) |
| Concurrency | `golang.org/x/sync/errgroup` + channels |
| Logging | `log/slog` |

---

## Architecture

A conversation turn flows through layered packages. Dependency arrows point downward; lower layers never import upper ones.

```
cmd/korai  →  tui  →  engine  →  { tool, perm, apiclient, mcp, context, config }
```

- **`engine`** — the agent loop. Assembles context → streams inference → executes tools (permission-gated) → feeds results back → repeats until no tool calls remain. Headless-first: emits events on a `<-chan engine.Event`. No UI code here.
- **`tool`** — the `Tool` interface + registry. Every agent-invokable action: name, description, JSON schema, validation, `Execute`, and `ReadOnly`/`ConcurrencySafe`/`CheckPermission` declarations (fail-closed by default).
- **`tools/*`** — one package per tool: `bash`, `read`, `write`, `edit`, `grep`, `glob`, `webfetch`, `websearch`.
- **`perm`** — allow / ask / deny decisions and modes (`default`, `plan`, `acceptEdits`, `bypassPermissions`).
- **`apiclient`** — the only package that calls the Korai SDK.
- **`mcp`** — MCP client; maps external MCP tools onto the `Tool` interface.
- **`context`** — assembles system + user context (working dir, git status, project instructions, date).
- **`config`** — settings hierarchy + app state as explicit injected values; no global singleton.
- **`tui`** — Bubble Tea REPL. Strict Elm discipline: `Update` is pure, all I/O in `tea.Cmd`s, `View` is pure rendering. Renders the transcript, streaming output, prompt input, and permission dialogs by consuming engine events.

---

## Build

```sh
# Prerequisites: Go 1.23+, golangci-lint
make check    # fmt, imports, build, vet, lint, race tests
make build    # produces ./bin/korai
```

---

## Usage

```sh
# Interactive TUI
korai

# Headless / scripting
korai --print "refactor the auth module to use middleware"

# With a specific model
korai --model <model-id> --print "explain this codebase"
```

---

## Project status

Active development. See `HANDOFF.md` for the build plan and `AGENTS.md` for contribution ground rules.

| Phase | Status | Description |
|---|---|---|
| 0 — Foundation | In progress | Module, Makefile, Tool interface, Cobra skeleton, end-to-end headless slice |
| 1 — Headless engine | Planned | Full `--print` loop: context → stream → tool loop → result |
| 2 — Core tools | Planned | Bash, Read/Write/Edit, Grep/Glob, WebFetch/WebSearch |
| 3 — Permission engine | Planned | allow/ask/deny + modes |
| 4 — TUI | Planned | Bubble Tea REPL, streaming display, permission dialogs |
| 5 — Services | Planned | MCP, OAuth, config, persistent memory, LSP |
| 6 — Experimental | Planned | Coordinator/sub-agents, skills, hooks, slash commands |
