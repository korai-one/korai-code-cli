# Handoff — replace the Anthropic SDK with the Korai SDK

**Audience:** a Claude (or engineer) migrating an app that currently uses the
Anthropic SDK (`@anthropic-ai/sdk` / `anthropic`) to the Korai SDK.

**TL;DR:** Korai's backend is **OpenAI-compatible**, and the Korai SDK exposes
an ergonomic layer over it (`client.llm.complete/stream/...`). This is **not a
drop-in** for the Anthropic Messages API — the request/response shapes differ.
This doc gives the auth story, the install story (no public registry yet), the
concept mapping, and the gotchas.

---

## 0. First, decide three things

1. **Which language?** Anthropic SDK exists for TS and Python; Korai SDK exists
   for TS, Python, and Go. The language decides the install command **and**
   whether you must publish a mirror first (see §2 — only the **Go** mirror is
   live today).
2. **Which backend URL?** `baseUrl` defaults to `https://cloud.korai.one`. For a
   self-hosted / local orchestrator, point it at that (`http://localhost:8080`
   in dev). Confirm with the operator which to use.
3. **Which model replaces the Claude model in use?** Korai routes by alias
   (`auto` / `fast` / `balanced` / `deep`) or a canonical worker model id, not by
   Anthropic model names. Default to `auto` unless you have a reason. List live
   options with `client.llm.listModels()`.

---

## 1. Auth — yes, there are real endpoints

The SDK authenticates with an **API key** (`sk-korai-…`) sent as
`Authorization: Bearer sk-korai-…`. The middleware also accepts a user JWT, and
inference works **anonymously** (rate-limited) if you pass no key — but for a
real app, mint a key.

**Get a key, two ways:**

- **Dashboard (easiest):** log in to the dashboard → `/platform/api-keys` →
  create key. Copy the `sk-korai-…` value (shown once).
- **HTTP (programmatic):**
  ```
  POST /auth/signup   {email, password, display_name}      # once, no auth
  POST /auth/login    {email, password}            -> { token: <JWT> }
  POST /auth/keys     {label}   (Authorization: Bearer <JWT>)
                                                   -> { key: "sk-korai-…", ... }
  ```
  The raw key is returned **once** and never again. Operations on `/auth/keys`
  require the JWT from `/auth/login`.

Then:
```ts
const client = new KoraiClient({ apiKey: "sk-korai-…", baseUrl: "https://cloud.korai.one" });
```
The key can also come from env: `KORAI_API_KEY` / `KORAI_BASE_URL` are read
automatically if `apiKey`/`baseUrl` are omitted.

> Note: there is **no** "create a key with no account" endpoint — a key always
> belongs to a user. For server apps, mint one key out-of-band and put it in the
> app's secret store, exactly like an `ANTHROPIC_API_KEY`.

---

## 2. Install — git-from-mirror (NO public registry yet)

The SDKs are **not on npm / PyPI yet**. They're published to read-only mirror
repos and installed from git. **Only `korai-sdk-go` is live right now** (`v0.1.0`).
If your target language is **JS or Python, you must release its mirror first**
(see §6) — the `korai-sdk-js` / `korai-sdk-py` repos are currently empty.

| Lang | Install | Status |
|------|---------|--------|
| Go | `go get github.com/korai-one/korai-sdk-go@v0.1.0` | ✅ live (`v0.1.0` tag) |
| TS | `npm install github:korai-one/korai-sdk-js#v0.1.0` (mirror ships built `dist/`) | ⚠️ mirror empty — release first |
| Py | `pip install "git+https://github.com/korai-one/korai-sdk-py@v0.1.0"` | ⚠️ mirror empty — release first |

If `go get` can't resolve a fresh tag, force direct: `GOPROXY=direct go get …`.

---

## 3. The conceptual gap (read this before touching code)

| | Anthropic SDK | Korai SDK |
|---|---|---|
| Wire API | Messages API (Anthropic-native) | **OpenAI-compatible** chat completions |
| Entry | `client.messages.create(...)` | `client.llm.complete(...)` |
| `system` | top-level param | `complete({ system })` (prepended as a system message) or a `{role:"system"}` message |
| Message content | string **or** content blocks (`text`, `image`, `tool_use`, `tool_result`) | `{ role, content: string }` (+ optional `tool_calls` / `tool_call_id` / `name`) |
| Streaming | `client.messages.stream()` + typed events (`content_block_delta`, …) | `client.llm.stream()` → `AsyncIterable<StreamEvent>` (`content_delta`, `citation`, `status`, `message_stop`, `error`) |
| Token count | `client.messages.countTokens(...)` | `client.llm.countTokens({ messages, model })` |
| Tools | `tool_use` / `tool_result` content blocks | OpenAI-style `tool_calls` round-trip (different shape) |
| Errors | `Anthropic.APIError` subclasses | `KoraiAPIError` hierarchy (`KoraiAuthError` 401, `KoraiPermissionError` 403, `KoraiRateLimitError` 429 w/ `retryAfter`, `KoraiServerError` 5xx, `KoraiConnectionError`) |

---

## 4. Concrete mapping (TypeScript)

```ts
// BEFORE (Anthropic)
import Anthropic from "@anthropic-ai/sdk";
const a = new Anthropic({ apiKey: process.env.ANTHROPIC_API_KEY });
const msg = await a.messages.create({
  model: "claude-sonnet-4-6",
  max_tokens: 1024,
  system: "You are helpful.",
  messages: [{ role: "user", content: "Bonjour" }],
});
const text = msg.content[0].type === "text" ? msg.content[0].text : "";
const inTok = msg.usage.input_tokens;

// AFTER (Korai)
import { KoraiClient } from "@korai/sdk";            // package name unchanged once on npm
const k = new KoraiClient({ apiKey: process.env.KORAI_API_KEY });
const res = await k.llm.complete({
  model: "auto",                                     // not a claude-* id
  max_tokens: 1024,
  system: "You are helpful.",
  messages: [{ role: "user", content: "Bonjour" }],
});
const text = res.content;
const inTok = res.input_tokens;
```

**Streaming:**
```ts
// Anthropic: for await (const ev of a.messages.stream({...})) { if (ev.type==="content_block_delta") ... }
for await (const ev of k.llm.stream({ messages, model: "fast" })) {
  if (ev.type === "content_delta") process.stdout.write(ev.delta!);
}
// or fold to a single result:
const res = await k.llm.streamToCompletion({ messages, model: "fast" });
```

**Token count:** `await k.llm.countTokens({ messages, model: "auto" })` → `{ promptTokens, model, resolvedModel? }`.

**Python:** map `Anthropic()` → `SyncKoraiClient(...)` and `AsyncAnthropic()` →
`KoraiClient(...)`; methods are `client.llm.complete(...)` / `.stream(...)` /
`.count_tokens(...)` with `snake_case` fields.

**Go:** `korai.New(korai.WithAPIKey(...), korai.WithBaseURL(...))`; see the
`korai-sdk-go` README + `examples/quickstart`.

---

## 5. Gotchas / not-1:1 (verify each against the app's usage)

- **Models:** any hard-coded `claude-*` model id must be replaced with a Korai
  alias or a canonical id from `listModels()`. There is no model-name compat shim.
- **Tool use is shaped differently.** Anthropic `tool_use`/`tool_result` blocks
  → OpenAI-style `tool_calls` (+ `tool_call_id` / `name` on follow-up messages).
  `buildPayload` preserves those fields; budget real time here if the app uses tools.
- **Vision/multimodal:** the SDK's `Message.content` is a `string`. Image inputs
  (OpenAI `image_url` parts) are not modeled in the ergonomic type yet — they go
  through the raw request path. Flag if the app sends images.
- **Extended thinking:** `CompletionResult.thinking` is currently `null` on the
  buffered path; there's a `thinking` option but no Anthropic-equivalent thinking
  blocks. Don't assume parity.
- **Prompt caching:** Anthropic's cache-control has no Korai equivalent — drop it.
- **Usage on streams:** `input_tokens`/`output_tokens` are `0` on the streamed
  path (only the buffered `complete()` returns usage). Use `countTokens` for
  pre-flight estimates.
- **Citations / `web`:** Korai adds a `web` flag (server-side tool loop) and a
  `citations` array — no Anthropic analogue; ignore unless wanted.
- **Pre-1.0:** the SDK is `v0.1.0`. Pin the version/tag; expect churn.

---

## 6. If the target is JS or Python: release its mirror first

The mirror repos exist but are empty for JS/Py. To populate them you need the
release workflow on `main` (it's currently on the `rpi/sdk` branch) **or** a
manual push like the Go one was done:

- **Via workflow** (preferred): merge `rpi/sdk` → `main`, add the `MIRROR_TOKEN`
  secret, then run the `release-sdks` Action with the version + the js/py toggle.
- **Manual** (what was done for Go): from the monorepo,
  ```
  MIRROR_TOKEN="$(gh auth token)" bash korai-platform/scripts/mirror-package.sh \
    packages/sdk-js korai-one/korai-sdk-js 0.1.0 src/_generated dist
  # JS must be built first: (cd korai-platform && bash scripts/codegen.sh js && cd packages/sdk-js && bun run build)
  ```
  (Py: `… packages/sdk-py korai-one/korai-sdk-py 0.1.0` — no build needed, core is committed.)

See `korai-platform/BUILD.md` and `.github/workflows/release-sdks.yml`.

---

## 7. Migration checklist

- [ ] Confirm language, `baseUrl`, and the replacement model.
- [ ] Obtain an `sk-korai-…` key; put it in the app's secret store as `KORAI_API_KEY`.
- [ ] (JS/Py only) Release the mirror so the package is installable (§6).
- [ ] Install the SDK (§2).
- [ ] Find every Anthropic SDK call site (`messages.create`, `.stream`,
      `.countTokens`, client construction, error handling).
- [ ] Swap client construction + each call per §4; replace model ids.
- [ ] Rework streaming-event handling (different event names/shape).
- [ ] Rework tool-use plumbing if present (different shape).
- [ ] Map error handling to the `KoraiAPIError` hierarchy.
- [ ] Remove Anthropic-only features (prompt caching, thinking blocks, content blocks).
- [ ] Test: a buffered completion, a streamed completion, token counting, and
      (if used) one tool round-trip, against the real `baseUrl`.

---

## 8. References

- SDK source (source of truth): `korai-platform/packages/sdk-{js,go,py}` in the
  `korai-one/korai` monorepo. Mirrors: `korai-one/korai-sdk-{js,go,py}`.
- API surface: `korai-platform/specs/openapi.yaml` (full) — note operator
  endpoints (`jobs`/`workers`) are `x-internal` and excluded from the public SDK.
- Per-language READMEs in each package dir (install + quickstart).
- Endpoint reference: `docs/API.md` in the monorepo.
- Auth detail: `internal/auth/` (key format `sk-korai-` + hex; `middleware.go`
  accepts key or JWT).
```
