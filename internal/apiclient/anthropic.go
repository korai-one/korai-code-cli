package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// AnthropicClient implements Client against the Anthropic API via
// anthropic-sdk-go. Every direct SDK call is annotated // TODO KORAI SDK —
// these are the swap points when the Korai P2P inference SDK replaces this
// implementation.
type AnthropicClient struct {
	inner anthropic.Client
	model string
}

// NewAnthropicClient creates a client authenticated with apiKey.
// model selects the Anthropic model (e.g. anthropic.ModelClaudeSonnet4_6).
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	c := anthropic.NewClient( // TODO KORAI SDK
		option.WithAPIKey(apiKey),
	)
	return &AnthropicClient{inner: c, model: model}
}

// Complete implements Client. It converts req into Anthropic API types,
// opens a streaming request, converts each event to our own Event type,
// and sends it on the returned channel.
func (c *AnthropicClient) Complete(ctx context.Context, req Request) (<-chan Event, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, fmt.Errorf("building params: %w", err)
	}

	stream := c.inner.Messages.NewStreaming(ctx, params) // TODO KORAI SDK

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		c.drainStream(ctx, stream, ch)
	}()
	return ch, nil
}

// buildParams converts our Request into anthropic.MessageNewParams.
func (c *AnthropicClient) buildParams(req Request) (anthropic.MessageNewParams, error) {
	msgs, err := convertMessages(req.Messages)
	if err != nil {
		return anthropic.MessageNewParams{}, fmt.Errorf("converting messages: %w", err)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8096
	}

	// req.Model overrides the client's default when set (e.g. the /model command).
	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return anthropic.MessageNewParams{}, fmt.Errorf("converting tools: %w", err)
		}
		params.Tools = tools
	}
	return params, nil
}

// drainStream reads from stream and sends converted events to ch.
func (c *AnthropicClient) drainStream(ctx context.Context, stream *ssestream.Stream[anthropic.MessageStreamEventUnion], ch chan<- Event) {
	// Track in-progress tool calls by their index in the content block sequence.
	type pendingTool struct {
		id    string
		name  string
		input strings.Builder
	}
	var pending *pendingTool

	for stream.Next() { // TODO KORAI SDK
		if ctx.Err() != nil {
			return
		}
		evt := stream.Current() // TODO KORAI SDK

		switch evt.Type {
		case "content_block_start":
			block := evt.AsContentBlockStart() // TODO KORAI SDK
			cb := block.ContentBlock
			if cb.Type == "tool_use" {
				tu := cb.AsToolUse() // TODO KORAI SDK
				pending = &pendingTool{id: tu.ID, name: tu.Name}
				send(ch, ToolCallStartEvent{ID: tu.ID, Name: tu.Name})
			}

		case "content_block_delta":
			delta := evt.AsContentBlockDelta() // TODO KORAI SDK
			d := delta.Delta
			switch d.Type {
			case "text_delta":
				send(ch, TextDeltaEvent{Text: d.AsTextDelta().Text}) // TODO KORAI SDK
			case "input_json_delta":
				if pending != nil {
					chunk := d.AsInputJSONDelta().PartialJSON // TODO KORAI SDK
					pending.input.WriteString(chunk)
					send(ch, ToolCallInputDeltaEvent{ID: pending.id, Delta: chunk})
				}
			}

		case "content_block_stop":
			if pending != nil {
				send(ch, ToolCallCompleteEvent{
					ID:    pending.id,
					Name:  pending.name,
					Input: json.RawMessage(pending.input.String()),
				})
				pending = nil
			}

		case "message_delta":
			md := evt.AsMessageDelta() // TODO KORAI SDK
			send(ch, MessageCompleteEvent{
				StopReason: string(md.Delta.StopReason),
				Usage: Usage{ // TODO KORAI SDK
					InputTokens:  md.Usage.InputTokens,
					OutputTokens: md.Usage.OutputTokens,
				},
			})
		}
	}

	if err := stream.Err(); err != nil { // TODO KORAI SDK
		send(ch, ErrorEvent{Err: fmt.Errorf("stream: %w", err)})
	}
}

func send(ch chan<- Event, e Event) {
	select {
	case ch <- e:
	default:
	}
}

// convertMessages converts our Message slice to anthropic.MessageParam slice.
func convertMessages(msgs []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		blocks, err := convertContentBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case RoleUser:
			out = append(out, anthropic.NewUserMessage(blocks...)) // TODO KORAI SDK
		case RoleAssistant:
			out = append(out, anthropic.NewAssistantMessage(blocks...)) // TODO KORAI SDK
		default:
			return nil, fmt.Errorf("unknown role %q", m.Role)
		}
	}
	return out, nil
}

// convertContentBlocks converts our ContentBlock slice to anthropic params.
func convertContentBlocks(blocks []ContentBlock) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case TextBlock:
			out = append(out, anthropic.NewTextBlock(v.Text)) // TODO KORAI SDK
		case ToolCallBlock:
			out = append(out, anthropic.NewToolUseBlock(v.ID, v.Input, v.Name)) // TODO KORAI SDK
		case ToolResultBlock:
			content := v.Content
			if v.IsError {
				content = "ERROR: " + content
			}
			out = append(out, anthropic.NewToolResultBlock(v.ToolCallID, content, v.IsError)) // TODO KORAI SDK
		default:
			return nil, fmt.Errorf("unknown content block type %T", b)
		}
	}
	return out, nil
}

// convertTools converts our ToolDef slice to anthropic.ToolUnionParam slice.
// It parses the full JSON Schema object and extracts the properties and
// required list the API's ToolInputSchemaParam expects.
func convertTools(tools []ToolDef) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema struct {
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}
		if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf("tool %q: bad input schema: %w", t.Name, err)
		}
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description), // TODO KORAI SDK
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: schema.Properties,
				Required:   schema.Required,
			},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out, nil
}
