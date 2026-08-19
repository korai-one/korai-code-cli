package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	korai "github.com/korai-one/korai-sdk-go"
)

// Native OpenAI tool calling for the Korai backend.
//
// Historically every Korai model was driven through the text-fence dialect in
// fence.go (<tool:NAME>{json}</tool>), because the stack could not carry an
// OpenAI `tools` array end to end. It can now, and the models on the ladder
// ship real tool-call chat templates, so this file adds the native path.
//
// The two dialects are STRICTLY exclusive per request. Mixing them is the one
// thing that must never happen: a model told about its tools twice — once in
// the system prompt and once in the `tools` array — will happily answer in
// both dialects, and the response handler honours structured tool calls over
// fences, silently dropping whatever came back as a fence. So the mode is
// resolved once per request, before anything is built, and every stage reads
// that one decision.
//
// Selection needs TWO independent answers, and conflating them is a trap:
//
//   1. Can the MODEL call tools? /v1/models reports supports_tools per model
//      and the SDK surfaces it on ModelInfo.
//   2. Can the ORCHESTRATOR carry an OpenAI `tools` array end to end?
//
// supports_tools answers only the first. It is already true for the models on
// the current ladder, while the deployed orchestrator still drops `tools` on
// the floor — it has no such field on its request body. Switching on
// supports_tools alone would send tools that nothing reads and strip the fence
// instructions that were the model's only way to learn about its tools: every
// tool call would vanish, silently, against production.
//
// There is no server-capability endpoint to probe for (2), so it is an
// explicit opt-in — KORAI_NATIVE_TOOLS=1 — defaulting OFF. Flip the default
// here once the orchestrator ships native tools and the fleet has rolled over;
// that is a one-line change and this comment is the note to do it.
//
// Everything unknown falls back to the fence: probe failed, model absent from
// the listing, orchestrator too old to report the field, opt-in unset. The
// fence works everywhere; native tools work only on a new enough stack.

// toolMode is the resolved tool dialect for one request.
type toolMode int

const (
	// toolModeFence renders tool schemas into the system prompt and parses
	// <tool:NAME>{json}</tool> back out of the reply text.
	toolModeFence toolMode = iota
	// toolModeNative sends an OpenAI `tools` array and reads structured
	// tool_calls off the reply.
	toolModeNative
)

func (m toolMode) String() string {
	if m == toolModeNative {
		return "native"
	}
	return "fence"
}

// toolCapabilities caches the per-model supports_tools answer for one client.
// The listing is one HTTP call and the answer is stable for a session, so it
// is fetched at most once; a failed probe is cached as "unknown" too, so a
// broken or old orchestrator does not cost a round trip per turn.
type toolCapabilities struct {
	once   sync.Once
	byID   map[string]bool
	probed bool
}

// nativeToolsEnvVar opts this client into native tool calling. See the package
// note above: the model-side capability is probed, but the server-side one
// cannot be, so it is opt-in until the orchestrator ships it.
const nativeToolsEnvVar = "KORAI_NATIVE_TOOLS"

// nativeToolsEnabled reports the opt-in. Anything other than "1"/"true" is off,
// including unset — the safe default is the dialect that works everywhere.
func nativeToolsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(nativeToolsEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// resolve reports whether model can take native tools. It never returns an
// error: a probe failure is indistinguishable, for our purposes, from a model
// that cannot do tools, and both must land on the fence path.
func (tc *toolCapabilities) resolve(ctx context.Context, inner *korai.Client, model string) toolMode {
	// Cheapest gate first, and the one that keeps us safe against a production
	// orchestrator that cannot carry tools yet. Also skips the probe entirely.
	if !nativeToolsEnabled() {
		return toolModeFence
	}

	tc.once.Do(func() {
		if inner == nil {
			return
		}
		models, err := inner.ListModelsDetailed(ctx)
		if err != nil {
			// Old orchestrator, no network, or an auth failure that the real
			// request is about to report properly anyway. Stay on the fence.
			return
		}
		tc.byID = make(map[string]bool, len(models))
		for _, m := range models {
			tc.byID[m.ID] = m.SupportsTools
		}
		tc.probed = true
	})

	if !tc.probed {
		return toolModeFence
	}
	// An alias ("auto"/"fast"/"balanced"/"deep") appears in the listing in its
	// own right, so this covers both aliases and canonical ids. A model the
	// orchestrator does not list cannot be reasoned about — use the fence.
	if native, ok := tc.byID[model]; ok && native {
		return toolModeNative
	}
	return toolModeFence
}

// buildNativeChatRequest converts req into a korai.ChatRequest that carries an
// OpenAI `tools` array and structured tool history. The system prompt gets NO
// fence instructions — that is the whole point of this path.
func (c *KoraiClient) buildNativeChatRequest(req Request, model string, maxTokens int64) (korai.ChatRequest, error) {
	msgs, err := convertToNativeMessages(req.Messages)
	if err != nil {
		return korai.ChatRequest{}, fmt.Errorf("converting messages: %w", err)
	}

	// Same reason as the fence path: Korai's OpenAI-compatible endpoints read
	// the system prompt from a role="system" message, not a top-level field.
	if req.System != "" {
		msgs = append([]korai.Message{{Role: "system", Content: req.System}}, msgs...)
	}

	tools, err := convertToNativeTools(req.Tools)
	if err != nil {
		return korai.ChatRequest{}, err
	}

	return korai.ChatRequest{
		Model:     model,
		Messages:  msgs,
		MaxTokens: int(maxTokens),
		Tools:     tools,
	}, nil
}

// convertToNativeTools renders our ToolDefs as OpenAI function-tool objects.
// InputSchema is already a complete JSON Schema object, so it becomes
// `parameters` verbatim — no rewriting, so a schema feature we do not model
// still reaches the engine.
func convertToNativeTools(tools []ToolDef) ([]any, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		params := t.InputSchema
		if len(params) == 0 {
			// A tool with no inputs still needs a schema; an absent
			// `parameters` makes some engines reject the whole request.
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if !json.Valid(params) {
			return nil, fmt.Errorf("tool %q: input schema is not valid JSON", t.Name)
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out, nil
}

// convertToNativeMessages flattens our block-structured messages into the
// OpenAI shape: an assistant turn's tool calls ride on `tool_calls`, and each
// tool result becomes its own role="tool" message keyed by tool_call_id.
//
// Ordering is load-bearing. OpenAI requires every role="tool" message to
// follow the assistant turn that requested it, so results are emitted BEFORE
// the user's own text for that turn, in the order the blocks appear.
//
// Unlike the fence path this does NOT emit an empty user message to preserve
// turn count: a turn that carries only tool results is exactly the role="tool"
// sequence, and a trailing empty user message would be a protocol error rather
// than a harmless no-op.
func convertToNativeMessages(msgs []Message) ([]korai.Message, error) {
	out := make([]korai.Message, 0, len(msgs))

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			var text strings.Builder
			var images []ImageBlock
			var results []korai.Message

			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolResultBlock:
					content := v.Content
					if v.IsError {
						// The engine sees one content string per result, so the
						// error marker has to live in the text. Keep it terse
						// and stable — models key off the prefix.
						content = "ERROR: " + content
					}
					results = append(results, korai.Message{
						Role:       "tool",
						Content:    content,
						ToolCallID: v.ToolCallID,
					})
				case ImageBlock:
					images = append(images, v)
				default:
					return nil, fmt.Errorf("unsupported block %T in user message", b)
				}
			}

			// Tool results first: they answer the preceding assistant turn.
			out = append(out, results...)

			if len(images) > 0 {
				cps := make([]korai.ContentPart, 0, len(images)+1)
				if text.Len() > 0 {
					cps = append(cps, korai.TextPart(text.String()))
				}
				for _, img := range images {
					cps = append(cps, korai.ImagePart(img.Source))
				}
				out = append(out, korai.UserMessageWithParts(cps...))
				continue
			}
			if text.Len() > 0 {
				out = append(out, korai.Message{Role: "user", Content: text.String()})
			}

		case RoleAssistant:
			var text strings.Builder
			var calls []korai.ToolCall
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolCallBlock:
					input, err := toolInputMap(v.Input)
					if err != nil {
						return nil, fmt.Errorf("tool call %q: %w", v.Name, err)
					}
					calls = append(calls, korai.ToolCall{ID: v.ID, Name: v.Name, Input: input})
				default:
					return nil, fmt.Errorf("unsupported block %T in assistant message", b)
				}
			}
			out = append(out, korai.Message{
				Role:      "assistant",
				Content:   text.String(),
				ToolCalls: calls,
			})

		default:
			return nil, fmt.Errorf("unknown role %q", m.Role)
		}
	}
	return out, nil
}

// toolInputMap decodes a tool call's recorded arguments into the map the SDK
// carries. An absent or literal-null input is an empty object, not an error:
// a no-argument tool legitimately records nothing.
func toolInputMap(raw json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("input is not a JSON object: %w", err)
	}
	return m, nil
}
