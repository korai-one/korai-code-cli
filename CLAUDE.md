# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

This is a **read-only source snapshot** of Claude Code (Anthropic's terminal CLI), mirrored for educational and defensive security research. See `README.md` for provenance.

Practical consequences:

- There is **no build tooling, no `package.json`, no `tsconfig.json`, no lockfile, and no tests** in this tree. It is a `src/`-only extract (~1,900 files, 500K+ lines of TypeScript). Do not try to `bun install`, build, lint, or run tests — the supporting config was never part of the snapshot.
- Intended runtime is **Bun**; UI is **React + Ink** (custom terminal renderer). Code uses Bun-specific APIs (`bun:bundle`).
- Verification is by reading and cross-referencing files, not by executing. Cite findings as `src/path/file.ts:line`.

## Conventions that will trip you up

- **ESM import paths use `.js` extensions even though source files are `.ts`/`.tsx`** (e.g. `import { BashTool } from './tools/BashTool/BashTool.js'` → `BashTool.tsx`). Preserve this when editing imports.
- **Biome** is the formatter/linter (88+ `biome-ignore` directives). Some, like `organizeImports`, are load-bearing — import ordering matters in the registries.
- **`ANT-ONLY` markers** (56+ files) delimit Anthropic-internal regions. Comments like "must not be reordered" mean exactly that.
- **Two code-gating mechanisms** appear everywhere; a symbol may only exist in some build configs:
  - `feature('FLAG')` from `bun:bundle` — stripped at build time. Notable: `PROACTIVE`, `KAIROS`, `BRIDGE_MODE`, `DAEMON`, `VOICE_MODE`, `AGENT_TRIGGERS`, `COORDINATOR_MODE`, `MONITOR_TOOL`, `TRANSCRIPT_CLASSIFIER`.
  - `process.env.USER_TYPE === 'ant'` (145+ files) — gates internal-only tools/commands via `require()`, absent for external users.

## The core loop (start here)

A conversation turn flows through three central files plus the tool registry:

1. **`QueryEngine.ts`** (`submitMessage`, ~line 209) owns the **conversation/session lifecycle** — one engine per conversation, each call is a turn. It processes slash commands (`processUserInput`), drives the LLM loop via `query()`, accumulates usage/cost, tracks permission denials and file state, and emits `compact_boundary` messages to release memory in long sessions.
2. **`query.ts`** (`query` → `queryLoop`, ~line 219/241) is the **per-turn state machine**: preflight (snapshot `QueryConfig`, prefetch memories, build system prompt, apply token budgets, run snip/microcompact/autocompact) → API call (streaming events from `deps.callModel`) → tool execution → loop or finalize. Retries handle `FallbackTriggeredError` and max-output-token recovery. Stop hooks run post-response.
3. **`context.ts`** supplies memoized **system context** (git status, cache breaker) and **user context** (CLAUDE.md discovery, date) prepended to conversations.
4. **`query/`** holds the loop's helpers: `config.ts` (snapshot feature gates once per query), `tokenBudget.ts` (auto-continue vs. stop decision), `deps.ts` (dependency injection for `callModel`/compaction), `stopHooks.ts` (post-response: memory extraction, classifiers).

Tool execution has two paths: a **`StreamingToolExecutor`** (ingests `tool_use` blocks as they stream; read-only/concurrency-safe tools run in parallel, others serial) and a fallback **`runTools`** orchestrator. Both gate each call through `canUseTool`. Supporting cost/history: `cost-tracker.ts` (per-model USD aggregation, restored on `/resume`), `costHook.ts` (flush on exit), `history.ts` (prompt history in `~/.claude/history.jsonl`).

## Tool system (`Tool.ts`, `tools.ts`, `tools/`)

- **`Tool.ts`** defines the base shape: Zod `inputSchema`/`outputSchema`, two-phase permission (`validateInput` → `checkPermissions` returning `allow`/`deny`/`ask`), `call()` execution, and a family of `render*` methods for Ink UI (use, progress, result, error, rejected). `buildTool()` merges a partial `ToolDef` with **fail-closed `TOOL_DEFAULTS`** (`isConcurrencySafe`/`isReadOnly` default false). `maxResultSizeChars` spills oversized results to disk with a preview to the model.
- **`tools.ts`** is the registry (`getAllBaseTools`), applying `feature()`/`USER_TYPE` gating and `TOOL_PRESETS`. `constants/tools.ts` holds disallowed-tool sets per agent type/mode.
- **Per-tool directory convention:** each tool is a directory, not a file. Logic (`ToolName.tsx`), prompt text (`prompt.ts`), Ink UI (`UI.tsx`, `*ResultMessage.tsx`), and permission/security/validation modules live side by side — **change them together.** `BashTool/` is the richest example (`bashPermissions.ts`, `bashSecurity.ts`, `readOnlyValidation.ts`, sandbox decisions). `AgentTool/` (`runAgent.ts`, `forkSubagent.ts`, `agentMemory.ts`) and `MCPTool/` (template overridden at runtime with live MCP info) are the other key ones. Shared helpers: `tools/shared/` (`spawnMultiAgent.ts`, `gitOperationTracking.ts`) and `tools/utils.ts` (tool-use ID message plumbing).

## CLI & commands (`main.tsx`, `commands.ts`, `commands/`, `entrypoints/`)

- **`entrypoints/cli.tsx`** is the real entry: fast-paths `--version`, `--dump-system-prompt`, and daemon/MCP worker modes with zero heavy imports, otherwise dynamically imports `main.tsx`.
- **`main.tsx`** (the huge bundled file) does Commander.js parsing + Ink render init. **Startup is aggressively parallelized:** MDM read and keychain prefetch fire as import-time side-effects; commands and agent definitions load in parallel before the trust dialog; `startDeferredPrefetches()` fires cloud-cred/MCP/cache prefetches after first render. `entrypoints/init.ts` defers OpenTelemetry (~400KB) and gRPC (~700KB) until needed. `setup.ts` does the async bootstrap (Node check, git root, worktree, hooks snapshot, file watcher); `bootstrap/state.ts` is the `AppState` singleton.
- **`commands.ts`** registers ~150 commands (single-file like `commit.ts`, or directories like `config/`). `getCommands(cwd)` is memoized and merges builtins + skills + workflows + plugin + MCP commands, filtered by availability. Three command kinds: `prompt` (LLM-expanded skills), `local-jsx` (Ink UI), `local` (sync/async). Remote/bridge sessions filter commands via `isBridgeSafeCommand` / `filterCommandsForRemoteMode`. `createMovedToPluginCommand.ts` redirects deprecated builtins.

## Terminal UI (`ink/`, `components/`, `screens/`, `vim/`, `keybindings/`)

- **`ink/`** is a **fully custom Ink renderer** (react-reconciler + Yoga layout, alt-screen mode, frame diffing, mouse/selection/hyperlinks). `ink/components/` provides `Box`/`Text`/`ScrollBox`/etc.; `ink/hooks/` handles raw input, selection, cursor.
- **`screens/REPL.tsx`** is the monolithic (~5K line) interactive loop wiring message list + prompt input + permission requests + tasks + voice via 100+ hooks. Other screens: `Doctor`, `ResumeConversation`.
- **`components/`** groups: `messages/` (transcript rendering), `design-system/` (themed primitives), `PromptInput/` (editor with vim mode, paste, image), `permissions/` (tool/file/bash permission prompts), and feature UIs (`mcp/`, `skills/`, `agents/`). Dialogs are launched as promise-returning factories via `dialogLaunchers.tsx` / `interactiveHelpers.tsx`.
- **`vim/`** is a pure state machine (NORMAL/INSERT, operators+motions+find, dot-repeat). **`keybindings/`** loads default + `~/.claude/keybindings.json` with hot-reload, chord support (1s timeout), and innermost-context-wins resolution. **`outputStyles/`** loads `.claude/output-styles/*.md` style prompts. **`buddy/`** is an animated companion sprite.

## Services (`services/`)

External systems and cross-cutting concerns. Recurring patterns: transparent **auth layering** (API client auto-refreshes OAuth/AWS/GCP), **fail-open** remote config, **ETag caching**, **forked sub-agents** for background work, and **PII guards**.

- **`api/`** — Anthropic client factory (direct API / Bedrock / Azure), `withRetry` backoff, utilization/rate-limit fetch.
- **`mcp/`** — MCP server lifecycle (`MCPConnectionManager`) over stdio/HTTP/SSE, OAuth-proxied fetch, maps MCP tools to the `Tool` interface.
- **`oauth/`** — OAuth 2.0 + PKCE flow for Claude.ai (browser + manual paste).
- **`lsp/`** — per-language LSP servers, diagnostic aggregation.
- **`analytics/`** — event logging (Datadog + 1P BigQuery) and GrowthBook feature flags; `stripProtoFields` PII guard.
- **`compact/`** — conversation compaction (summarize old turns) before exceeding token budget; file reinjection after.
- **Memory:** `SessionMemory/` (background per-turn notes), `extractMemories/` (durable project memory via stop hook), `teamMemorySync/` (git-scoped shared memory with secret scanning), `settingsSync/`.
- **Enterprise:** `policyLimits/`, `remoteManagedSettings/` (fail-open, security-checked).
- **`plugins/`** — `PluginInstallationManager` background-installs trusted plugins. Loose files: `claudeAiLimits.ts` (subscriber quotas), `notifier.ts`, `voice.ts`, `vcr.ts` (API fixture record/replay for tests).

## Agent orchestration (`coordinator/`, `tasks/`, `Task.ts`, `skills/`, `plugins/`)

- **`coordinator/coordinatorMode.ts`** (feature-gated `COORDINATOR_MODE`): a coordinator agent spawns workers via `AgentTool`, continues them with `SendMessage`, stops them with `TaskStop`; internal tools (TeamCreate/Delete, SyntheticOutput) are excluded from the worker pool.
- **`Task.ts` / `tasks/`** — 7 task types (`local_bash`, `local_agent`, `remote_agent`, `in_process_teammate`, `local_workflow`, `monitor_mcp`, `dream`). Tasks register in `AppState`; `LocalAgentTask` spawns backgrounded async workers tracked by a `ProgressTracker`, and on completion enqueues a `<task-notification>` (atomic `notified` flag prevents duplicates). `RemoteAgentTask` polls CCR sessions.
- **`skills/`** — discovery tiers: bundled → project `.claude/skills/` → user → plugin → MCP. `SkillTool` executes a skill in a **forked sub-agent** that inherits the parent's tool pool (cache-identical), output wrapped in `<command>` tags.
- **Plugins** (loader in `utils/plugins/pluginLoader.ts`) — marketplaces/git/npm sources; a plugin contributes `commands/`, `agents/`, `hooks/`, and skills. Background reconciliation diffs declared vs. installed without blocking startup.

## Remote / bridge / server (`bridge/`, `remote/`, `server/`, `upstreamproxy/`)

- **`bridge/`** (gated `BRIDGE_MODE` + GrowthBook) — IDE↔CLI link. Poll-based work queue (`bridgeMain.ts`); `sessionRunner.ts` spawns `claude --print` children with NDJSON I/O and forwards permission `control_request`s; JWT ingress tokens with proactive refresh (`jwtUtils.ts`). Spawn modes: single-session / worktree / same-dir.
- **`remote/`** — CCR remote sessions over WebSocket (`RemoteSessionManager`, `SessionsWebSocket`); `sdkMessageAdapter.ts` converts SDK messages to REPL messages.
- **`server/`** — local direct-connect server for native clients; sessions persist to `~/.claude/server-sessions.json` for resume.
- **`upstreamproxy/`** — container-side CCR MITM relay: reads session token, blocks ptrace (`PR_SET_DUMPABLE`), tunnels HTTP CONNECT over WebSocket, injects proxy/CA env vars.
- **`native-ts/`** — pure-TS fallbacks for native modules (fuzzy file index, color diff, Yoga enums). **`voice/`** — voice-mode gating (OAuth-only `/voice_stream`).

## State, config, permissions (`state/`, `schemas/`, `migrations/`, `memdir/`, `hooks/`)

- **`state/`** — single immutable `AppState` (`DeepImmutable`) in a small store (`getState`/`setState`/`subscribe`) exposed via React context; `selectors.ts` derives view state. `bootstrap/state.ts` holds the singleton.
- **Settings** (`utils/settings/`) resolve a hierarchy: flag > project > local > user > defaults (+ policy). **`schemas/`** are the Zod schemas (including `HooksSchema`). **`migrations/`** are versioned config upgrades (model bumps, flag resets).
- **`memdir/`** — persistent memory rooted at `MEMORY.md` (200-line / 25KB cap with smart truncation), 4 memory types, gated by `isAutoMemoryEnabled()`.
- **Two distinct "hooks":**
  - **`hooks/toolPermission/`** + `types/permissions.ts` — the permission decision system: modes (`default`, `plan`, `acceptEdits`, `bypassPermissions`, `dontAsk`, internal `auto`/`bubble`), behaviors (`allow`/`deny`/`ask`), and rules sourced from settings/CLI/session.
  - **`hooks/` (React hooks)** — 80+ UI hooks (`useCanUseTool`, `useTextInput`, `useReplBridge`, `useVoice`, etc.). Separately, **user-configurable hooks** (the `HooksSchema` `command`/`prompt`/`agent`/`http` kinds firing on `PreToolUse`/`PostToolUse`/`SessionStart`/etc.) are config, not code.
  - **`moreright/useMoreRight.tsx`** is an external-build stub for internal turn-lifecycle hooks (`onBeforeQuery`/`onTurnComplete`).

## Editing guidance specific to this tree

- Nothing builds here — prefer **minimal, localized edits** and confirm correctness by reading the `.js`-as-`.ts` import graph by hand.
- When you touch a tool, the logic, prompt, UI, and permission/validation modules all live in its directory; keep them consistent.
- Always cite behavior as `src/path/file.ts:line` so claims stay verifiable without a runnable build.
