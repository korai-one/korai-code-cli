package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	korai "github.com/korai-one/korai-sdk-go"
)

// KoraiClient implements Client against the Korai P2P inference network via
// korai-sdk-go. It is the default networked backend. No korai.* type crosses
// this package boundary: everything is converted to apiclient's own types at
// the edge.
//
// Tool use: two dialects, chosen per request by capability probe. Models that
// report supports_tools take the NATIVE path — a real OpenAI `tools` array,
// structured tool_calls on the way back (see native_tools.go). Everything else
// takes the historical FENCE path, where schemas are rendered into the system
// prompt and <tool:NAME>{json}</tool> is parsed back out of the reply text
// (see fence.go). The fence remains the fallback for older orchestrators and
// for models without a tool-call template, so it is not going away.
//
// Either way the engine above never knows: it speaks structured tool calls
// throughout, and this package converts at the edge. No korai.* type crosses
// this boundary.
//
// The dialects are strictly exclusive per request. A model told about its
// tools twice answers in both, and runBuffered honours structured calls over
// fences — silently dropping the fence ones. So the mode is resolved once, up
// front, and the request builder and the response reader both obey it.
//
// Complete uses the buffered ChatComplete path (real token usage, whole-reply
// parsing).
type KoraiClient struct {
	inner *korai.Client
	model string
	tools toolCapabilities
}

// NewKoraiClient creates a client authenticated with apiKey against baseURL.
// An empty baseURL falls back to the SDK default (https://cloud.korai.one) or
// KORAI_BASE_URL from the environment. model selects the Korai routing alias
// (auto / fast / balanced / deep) or a canonical worker model id.
func NewKoraiClient(apiKey, baseURL, model string) *KoraiClient {
	opts := []korai.ClientOption{korai.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, korai.WithBaseURL(baseURL))
	}
	return &KoraiClient{inner: korai.New(opts...), model: model}
}

// Complete implements Client. It converts req into a korai.ChatRequest, runs a
// buffered completion, and adapts the response into our Event channel. See the
// type doc for why this is buffered rather than streamed.
func (c *KoraiClient) Complete(ctx context.Context, req Request) (<-chan Event, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8096
	}

	// One decision, read by both the builder and the reader below. Resolved
	// before anything is built so the two can never disagree about dialect.
	//
	// Only worth asking when tools are actually in play. A turn with no tools
	// and no tool history renders identically either way, so probing would buy
	// nothing and cost a /v1/models round trip on every plain completion.
	mode := toolModeFence
	if len(req.Tools) > 0 || hasToolHistory(req.Messages) {
		mode = c.tools.resolve(ctx, c.inner, model)
	}

	var chatReq korai.ChatRequest
	var err error
	if mode == toolModeNative {
		chatReq, err = c.buildNativeChatRequest(req, model, maxTokens)
	} else {
		chatReq, err = c.buildChatRequest(req, model, maxTokens)
	}
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		c.runBuffered(ctx, chatReq, req, mode, ch)
	}()
	return ch, nil
}

// runBuffered runs one buffered completion — through the SDK's ChatComplete,
// or the extended raw path when req carries fields SDK v0.4.0 does not model —
// and replays the single response as the same ordered Event sequence a
// streaming backend would produce: text, then one start+complete pair per tool
// call, then a final MessageCompleteEvent carrying real usage. Sends are
// blocking and honour ctx so a cancelled turn stops promptly without dropping
// events.
//
// mode is the tool dialect resolved for this request (see native_tools.go). It
// decides whether the reply text is run through the fence parser: in native
// mode it must NOT be, or prose that merely mentions "<tool:" is eaten.
func (c *KoraiClient) runBuffered(ctx context.Context, chatReq korai.ChatRequest, req Request, mode toolMode, ch chan<- Event) {
	var resp *korai.ChatResponse
	var err error
	if needsExtendedRequest(req) {
		resp, err = c.chatCompleteExtended(ctx, chatReq, req)
	} else {
		resp, err = c.inner.ChatComplete(ctx, chatReq)
	}
	if err != nil {
		sendKorai(ctx, ch, ErrorEvent{Err: fmt.Errorf("korai: chat complete: %w", err)})
		return
	}
	if len(resp.Choices) == 0 {
		sendKorai(ctx, ch, ErrorEvent{Err: errors.New("korai: response had no choices")})
		return
	}

	choice := resp.Choices[0]
	msg := choice.Message

	// Fence mode: tool calls arrive as <tool:NAME>{json}</tool> inside the reply
	// text, so strip them out and surface them as structured events. Native
	// mode must NOT run the fence parser: the text is the model's prose, and a
	// model that legitimately writes "<tool:" while explaining itself would
	// have that silently eaten. The cleaned text is emitted first so the UI
	// shows the model's words before any tool activity.
	var fences []fenceCall
	cleanText := msg.Content
	if mode == toolModeFence {
		cleanText, fences = parseToolFences(msg.Content)
	}
	if cleanText != "" {
		if !sendKorai(ctx, ch, TextDeltaEvent{Text: cleanText}) {
			return
		}
	}

	if len(msg.ToolCalls) > 0 {
		// Structured fallback: honor a backend that does return real tool calls.
		for _, tc := range msg.ToolCalls {
			input, err := json.Marshal(tc.Input)
			if err != nil {
				sendKorai(ctx, ch, ErrorEvent{Err: fmt.Errorf("korai: marshaling input for tool %q: %w", tc.Name, err)})
				return
			}
			if !sendKorai(ctx, ch, ToolCallStartEvent{ID: tc.ID, Name: tc.Name}) {
				return
			}
			if !sendKorai(ctx, ch, ToolCallCompleteEvent{ID: tc.ID, Name: tc.Name, Input: input}) {
				return
			}
		}
	} else {
		// Fence path: synthesize an id per call so the engine can match the
		// result back; the model never sees it.
		for _, f := range fences {
			id := uuid.NewString()
			if !sendKorai(ctx, ch, ToolCallStartEvent{ID: id, Name: f.Name}) {
				return
			}
			if !sendKorai(ctx, ch, ToolCallCompleteEvent{ID: id, Name: f.Name, Input: f.Input}) {
				return
			}
		}
	}

	sendKorai(ctx, ch, MessageCompleteEvent{
		StopReason: NormalizeStopReason(choice.FinishReason),
		Usage: Usage{
			InputTokens:  int64(resp.Usage.PromptTokens),
			OutputTokens: int64(resp.Usage.CompletionTokens),
		},
	})
}

// sendKorai delivers e on ch, blocking until the consumer reads it or ctx is
// cancelled. It returns false if ctx was cancelled, so callers stop emitting.
// The buffered path must not drop events — a lost ToolCallCompleteEvent would
// silently break the agent loop — so it blocks rather than doing a best-effort
// non-blocking send.
func sendKorai(ctx context.Context, ch chan<- Event, e Event) bool {
	select {
	case ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// buildChatRequest converts our Request into a korai.ChatRequest using the
// fence dialect. model and maxTokens are resolved by the caller, which needs
// the model to pick the dialect in the first place.
func (c *KoraiClient) buildChatRequest(req Request, model string, maxTokens int64) (korai.ChatRequest, error) {
	msgs, err := convertToKoraiMessages(req.Messages)
	if err != nil {
		return korai.ChatRequest{}, fmt.Errorf("converting messages: %w", err)
	}

	system := req.System
	// Tools are taught via fence instructions in the system prompt, not the
	// OpenAI Tools field — Korai text models ignore the latter. See fence.go.
	if instr := renderToolInstructions(req.Tools); instr != "" {
		if system != "" {
			system += "\n\n" + instr
		} else {
			system = instr
		}
	}

	// Korai's OpenAI-compatible endpoints (the orchestrator and the local
	// worker) read the system prompt from a role="system" message, NOT a
	// top-level "system" field — they have no such field, so it is silently
	// dropped. Send the prompt (with the fence tool instructions) as the first
	// message instead, so the model actually receives it. The top-level System
	// field is left empty deliberately.
	if system != "" {
		msgs = append([]korai.Message{{Role: "system", Content: system}}, msgs...)
	}

	cr := korai.ChatRequest{
		Model:     model,
		Messages:  msgs,
		MaxTokens: int(maxTokens),
	}
	return cr, nil
}

// convertToKoraiMessages flattens our block-structured messages into Korai's
// flat message list using the fence dialect (see fence.go). An assistant turn's
// tool calls are rendered back into <tool:NAME>{json}</tool> text appended to
// its content; a user turn's tool results are rendered as [TOOL RESULT: name]
// text. The tool name for a result is recovered by matching ToolCallID against
// the calls seen on the preceding assistant turn. Nothing uses role="tool" or
// structured ToolCalls, because Korai text models understand neither.
func convertToKoraiMessages(msgs []Message) ([]korai.Message, error) {
	out := make([]korai.Message, 0, len(msgs))
	// toolNames maps a tool_call_id to the tool's name, populated from assistant
	// tool calls and read back when rendering the matching result text.
	toolNames := make(map[string]string)

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			var parts []string
			var text strings.Builder
			var images []ImageBlock
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolResultBlock:
					parts = append(parts, renderToolResultText(toolNames[v.ToolCallID], v.Content, v.IsError))
				case ImageBlock:
					images = append(images, v)
				default:
					return nil, fmt.Errorf("unsupported block %T in user message", b)
				}
			}
			if text.Len() > 0 {
				parts = append([]string{text.String()}, parts...)
			}
			combined := strings.Join(parts, "\n\n")
			if len(images) > 0 {
				// Vision-capable models take multimodal content parts: carry the
				// text (and any tool-result feedback) as a text part, then each
				// image as an image_url part the orchestrator forwards verbatim.
				cps := make([]korai.ContentPart, 0, len(images)+1)
				if combined != "" {
					cps = append(cps, korai.TextPart(combined))
				}
				for _, img := range images {
					cps = append(cps, korai.ImagePart(img.Source))
				}
				out = append(out, korai.UserMessageWithParts(cps...))
				continue
			}
			// One user message carrying the genuine text and/or the tool-result
			// feedback. A genuinely empty user turn still emits an empty message
			// so the turn count is preserved.
			out = append(out, korai.Message{Role: "user", Content: combined})

		case RoleAssistant:
			var text strings.Builder
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolCallBlock:
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(renderToolCallFence(v.Name, v.Input))
					toolNames[v.ID] = v.Name
				default:
					return nil, fmt.Errorf("unsupported block %T in assistant message", b)
				}
			}
			out = append(out, korai.Message{Role: "assistant", Content: text.String()})

		default:
			return nil, fmt.Errorf("unknown role %q", m.Role)
		}
	}
	return out, nil
}
