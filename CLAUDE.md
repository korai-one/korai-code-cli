# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

This is a **read-only source snapshot** of Claude Code (Anthropic's terminal CLI), mirrored for educational and defensive security research. See `README.md` for the provenance details.

Practical consequences for working here:

- There is **no build tooling, no `package.json`, no `tsconfig.json`, no lockfile, and no tests** in this tree. It is a `src/`-only extract (~1,900 files, 500K+ lines of TypeScript). Do not attempt to run `bun install`, `npm test`, build, or lint — those commands will not work because the supporting config was never part of the snapshot.
- The intended runtime is **Bun**; the UI is **React + Ink** (terminal renderer). Code uses Bun-specific APIs (e.g. `bun:bundle`).
- Treat changes as analysis/annotation rather than something that compiles and runs end-to-end. Verification is by reading and cross-referencing files, not by executing.

## Conventions that will trip you up

- **ESM import paths use `.js` extensions even though the source files are `.ts`/`.tsx`** (e.g. `import { BashTool } from './tools/BashTool/BashTool.js'` resolves to `BashTool.tsx`). Preserve this when editing imports.
- **Biome** is the formatter/linter (88+ files carry `biome-ignore` directives). Respect existing `biome-ignore` comments — some, like `organizeImports`, exist specifically because import ordering is load-bearing.
- **`ANT-ONLY` markers** (56+ files) delimit Anthropic-internal code. Comments like `// ANT-ONLY import markers must not be reordered` mean exactly that — do not reorder or strip those regions.
- **Conditional / dead-code-eliminated imports.** Two gating mechanisms appear throughout the registries:
  - `feature('FLAG')` from `bun:bundle` — stripped at build time when the flag is off. Notable flags: `PROACTIVE`, `KAIROS`, `BRIDGE_MODE`, `DAEMON`, `VOICE_MODE`, `AGENT_TRIGGERS`, `MONITOR_TOOL`.
  - `process.env.USER_TYPE === 'ant'` (145+ files) — gates internal-only tools/commands via `require()` so they are absent for external users.
  - When adding or tracing a tool/command, check whether it is gated; a symbol may only exist in some build configurations.

## Architecture (the big picture)

The system is a tool-calling agent loop with a terminal UI. The three central registries tie everything together:

- **`src/tools.ts`** — the tool registry. Assembles the list of agent-invocable tools, applying the `feature()` / `USER_TYPE` gating described above. `src/Tool.ts` (~29K lines) defines the base `Tool` type: input schema (Zod), permission model, progress/result types, and `toolMatchesName`.
- **`src/commands.ts`** (~25K lines) — the slash-command registry, loaded conditionally per environment.
- **`src/QueryEngine.ts`** (~46K lines) and **`src/query.ts`** (~68K lines) — the LLM query pipeline: streaming responses, the tool-call loop, thinking mode, retries, token counting.
- **`src/main.tsx`** (~800K lines, the bundled entrypoint) — Commander.js CLI parsing + React/Ink renderer init. Startup deliberately overlaps MDM settings read, keychain prefetch, and GrowthBook init as parallel side-effects before heavy module evaluation. Heavy subsystems (OpenTelemetry, gRPC, analytics, feature-gated code) are deferred via dynamic `import()`.

### Tools (`src/tools/<ToolName>/`)

Each tool is a self-contained directory, not a single file. The `BashTool/` layout is the template to follow:

- `BashTool.tsx` — the tool definition (schema, permissions, execution).
- `prompt.ts` — the tool's description/prompt text shown to the model.
- `UI.tsx` / `*ResultMessage.tsx` — Ink components rendering invocation and results.
- `*Permissions.ts`, `*Security.ts`, `*Validation.ts` — permission checks, safety/sandbox decisions, and input validation are split into dedicated co-located modules.

When modifying a tool, the logic, its prompt, its UI, and its permission/validation rules live side by side in that directory — change them together.

### Commands (`src/commands/`)

User-facing `/`-prefixed commands. Mixed shape: simple commands are single files (`commit.ts`, `diff`, `cost`); richer ones are directories. `createMovedToPluginCommand.ts` indicates some former built-ins have migrated to the plugin system.

### Services (`src/services/`)

External integrations and cross-cutting subsystems: `api/` (Anthropic API client, file API, bootstrap), `mcp/` (Model Context Protocol servers), `oauth/`, `lsp/` (Language Server Protocol), `analytics/` (GrowthBook flags), `plugins/`, `compact/` (context compression), `extractMemories/` + `SessionMemory/` + `teamMemorySync/` (persistent/team memory), `policyLimits/` & `remoteManagedSettings/` (org policy).

### Other notable subsystems

- `coordinator/` — multi-agent orchestration; sub-agents are spawned via `AgentTool`, teams via `TeamCreateTool`.
- `bridge/` — bidirectional IDE (VS Code / JetBrains) ↔ CLI link; JWT-authenticated session runner.
- `skills/` + `SkillTool` — reusable workflows; `plugins/` — built-in and third-party plugin loading.
- `hooks/toolPermission/` — runs on every tool invocation, resolving against the active permission mode (`default`, `plan`, `bypassPermissions`, etc.) or prompting the user.
- `schemas/` — Zod config schemas; `migrations/` — config migrations; `state/` — state management.

## Editing guidance specific to this tree

- Because nothing builds here, prefer **minimal, localized edits** and rely on cross-file reading to confirm correctness (follow the `.js`-as-`.ts` import graph by hand).
- When you reference behavior, cite it as `src/path/file.ts:line` so it stays verifiable in the absence of a runnable build.
