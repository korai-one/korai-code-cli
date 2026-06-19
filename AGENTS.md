# AGENTS.md — Ground Rules for Korai Code CLI

This file governs **every agent and human** that touches code in `korai-code-cli/`. It is
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
2. **Build behavior, not syntax.** The architecture described in `HANDOFF.md` is
   the specification. You are implementing *what it does*, not transliterating any
   reference syntax. Idiomatic Go that implements the described behavior correctly
   beats anything that fights the language.
3. **If it isn't verified, it isn't done.** Code that compiles is not done. Code
   that passes its oracle/unit tests is done (see §6).
4. **Stay in your lane.** Touch only the module(s) the coordinator assigned you.
   The foundation (§4, §5) is frozen; you may not edit it — propose changes to the
   coordinator instead.
5. **Consult the architecture before implementing.** When unsure how a behavior
   should work, consult `HANDOFF.md` (the authoritative spec). Never guess at
   behavior you can look up there.

---

## 1. Repository & module layout

- **Module path:** `github.com/Nevaero/korai-code-cli`
- **Binary name:** `korai`
- **Go version:** 1.23+ (uses `log/slog`, range-over-func, modern stdlib). Pinned in `go.mod`.
- **Layout** (standard Go; `internal/` prevents accidental external coupling):

```
korai-code-cli/
├── go.mod
├── Makefile                # the single source of truth for build/test/lint
├── AGENTS.md               # this file
├── cmd/korai/              # main(); Cobra wiring only — no business logic
├── internal/
│   ├── engine/             # the agent loop (conversation/session lifecycle, LLM tool-calling loop)
│   ├── tool/               # Tool interface + registry
│   ├── tools/              # one package per tool
│   │   ├── bash/
│   │   ├── edit/
│   │   └── ...
│   ├── perm/               # permission engine (allow/ask/deny modes)
│   ├── apiclient/          # Anthropic SDK wrapper
│   ├── mcp/                # MCP client
│   ├── config/             # settings hierarchy + state
│   ├── tui/                # Bubble Tea UI
│   ├── context/            # context assembly (working dir, git status, project instructions, date)
│   └── ...
├── pkg/                    # ONLY for genuinely reusable, stable public API (rare; default to internal/)
└── testdata/               # golden files, test fixtures
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
- **Name mapping:** drop the `Tool`/`Service` suffix that becomes the
  package name. `FileEditTool` → package `edit`, constructor `edit.New`, type
  `edit.Tool`. `MCPConnectionManager` → `mcp.ConnectionManager`. Record the
  conceptual mapping in the package doc comment.
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
  - **No shared mutable global state.** All config, permissions, and clients are
    explicit dependencies passed in. If two goroutines touch a value, it is either
    immutable, channel-owned, or mutex-guarded — and the guard is documented.
  - The streaming tool executor is the canonical pattern: read-only/concurrency-safe
    tools run in parallel, mutating tools run serially, every call is
    permission-gated. Reuse it; do not reinvent.
  - Run `go test -race` (it's in `make check`); a data race is a merge block.
- **No globals for config/permissions/clients.** Inject them. A function's
  dependencies must be visible in its signature or its receiver.
- **Logging:** `log/slog` only, structured, no `fmt.Println` debugging left in.
  Levels: `Debug` (dev detail), `Info` (lifecycle), `Warn`, `Error`. The TUI owns
  the screen — library code logs, it does not print.
- **Dependencies:** the approved set is fixed (§ end). Adding any new third-party
  module requires coordinator approval **before** you import it. Prefer stdlib.
- **Comments:** doc comment on every exported symbol, starting with its name.
  Match the comment density of the package. Explain *why*, not *what*. Call out
  non-obvious design decisions with a brief justification.

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
8. **`// TODO KORAI SDK` annotation.** Every call site inside `internal/apiclient`
   that directly invokes an `anthropic-sdk-go` method (e.g. `client.Messages.New`,
   `stream.Next`, `stream.Event`) must have a `// TODO KORAI SDK` comment on the
   same line or the line immediately above. These mark the exact swap points when
   the Korai P2P inference SDK replaces the Anthropic SDK. Missing annotations are
   a merge block.
9. **`internal/apiclient` owns its own types — no SDK types cross the boundary.**
   `anthropic.*` types (messages, content blocks, tool use, events, etc.) must
   never appear in the signature of any symbol exported from `internal/apiclient`.
   Define equivalent types in `internal/apiclient` (e.g. `apiclient.Request`,
   `apiclient.Event`, `apiclient.ToolCall`) and convert at the boundary inside the
   package. This is the anti-corruption layer that lets the SDK be swapped without
   touching `engine`, `tool`, or any other package.
10. **`apiclient` exposes a `Client` interface, not a concrete struct.** The
    interface is the only thing `engine` depends on:
    ```go
    // Client is the inference boundary. engine calls this; nothing below it
    // knows which network backend is in use.
    type Client interface {
        Complete(ctx context.Context, req Request) (<-chan Event, error)
    }
    ```
    `AnthropicClient` implements `Client` today using `anthropic-sdk-go` (all call
    sites annotated `// TODO KORAI SDK`). `KoraiClient` will implement the same
    interface against the Korai P2P SDK. A `StranglerClient` can wrap both and
    route via a config flag during the transition. `engine` is wired to `Client` at
    construction time and never sees the concrete type.

---

## 5. The Tool contract (the most-shared interface — get it exactly right)

Every tool implements this single interface in `internal/tool`. This is frozen;
the coordinator owns it.

```go
package tool

// Tool is the contract every agent-invokable action implements.
type Tool interface {
    // Name is the stable identifier the model calls (e.g. "Bash", "Edit").
    Name() string

    // Description returns the prompt text shown to the model.
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

    // CheckPermission returns allow/ask/deny BEFORE Execute is called.
    CheckPermission(ctx context.Context, raw json.RawMessage, mode perm.Mode) perm.Decision
}
```

Rules for adding a tool (this is the swarm's hot path):

- One package under `internal/tools/<name>/`. Split by concern: execution,
  prompt, validation, permission — separate files within the package.
- Define a typed input struct with `json` + `jsonschema` tags. Validate it
  explicitly at the top of `Execute`; return a descriptive error on bad input.
- Register via the registry's `Register(New())` in an `init()` or an explicit
  registration list owned by the coordinator (no magic global mutation across
  packages — follow the existing registration file's pattern).
- Fail-closed: `ReadOnly`/`ConcurrencySafe` default to `false`. Only set `true`
  when you have explicitly verified the tool cannot mutate state.
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
- **Golden tests for engine/tool behavior** — this is how we prove correctness
  without trusting the LLM:
  - Capture hand-crafted or recorded request/response and tool-execution fixtures
    into `testdata/fixtures/`.
  - The implementation replays the same inputs and must reproduce the expected
    output (golden file in `testdata/golden/`). Diff via `go-cmp`; update goldens
    only with a deliberate `-update` flag and a justification in the PR.
  - No engine/tool behavior is "done" without a golden test.
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
  golden/unit tests present and passing · behavior matches HANDOFF.md spec ·
  no new deps without prior approval · no globals/cycles · errors wrapped · ctx
  honored.
- CI runs `make check` on every push; red CI = not mergeable, full stop.

**Commits:**

- Imperative, scoped subject: `engine: stream tool_use blocks as they arrive`.
- Small, frequent, each independently green.

---

## 7. Implementation methodology

- **Spec first, then implement.** Read the relevant section of `HANDOFF.md` before
  writing code. Implement what the architecture describes, not what any reference
  reads. Idiomatic Go that correctly implements the behavior is always right.
- **Faithful by default; deviate only deliberately.** If Go idiom or constraints
  force a behavioral change, call it out explicitly in a comment and the PR
  (e.g. "uses errgroup with 4-attempt backoff instead of inline retry"). Silent
  drift from the spec is the worst failure mode.
- **Concurrency idioms:** event-driven async becomes synchronous Go with `ctx`;
  fan-out becomes `errgroup`; event streams become channels.
- **Schema + validation:** use Go structs → `invopop/jsonschema` for schemas;
  explicit validation code in `Execute` (§4.4). No reflection validators.
- **TUI state:** UI state lives in Bubble Tea model fields + `tea.Cmd`s (§4.3).
  Do not attempt to recreate React hook patterns.
- **Out of scope (confirm with coordinator before building):**
  analytics/telemetry, the CCR cloud/bridge/remote/server infra (unless cloud
  sessions are explicitly in scope), the auto-updater, inline image rendering.
- **Deferred services (Phase 5):**
  - **OAuth login** — authentication belongs to the Korai inference SDK behind
    the `apiclient.Client` boundary (the strangler-fig seam, §4 items 8–10). A
    provider-specific OAuth flow is not built until that backend is chosen.
  - **LSP** — per-language diagnostics are a large side-quest off the MVP path;
    defer until requested. When built, it follows the MCP pattern: a client in
    `internal/lsp` that adapts diagnostics, never an import from upper layers.

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

- [ ] Behavior matches the architecture described in `HANDOFF.md` (or deviations are documented).
- [ ] `make check` passes locally (build, vet, lint, race tests).
- [ ] Unit tests cover the branching logic; a golden test verifies engine/tool behavior.
- [ ] Exported symbols have doc comments; non-obvious design decisions explained.
- [ ] Stays within assigned module; no edits to frozen contracts; no import cycles.
- [ ] No new dependencies beyond the approved set without coordinator sign-off.
- [ ] Errors wrapped, `ctx` honored, no globals, no leftover debug printing.

---

## Approved dependencies (additions require coordinator approval)

- CLI: `github.com/spf13/cobra`
- TUI: `github.com/charmbracelet/bubbletea`, `.../lipgloss`, `.../bubbles`,
  `.../glamour` (markdown→ANSI rendering of assistant text),
  `github.com/76creates/stickers`; TUI tests `.../x/exp/teatest`
- LLM: `github.com/anthropics/anthropic-sdk-go`
- MCP: `github.com/modelcontextprotocol/go-sdk`
- Schema: `github.com/invopop/jsonschema`
- Config: `github.com/joho/godotenv` (loads `.env` for local development)
- Fuzzy match: `github.com/sahilm/fuzzy` (slash-command menu ranking)
- Concurrency: `golang.org/x/sync/errgroup`
- Test compare: `github.com/google/go-cmp`
- Stdlib for everything else (`log/slog`, `net/http`, `os/exec`, `context`, ...).

Everything not on this list is **not approved**. Ask first.
