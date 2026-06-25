package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	korai "github.com/korai-one/korai-sdk-go"
)

// KoraiClient implements Client against the Korai P2P inference network via
// korai-sdk-go. It is the sibling of AnthropicClient and the eventual default
// backend. As with AnthropicClient, no korai.* type crosses this package
// boundary: everything is converted to apiclient's own types at the edge.
//
// Tool use today: the Korai SSE stream does not yet carry structured tool calls
// (see HANDOFF-korai-sdk-tool-use.md). Because this CLI is a tool-calling agent,
// Complete therefore uses the buffered ChatComplete path, which returns
// structured tool calls AND real token usage, and synthesizes our streaming
// Event sequence from the single response. When the SDK starts emitting
// structured tool-use events over the stream (the acceptance check in
// HANDOFF-korai-sdk-tool-use.md §6), replace runBuffered with a streaming
// adapter over korai.Client.ChatStream that maps the new tool_use_start /
// tool_use_delta / tool_use_stop / usage events onto our ToolCallStart /
// ToolCallInputDelta / ToolCallComplete / MessageComplete events — nothing
// above this package changes.
type KoraiClient struct {
	inner *korai.Client
	model string
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
	chatReq, err := c.buildChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		c.runBuffered(ctx, chatReq, ch)
	}()
	return ch, nil
}

// runBuffered calls ChatComplete and replays the single response as the same
// ordered Event sequence a streaming backend would produce: text, then one
// start+complete pair per tool call, then a final MessageCompleteEvent carrying
// real usage. Sends are blocking and honour ctx so a cancelled turn stops
// promptly without dropping events.
func (c *KoraiClient) runBuffered(ctx context.Context, req korai.ChatRequest, ch chan<- Event) {
	resp, err := c.inner.ChatComplete(ctx, req)
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

	if msg.Content != "" {
		if !sendKorai(ctx, ch, TextDeltaEvent{Text: msg.Content}) {
			return
		}
	}

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

	sendKorai(ctx, ch, MessageCompleteEvent{
		StopReason: choice.FinishReason,
		Usage: Usage{
			InputTokens:  int64(resp.Usage.PromptTokens),
			OutputTokens: int64(resp.Usage.CompletionTokens),
		},
	})
}

// sendKorai delivers e on ch, blocking until the consumer reads it or ctx is
// cancelled. It returns false if ctx was cancelled, so callers stop emitting.
// Unlike apiclient.send (used by the Anthropic streaming path, where dropping a
// best-effort delta is tolerable), the buffered path must not drop events — a
// lost ToolCallCompleteEvent would silently break the agent loop.
func sendKorai(ctx context.Context, ch chan<- Event, e Event) bool {
	select {
	case ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// buildChatRequest converts our Request into a korai.ChatRequest.
func (c *KoraiClient) buildChatRequest(req Request) (korai.ChatRequest, error) {
	msgs, err := convertToKoraiMessages(req.Messages)
	if err != nil {
		return korai.ChatRequest{}, fmt.Errorf("converting messages: %w", err)
	}

	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8096
	}

	cr := korai.ChatRequest{
		Model:     model,
		Messages:  msgs,
		System:    req.System,
		MaxTokens: int(maxTokens),
	}
	if len(req.Tools) > 0 {
		tools, err := convertToKoraiTools(req.Tools)
		if err != nil {
			return korai.ChatRequest{}, fmt.Errorf("converting tools: %w", err)
		}
		cr.Tools = tools
	}
	return cr, nil
}

// convertToKoraiMessages flattens our block-structured messages into Korai's
// flat OpenAI-style message list. A user turn's text becomes a role="user"
// message; an assistant turn's text + tool calls become one role="assistant"
// message; each tool result becomes its own role="tool" message. Korai requires
// a Name on tool-result messages, which our ToolResultBlock does not carry, so
// the tool name is recovered by matching ToolCallID against the tool calls seen
// on the preceding assistant turn.
func convertToKoraiMessages(msgs []Message) ([]korai.Message, error) {
	out := make([]korai.Message, 0, len(msgs))
	// toolNames maps a tool_call_id to the tool's name, populated from assistant
	// tool calls and read back when emitting the matching role="tool" result.
	toolNames := make(map[string]string)

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			var text strings.Builder
			var toolResults []korai.Message
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolResultBlock:
					content := v.Content
					if v.IsError {
						content = "ERROR: " + content
					}
					toolResults = append(toolResults, korai.Message{
						Role:       "tool",
						Content:    content,
						ToolCallID: v.ToolCallID,
						Name:       toolNames[v.ToolCallID],
					})
				default:
					return nil, fmt.Errorf("unsupported block %T in user message", b)
				}
			}
			// Emit a user message only when there is actual text (or when the
			// turn is genuinely empty), so a results-only turn does not inject a
			// stray blank user message between the assistant call and its results.
			if text.Len() > 0 || len(toolResults) == 0 {
				out = append(out, korai.Message{Role: "user", Content: text.String()})
			}
			out = append(out, toolResults...)

		case RoleAssistant:
			var text strings.Builder
			var calls []korai.ToolCall
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolCallBlock:
					var input map[string]any
					if len(v.Input) > 0 {
						if err := json.Unmarshal(v.Input, &input); err != nil {
							return nil, fmt.Errorf("tool call %q: bad input json: %w", v.Name, err)
						}
					}
					calls = append(calls, korai.ToolCall{ID: v.ID, Name: v.Name, Input: input})
					toolNames[v.ID] = v.Name
				default:
					return nil, fmt.Errorf("unsupported block %T in assistant message", b)
				}
			}
			out = append(out, korai.Message{Role: "assistant", Content: text.String(), ToolCalls: calls})

		default:
			return nil, fmt.Errorf("unknown role %q", m.Role)
		}
	}
	return out, nil
}

// convertToKoraiTools renders our tool definitions into the OpenAI tool schema
// shape Korai's chat-completions endpoint expects. The full JSON Schema object
// is passed through verbatim as the function parameters.
func convertToKoraiTools(tools []ToolDef) ([]any, error) {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &params); err != nil {
				return nil, fmt.Errorf("tool %q: bad input schema: %w", t.Name, err)
			}
		}
		out = append(out, korai.OpenAITool{
			Type: "function",
			Function: korai.OpenAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return out, nil
}
