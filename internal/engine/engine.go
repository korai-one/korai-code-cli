package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Engine drives the LLM tool-calling loop for a single conversation turn.
type Engine struct {
	client   apiclient.Client
	registry *tool.Registry
	perm     *perm.Engine
	deps     tool.Deps
}

// New creates an Engine with the given inference client, tool registry,
// permission engine, and tool dependencies.
func New(client apiclient.Client, registry *tool.Registry, permEngine *perm.Engine, deps tool.Deps) *Engine {
	return &Engine{
		client:   client,
		registry: registry,
		perm:     permEngine,
		deps:     deps,
	}
}

// Run executes the agent loop starting from messages under system prompt system.
// It returns a channel of events that is closed when the loop finishes or ctx
// is cancelled. The caller must drain the channel.
func (e *Engine) Run(ctx context.Context, messages []apiclient.Message, system string) <-chan Event {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		if err := e.run(ctx, messages, system, ch); err != nil {
			send(ch, ErrorEvent{Err: err})
		} else {
			send(ch, DoneEvent{})
		}
	}()
	return ch
}

func (e *Engine) run(ctx context.Context, messages []apiclient.Message, system string, ch chan<- Event) error {
	history := make([]apiclient.Message, len(messages))
	copy(history, messages)

	for {
		req := e.buildRequest(ctx, history, system)
		toolCalls, stopReason, err := e.streamTurn(ctx, req, ch)
		if err != nil {
			return err
		}
		if len(toolCalls) == 0 {
			if stopReason == "max_tokens" {
				slog.Warn("response truncated: hit max output tokens")
			}
			return nil
		}

		// Execute all tool calls and collect results.
		assistantContent, results, err := e.executeTools(ctx, toolCalls, ch)
		if err != nil {
			return err
		}

		// Append the assistant turn (with its tool calls) and the tool results.
		history = append(history,
			apiclient.Message{Role: apiclient.RoleAssistant, Content: assistantContent},
			apiclient.Message{Role: apiclient.RoleUser, Content: results},
		)
	}
}

// streamTurn calls the model and streams events until the turn ends. It returns
// the tool calls accumulated during the stream and the stop reason reported by
// the backend.
func (e *Engine) streamTurn(ctx context.Context, req apiclient.Request, ch chan<- Event) ([]apiclient.ToolCallCompleteEvent, string, error) {
	apiCh, err := e.client.Complete(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("complete: %w", err)
	}

	var (
		toolCalls  []apiclient.ToolCallCompleteEvent
		stopReason string
	)
	for evt := range apiCh {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		switch v := evt.(type) {
		case apiclient.TextDeltaEvent:
			send(ch, TextEvent{Text: v.Text})
		case apiclient.ToolCallCompleteEvent:
			toolCalls = append(toolCalls, v)
		case apiclient.MessageCompleteEvent:
			stopReason = v.StopReason
		case apiclient.ErrorEvent:
			return nil, "", v.Err
		}
	}
	return toolCalls, stopReason, nil
}

// executeTools runs each tool call, gating on permissions, and returns the
// assistant content block list and user-side tool result content blocks.
func (e *Engine) executeTools(ctx context.Context, calls []apiclient.ToolCallCompleteEvent, ch chan<- Event) ([]apiclient.ContentBlock, []apiclient.ContentBlock, error) {
	assistantBlocks := make([]apiclient.ContentBlock, 0, len(calls))
	resultBlocks := make([]apiclient.ContentBlock, 0, len(calls))

	for _, call := range calls {
		assistantBlocks = append(assistantBlocks, apiclient.ToolCallBlock(call))

		result := e.dispatchTool(ctx, call, ch)
		resultBlocks = append(resultBlocks, apiclient.ToolResultBlock{
			ToolCallID: call.ID,
			Content:    result.Content,
			IsError:    result.IsError,
		})
	}
	return assistantBlocks, resultBlocks, nil
}

// dispatchTool looks up the tool, resolves permission through the permission
// engine, and executes it if allowed.
func (e *Engine) dispatchTool(ctx context.Context, call apiclient.ToolCallCompleteEvent, ch chan<- Event) tool.Result {
	t, ok := e.registry.Get(call.Name)
	if !ok {
		return tool.Result{
			Content: fmt.Sprintf("unknown tool %q", call.Name),
			IsError: true,
		}
	}

	base := t.CheckPermission(ctx, call.Input, e.perm.Mode())
	outcome, err := e.perm.Resolve(ctx, perm.Request{
		ToolName: call.Name,
		Input:    call.Input,
		Base:     base,
	})
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("permission resolution for %q failed: %v", call.Name, err),
			IsError: true,
		}
	}
	if outcome == perm.OutcomeDenied {
		return tool.Result{
			Content: fmt.Sprintf("tool %q was not permitted in the current permission mode", call.Name),
			IsError: true,
		}
	}

	send(ch, ToolStartEvent{Name: call.Name, Input: call.Input})

	result, err := t.Execute(ctx, call.Input, e.deps)
	if err != nil {
		result = tool.Result{Content: err.Error(), IsError: true}
	}

	send(ch, ToolResultEvent{Name: call.Name, Result: result})
	return result
}

func (e *Engine) buildRequest(ctx context.Context, history []apiclient.Message, system string) apiclient.Request {
	tools := e.registry.All()
	toolDefs := make([]apiclient.ToolDef, 0, len(tools))
	for _, t := range tools {
		raw, err := json.Marshal(t.InputSchema())
		if err != nil {
			raw = json.RawMessage(`{"type":"object"}`)
		}
		toolDefs = append(toolDefs, apiclient.ToolDef{
			Name:        t.Name(),
			Description: t.Description(ctx),
			InputSchema: raw,
		})
	}
	return apiclient.Request{
		System:   system,
		Messages: history,
		Tools:    toolDefs,
	}
}

func send(ch chan<- Event, e Event) {
	select {
	case ch <- e:
	default:
	}
}
