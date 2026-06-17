# AGENTS.md — Ground Rules for the Claude Code Go Port

This file governs **every agent and human** that touches code in `cc-go/`. It is
not advisory. Where it says "must", a violation blocks merge. The whole point of
this document is to make a swarm of parallel agents produce **one coherent
codebase** instead of N divergent ones.

Read this in full before writing a single line. When this document and your
instinct disagree, this document wins. When this document is silent, match the
surrounding code and ask the coordinator — do not invent a new pattern.

---

## 0. Prime directives (non-negotiable)

1. **The build is always green.** `make check` must pass before any commit. Never
   commit or merge red. "I'll fix it later" is forbidden — later never comes in a
   swarm, and a red build blocks everyone.
2. **Port behavior, not syntax.** The TypeScript source (`../src`) is the
   specification. You are translating *what it does*, not transliterating *how it
   reads*. Idiomatic Go that preserves behavior beats a literal port that fights
   the language.
3. **If it isn't verified, it isn't done.** Code that compiles is not done. Code
   that passes its oracle/unit tests is done (see §6).
4. **Stay in your lane.** Touch only the module(s) the coordinator assigned you.
   The foundation (§4, §5) is frozen; you may not edit it — propose changes to the
   coordinator instead.
5. **Read the source before porting.** When unsure how the TS behaves, open the
   TS file and read it. Cite it (`../src/path/file.ts:line`) in your Go doc comment
   or PR. Never guess at behavior you can look up.

---

## 1. Repository & module layout

- **Module path:** `github.com/nevaero/cc-go`
- **Binary name:** `cc`
- **Go version:** 1.23+ (uses `log/slog`, range-over-func, modern stdlib). Pinned in `go.mod`.
- **Layout** (standard Go; `internal/` prevents accidental external coupling):

```
cc-go/
├── go.mod
├── Makefile                # the single source of truth for build/test/lint
├── AGENTS.md               # this file
├── cmd/cc/                 # main(); Cobra wiring only — no business logic
├── internal/
│   ├── engine/             # the agent loop  (TS: QueryEngine.ts, query.ts, query/)
│   ├── tool/               # Tool interface + registry  (TS: Tool.ts, tools.ts)
│   ├── tools/              # one package per tool  (TS: tools/<Name>/)
│   │   ├── bash/
│   │   ├── edit/
│   │   └── ...
│   ├── perm/               # permission engine  (TS: hooks/toolPermission, types/permissions.ts)
│   ├── apiclient/          # Anthropic SDK wrapper  (TS: services/api/)
│   ├── mcp/                # MCP client  (TS: services/mcp/)
│   ├── config/             # settings hierarchy + state  (TS: state/, utils/settings/)
│   ├── tui/                # Bubble Tea UI  (TS: screens/, components/, ink/)
│   ├── context/            # context assembly  (TS: context.ts)
│   └── ...
├── pkg/                    # ONLY for genuinely reusable, stable public API (rare; default to internal/)
└── testdata/               # golden files, VCR cassettes
```

- **Dependency direction (layering) — must not be violated:**
  `cmd → tui → engine → {tool, perm, apiclient, mcp, context, config}`.
  Lower layers must **never** import `tui` or `engine`. `tool` packages must not
  import each other. A package cycle is an automatic merge block (`go vet`/build
  will catch most; the coordinator catches the rest).

---

## 2. Naming conventions

- **Go-idiomatic, always.** `gofmt` + `goimports` decide formatting; there is no
  debate. Exported identifiers `PascalCase`, unexported `camelCase`.
- **No stutter.** Package `tool` exports `Tool`, not `tool.ToolType`. You call it
  `tool.Tool`. Package `bash` exports `New`, used as `bash.New()`.
- **Acronyms keep case:** `ID`, `URL`, `API`, `MCP`, `JSON`, `HTTP`, `LSP`,
  `OAuth`. So: `userID`, `apiClient`, `MCPServer`, `toJSON`, `parseURL`. Never
  `Id`, `Url`, `Api`, `Mcp`.
- **Package names:** short, lower-case, no underscores, no plurals unless the
  package is genuinely a collection (`tools/` dir holds packages, but each package
  is singular: `bash`, `edit`). No `util`/`common`/`helpers` grab-bag packages —
  name by responsibility.
- **TS → Go name mapping:** drop the `Tool`/`Service` suffix that becomes the
  package name. `FileEditTool` → package `edit`, constructor `edit.New`, type
  `edit.Tool`. `MCPConnectionManager` → `mcp.ConnectionManager`. Record the
  mapping in the package doc comment with the TS source path.
- **Files:** one cohesive concern per file, `snake_case.go`. Tests are
  `*_test.go` beside the code. No `manager.go`/`utils.go` dumping grounds.
- **Errors:** sentinels are `ErrXxx`; error types are `XxxError`.

---

## 3. Coding practices

- **Formatting/linting are mandatory and automated.** `gofmt -s`, `goimports`,
  and `golangci-lint` (config in repo: `errcheck`, `govet`, `staticcheck`,
  `ineffassign`, `unused`, `gocritic`, `revive`, `bodyclose`, `contextcheck`,
  `errorlint`). Lint failures block merge. Do not add `//nolint` without a reason
  comment and coordinator sign-off.
- **Errors:**
  - Return errors; do not `panic` in library code. `panic` is only for truly
    unreachable invariants, and never crosses a package boundary.
  - Wrap with context: `fmt.Errorf("reading config %s: %w", path, err)`. Always
    `%w` when the caller might inspect; never swallow an error silently.
  - Define sentinels (`var ErrNotFound = errors.New(...)`) for conditions callers
    branch on; check with `errors.Is`/`errors.As`.
- **Context:** every function that does I/O, spawns work, or can block takes
  `ctx context.Context` as the **first** parameter. Propagate it. Honor
  cancellation (the user hitting Ctrl-C must stop tool execution). Never store a
  `context.Context` in a struct.
- **Concurrency:**
  - Use `golang.org/x/sync/errgroup` for fan-out; channels for streaming.
  - **No shared mutable global state.** The TS `AppState` singleton becomes
    explicit dependencies passed in. If two goroutines touch a value, it is either
    immutable, channel-owned, or mutex-guarded — and the guard is documented.
  - The streaming tool executor (TS `StreamingToolExecutor`) is the canonical
    pattern: read-only/concurrency-safe tools run in parallel, mutating tools run
    serially, every call is permission-gated. Reuse it; do not reinvent.
  - Run `go test -race` (it's in `make check`); a data race is a merge block.
- **No globals for config/permissions/clients.** Inject them. A function's
  dependencies must be visible in its signature or its receiver.
- **Logging:** `log/slog` only, structured, no `fmt.Println` debugging left in.
  Levels: `Debug` (dev detail), `Info` (lifecycle), `Warn`, `Error`. The TUI owns
  the screen — library code logs, it does not print.
- **Dependencies:** the approved set is fixed (§ end). Adding any new third-party
  module requires coordinator approval **before** you import it. Prefer stdlib.
- **Comments:** doc comment on every exported symbol, starting with its name.
  Match the comment density of the package. Explain *why*, not *what*. Reference
  the TS source for non-obvious ported behavior.

---

## 4. Architecture decisions (LOCKED — do not relitigate)

These are settled. If you think one is wrong, raise it with the coordinator; do
not work around it locally.

1. **TUI = Bubble Tea (Elm architecture).** `Lipgloss` for styling, `Bubbles` for
   primitives, `stickers` for flexbox layout. The image-rendering decision
   (Bubble Tea vs vaxis) is the coordinator's to make in Phase 0; until then, no
   inline-image code.
2. **Engine is UI-agnostic and headless-first.** `internal/engine` knows nothing
   about Bubble Tea. It exposes an API and emits **events on a channel**
   (`<-chan engine.Event`). The TUI is one consumer; `--print` mode is another.
   Business logic in `tui` is a bug.
3. **Elm discipline in the TUI:** `Update(msg) (Model, Cmd)` is pure and fast; all
   I/O and blocking work happens in a `tea.Cmd` that returns a `tea.Msg`. `View()`
   is pure rendering — no side effects, no I/O, no business logic. React hooks
   become model fields + `Cmd`s; there is no per-component state.
4. **Tool input schemas are generated from Go structs** via
   `invopop/jsonschema` (struct tags). Validation is **explicit code in
   `Execute`**, not a reflection validator — the behavior must be readable.
5. **Feature gating** replaces TS `feature()`/`USER_TYPE` with **Go build tags**
   (compile-time dead-code elimination) plus runtime config for server-flippable
   gates. One tag per experimental subsystem; document it in the package.
6. **Permissions and config are values, not globals** (see §3). The permission
   decision (`allow`/`ask`/`deny`) and mode (`default`/`plan`/`acceptEdits`/
   `bypassPermissions`) live in `internal/perm` and are passed to the engine.
7. **The Anthropic SDK is `anthropic-sdk-go` (official).** All API access goes
   through `internal/apiclient`; no package calls the SDK directly.

---

## 5. The Tool contract (the most-shared interface — get it exactly right)

Every tool implements this single interface in `internal/tool`. This is frozen;
the coordinator owns it.

```go
package tool

// Tool is the contract every agent-invokable action implements.
// TS origin: ../src/Tool.ts
type Tool interface {
    // Name is the stable identifier the model calls (e.g. "Bash", "Edit").
    Name() string

    // Description returns the prompt text shown to the model. TS: prompt.ts
    Description(ctx context.Context) string

    // InputSchema returns the JSON schema generated from the input struct.
    InputSchema() *jsonschema.Schema

    // Execute runs the tool. It MUST honor ctx cancellation, MUST validate
    // input explicitly before acting, and MUST NOT print to the screen.
    Execute(ctx context.Context, raw json.RawMessage, deps Deps) (Result, error)

    // ReadOnly reports whether the tool mutates state. Default to false
    // (fail-closed) if unsure. Drives parallel execution + permission UX.
    ReadOnly() bool

    // ConcurrencySafe reports whether this tool may run in parallel with
    // others. Default false. Read-only tools are usually safe.
    ConcurrencySafe() bool

    // CheckPermission returns allow/ask/deny BEFORE Execute. TS: checkPermissions.
    CheckPermission(ctx context.Context, raw json.RawMessage, mode perm.Mode) perm.Decision
}
```

Rules for adding a tool (this is the swarm's hot path):

- One package under `internal/tools/<name>/`. Mirror the TS directory's split:
  execution, prompt, validation, permission — separate files, same as
  `../src/tools/<Name>/`.
- Define a typed input struct with `json` + `jsonschema` tags. Validate it
  explicitly at the top of `Execute`; return a descriptive error on bad input.
- Register via the registry's `Register(New())` in an `init()` or an explicit
  registration list owned by the coordinator (no magic global mutation across
  packages — follow the existing registration file's pattern).
- Fail-closed: `ReadOnly`/`ConcurrencySafe` default to `false`. Only set `true`
  when you have read the TS and confirmed it.
- Rendering (how a tool's call/result shows in the TUI) lives in `internal/tui`,
  not in the tool package — the tool returns structured `Result`, the TUI renders.

---

## 6. Build / test / review loop

**One command gates everything:** `make check` must run and pass before you
commit. It runs, in order:

```
make check  →  gofmt -l (must be empty)
               goimports -l (must be empty)
               go build ./...
               go vet ./...
               golangci-lint run
               go test -race ./...
```

A commit that breaks any step is reverted, no discussion.

**Testing requirements:**

- **Unit tests** for all logic with branches (validation, permission decisions,
  parsing, layout math). Table-driven. stdlib `testing` + `google/go-cmp` for
  comparisons. **`testify` is banned** — one assertion style across the swarm.
- **Oracle/golden tests for ports** — this is how we prove behavioral parity
  without trusting the LLM:
  - The TS `vcr.ts` fixture mechanism is the oracle. Capture TS request/response
    and tool-execution fixtures into `testdata/cassettes/`.
  - The Go port replays the same inputs and must reproduce the recorded output
    (golden file in `testdata/golden/`). Diff via `go-cmp`; update goldens only
    with a deliberate `-update` flag and a justification in the PR.
  - No port of engine/tool behavior is "done" without an oracle test.
- **TUI tests:** use `teatest` (Bubble Tea's harness) for golden-frame snapshots
  of key screens; logic must live in the model and be unit-testable without a
  terminal.
- Tests must be deterministic and hermetic — no network, no clock dependence
  (inject time), no ordering assumptions on maps.

**Review loop (coordinator-gated):**

- Work in a **dedicated git worktree/branch per agent**; keep changes **small and
  single-purpose** (one tool, one service, one screen). Giant PRs are rejected.
- The coordinator reviews against this checklist and blocks on any miss:
  builds + `make check` green · stays in assigned lane · Tool contract unchanged ·
  oracle/unit tests present and passing · TS source cited for ported behavior ·
  no new deps without prior approval · no globals/cycles · errors wrapped · ctx
  honored.
- CI runs `make check` on every push; red CI = not mergeable, full stop.

**Commits:**

- Imperative, scoped subject: `engine: stream tool_use blocks as they arrive`.
- Reference the TS source in the body when porting.
- Small, frequent, each independently green.

---

## 7. Porting methodology (TS → Go)

- **Read the TS first, cite it, then port the behavior.** Put
  `// Port of ../src/tools/BashTool/bashSecurity.ts` at the top of the file.
- **Faithful by default; deviate only deliberately.** If Go idiom forces a
  behavioral change, call it out explicitly in a comment and the PR ("TS retried
  inline; Go uses errgroup with the same 4-attempt backoff"). Silent behavior
  drift is the worst failure mode.
- **Async → concurrency:** `async/await` becomes synchronous Go with `ctx`;
  `Promise.all` becomes `errgroup`; event streams become channels.
- **Zod → struct + explicit validation + generated schema** (§4.4).
- **React hooks → model fields + `tea.Cmd`** (§4.3). Do not try to recreate hooks.
- **Do NOT port (skip list — confirm with coordinator before porting anything here):**
  analytics/telemetry internals, `vcr`/mock test infra (we reuse the *fixtures*,
  not the code), the CCR cloud/`bridge`/`remote`/`server`/`upstreamproxy` infra
  (unless cloud sessions are explicitly in scope), JS plugins, the auto-updater,
  and any `USER_TYPE === 'ant'` internal-only path that isn't on the keep-list.

---

## 8. Swarm coordination protocol

- **Ownership:** the coordinator assigns each agent a module. You edit only that
  module's packages. Need a change elsewhere? File it with the coordinator.
- **The foundation is frozen.** `internal/tool` (the `Tool`/`Deps`/`Result`
  types), `internal/perm` decision types, `internal/engine` event types, the
  `Makefile`, and this file change **only through the coordinator**. These are the
  contracts the whole swarm builds against; uncoordinated edits break everyone.
- **Green-build gate before fan-out:** no module work begins until the Phase-0
  foundation + one end-to-end vertical slice compiles, runs, and passes
  `make check`. (See the migration plan.)
- **Integration cadence:** rebase on the foundation frequently; run `make check`
  before every push; the coordinator merges continuously and keeps `main` green.
- **Status:** each agent reports module, current unit, and blockers. If blocked on
  a frozen contract, stop and escalate — do not fork the contract locally.
- **Conflicts:** if two units need the same change to a shared file, the
  coordinator serializes it. Agents do not hand-merge shared contracts.

---

## 9. Definition of Done (per unit)

A unit (tool/service/screen) is done when **all** are true:

- [ ] Behavior matches the cited TS source (or deviations are documented).
- [ ] `make check` passes locally (build, vet, lint, race tests).
- [ ] Unit tests cover the branching logic; an oracle/golden test proves parity
      for ported behavior.
- [ ] Exported symbols have doc comments; TS source path referenced.
- [ ] Stays within assigned module; no edits to frozen contracts; no import cycles.
- [ ] No new dependencies beyond the approved set without coordinator sign-off.
- [ ] Errors wrapped, `ctx` honored, no globals, no leftover debug printing.

---

## Approved dependencies (additions require coordinator approval)

- CLI: `github.com/spf13/cobra`
- TUI: `github.com/charmbracelet/bubbletea`, `.../lipgloss`, `.../bubbles`,
  `github.com/76creates/stickers`; TUI tests `.../x/exp/teatest`
- LLM: `github.com/anthropics/anthropic-sdk-go`
- MCP: `github.com/modelcontextprotocol/go-sdk`
- Schema: `github.com/invopop/jsonschema`
- Concurrency: `golang.org/x/sync/errgroup`
- Test compare: `github.com/google/go-cmp`
- Stdlib for everything else (`log/slog`, `net/http`, `os/exec`, `context`, ...).

Everything not on this list is **not approved**. Ask first.
