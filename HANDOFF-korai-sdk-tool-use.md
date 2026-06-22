# Handoff — make tool use a first-class, streamable path in `korai-sdk-go`

**Audience:** a Claude (or engineer) working on the **Korai SDK** itself
(`korai-one/korai` monorepo → `packages/sdk-go`, mirrored to
`korai-one/korai-sdk-go`) and the orchestrator behind `/v1/chat/completions`.

**Author:** the Korai Code CLI team. This is a **consumer's feature request**,
written while wiring the CLI's inference boundary onto `korai-sdk-go@v0.1.0`. It
is not a bug report against a broken build — the SDK works. It documents the one
capability gap that forces the CLI into a worse design, and proposes the smallest
change that closes it.

**TL;DR:** The CLI is a tool-calling agent loop with a streaming terminal UI.
Today, **structured tool calls are only available on the buffered
`ChatComplete` path; the SSE stream drops them.** So the CLI must choose between
streaming text (no reliable tools) and buffered turns (tools + usage, but no
token-by-token output). We want both: **emit structured tool-use and usage over
the stream.** Details and a concrete event shape below.

---

## 1. What the CLI is and why this matters

Korai Code CLI is an LLM-driven **tool-calling loop**. Every turn the model may
emit text *and/or* one or more tool calls; the CLI executes the tools, appends
results, and loops until the model stops calling tools. The UI streams the
model's text into a terminal as it arrives.

This is the dominant shape for coding agents (and for Anthropic's own Messages
API, which the CLI is migrating *off* of). For it to work over Korai, the SDK
must deliver, **on a single streamed turn**:

1. assistant text deltas (✅ already works), **and**
2. **structured tool calls** — `{id, name, input}` — as discrete events (❌ gap), **and**
3. **token usage** for the turn (❌ gap on the streamed path).

We have all three on `ChatComplete` (buffered). We have only #1 on `ChatStream`.

---

## 2. The gap, precisely (from reading v0.1.0 source)

### 2a. The SSE decoder throws tool calls away

`stream.go → decodeChunk` only ever reads `delta.content`, a top-level
`status`, and `error`. The OpenAI streaming delta field **`tool_calls` is not in
the envelope struct**, so it is silently dropped:

```go
// stream.go (v0.1.0) — the decoded envelope has no tool_calls field
Choices []struct {
    Delta struct {
        Role    string `json:"role,omitempty"`
        Content string `json:"content,omitempty"`   // <-- only content
    } `json:"delta"`
    FinishReason *string `json:"finish_reason,omitempty"`
} `json:"choices,omitempty"`
```

`StreamEvent` already declares a `ToolUse *ToolUseEvent` field, and the doc
comment is explicit that it is **aspirational**:

```go
// ToolUseEvent ... The orchestrator currently fences tools as in-band text,
// but the type is here so future structured tool-use fits without an API break.
```

So `Type: "tool_use"` is a defined-but-never-emitted event. A consumer that
switches on `ev.Type` will never see it.

### 2b. Usage is absent from the stream

`StreamEvent` carries no usage. `ChatResponse.Usage` (prompt/completion/total)
is only populated on the buffered `ChatComplete`. A streaming consumer gets zero
usage, so per-turn cost/accounting silently breaks. (The `HANDOFF-anthropic-to-korai.md`
gotcha list already notes this; we're asking to fix it, not just document it.)

### 2c. Net effect on the consumer

Because tools are central, the CLI cannot use `ChatStream` and must fall back to
buffered `ChatComplete`, converting its single response into synthetic stream
events. That costs us token-by-token output (first-token latency becomes
whole-turn latency). We'll ship that as v1, but it's a workaround for this gap.

---

## 3. What we need (in priority order)

1. **Structured tool calls on the SSE stream.** Decode the OpenAI streaming
   `delta.tool_calls` and surface them as `StreamEvent`s. This is the blocker.
2. **Usage on stream termination.** Put token usage on the final chunk /
   `done` event so streamed turns can account cost.
3. **A documented, stable round-trip shape for *sending* tool results back**
   (see §5) so multi-turn tool loops are spec'd, not folklore.

If only #1 ships, the CLI can stream text+tools and use `CountTokens` for
estimates. #1 is the one that changes our architecture.

---

## 4. Proposed event shape (non-breaking, additive)

OpenAI streams a tool call across many chunks: the first delta carries
`index`, `id`, and `function.name`; later deltas append
`function.arguments` fragments; `finish_reason: "tool_calls"` ends it. Mirror
that into the SDK's normalised events without breaking existing `content`
consumers.

Extend `decodeChunk`'s envelope:

```go
Delta struct {
    Role      string `json:"role,omitempty"`
    Content   string `json:"content,omitempty"`
    ToolCalls []struct {
        Index    int    `json:"index"`
        ID       string `json:"id,omitempty"`
        Type     string `json:"type,omitempty"`
        Function struct {
            Name      string `json:"name,omitempty"`
            Arguments string `json:"arguments,omitempty"` // streamed JSON fragments
        } `json:"function,omitempty"`
    } `json:"tool_calls,omitempty"`
} `json:"delta"`
```

Emit (suggestion — pick names that fit SDK convention):

| Event `Type`         | Fields                              | When |
|----------------------|-------------------------------------|------|
| `tool_use_start`     | `ToolUse{Index, ID, Name}`          | first delta of a tool call |
| `tool_use_delta`     | `Index`, `Delta` (arg JSON fragment)| each `function.arguments` chunk |
| `tool_use_stop`      | `Index`                             | call complete (next index starts, or finish_reason) |
| `usage` (or on `done`)| `Usage{PromptTokens, CompletionTokens}` | final chunk |

This maps 1:1 onto what the CLI's boundary already expects
(`ToolCallStart/InputDelta/Complete` + `MessageComplete{Usage}`), and onto
Anthropic's `content_block_start/_delta/_stop` + `message_delta`. A consumer
that only handles `content` is unaffected — the new types are additive.

If per-fragment streaming of arguments is more than the orchestrator can do
today, a **coarser** acceptable first step is a single `tool_use` event per call
carrying the **fully-assembled** `{id, name, input}` once `finish_reason:
"tool_calls"` arrives. That alone unblocks us; delta-streaming of arguments is a
nice-to-have.

---

## 5. The reverse trip — sending tool results back (please spec this)

Multi-turn tool loops also need the **request** side nailed down. Two concerns
found in `llm.go`:

- **Assistant tool calls (outbound).** `Message.ToolCalls` marshals as
  `{id, name, input}` (the SDK's own shape). OpenAI-compatible servers usually
  expect, on a prior assistant turn, the function shape
  `{id, type:"function", function:{name, arguments:"<json-string>"}}`.
  `ToolCall.UnmarshalJSON` accepts **both** inbound, but `Marshal` emits only the
  former. **Please confirm which shape the orchestrator accepts inbound**, and
  ideally make `ToolCall` marshal the OpenAI function shape (or document that the
  structured shape is accepted) so consumers don't hand-roll it.
- **Tool results (outbound).** A `role:"tool"` message needs `tool_call_id` and,
  per the `Message` doc, `name`. Please confirm `name` is actually required (and
  whether it must match the tool or the call id), so consumers thread the right
  value. Anthropic's `tool_result` keys only off the call id, so this is an easy
  place to diverge silently.

A short "tool-use round trip" example in the package README (assistant calls →
execute → `role:"tool"` reply → next turn) would make this unambiguous.

---

## 6. Acceptance check (what "done" looks like for the CLI)

Against a live `baseURL`, a single streamed request with `tools=[...]` where the
model decides to call a tool should let a consumer observe, **in order, on the
stream**:

1. zero or more text deltas,
2. a tool-call start with a non-empty `id` and `name`,
3. the assembled tool `input` (streamed in fragments *or* delivered whole),
4. a terminal event carrying non-zero `PromptTokens`/`CompletionTokens`,

and then accept a follow-up request whose history includes the assistant's tool
call and a `role:"tool"` result, continuing the loop. When that round-trips, the
CLI drops its buffered workaround and streams natively.

---

## 7. Pointers

- Spec / source of truth: `korai-platform/specs/openapi.yaml`
  (`/v1/chat/completions` streaming schema). Whatever ships here must be modeled
  there so codegen (`koraiapi`) stays aligned.
- SDK files to touch: `packages/sdk-go/stream.go` (`decodeChunk`, `StreamEvent`),
  `llm.go` (`ToolCall` marshalling, `StreamEvent` usage), plus `stream_test.go`.
- Cross-SDK parity: the same gap likely exists in `sdk-js` / `sdk-py` decoders —
  worth fixing in lockstep so all three behave identically.
- Consumer context: `HANDOFF-anthropic-to-korai.md` (this repo) §3/§5 lists the
  Anthropic→Korai concept mapping and the known not-1:1 items, including the
  "usage is 0 on the streamed path" note this request supersedes.
