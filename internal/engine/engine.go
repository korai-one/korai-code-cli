package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// HookFunc is fired at lifecycle points. event is one of the Hook* constants;
// toolName and input are set for tool events and empty otherwise. Returning
// block=true vetoes a PreToolUse call, with reason surfaced to the model. It
// uses only stdlib types so the engine never imports the hook implementation.
type HookFunc func(ctx context.Context, event, toolName string, input json.RawMessage) (block bool, reason string)

// Hook lifecycle event names passed to a HookFunc.
const (
	HookSessionStart = "SessionStart"
	HookPreToolUse   = "PreToolUse"
	HookPostToolUse  = "PostToolUse"
)

// Engine drives the LLM tool-calling loop for a single conversation turn.
type Engine struct {
	client     apiclient.Client
	registry   *tool.Registry
	perm       *perm.Engine
	deps       tool.Deps
	hooks      HookFunc
	models     *apiclient.ModelSelector
	usage      UsageRecorder
	sysSuffix  func() string
	compactFn  CompactFunc
	compactMax int
	estimate   func([]apiclient.Message) int

	// steerMu guards steer: user text injected mid-turn (see Enqueue), drained
	// into history at the top of each tool-loop iteration.
	steerMu sync.Mutex
	steer   []string
}

// Enqueue adds user-typed text to be folded into the running turn at the next
// tool-loop iteration ("mid-turn steering"). It is safe to call concurrently
// with Run and is a no-op for blank text. If no turn is running, the text is
// consumed at the start of the next Run.
func (e *Engine) Enqueue(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	e.steerMu.Lock()
	e.steer = append(e.steer, text)
	e.steerMu.Unlock()
}

// drainSteering folds any queued steering text into history before the next
// model call. To keep message roles valid it appends to a trailing user message
// (e.g. fresh tool results) when there is one, otherwise adds a new user turn.
// It copies the trailing message's blocks rather than mutating the caller's slice.
func (e *Engine) drainSteering(history []apiclient.Message) []apiclient.Message {
	e.steerMu.Lock()
	pending := e.steer
	e.steer = nil
	e.steerMu.Unlock()
	if len(pending) == 0 {
		return history
	}
	blocks := make([]apiclient.ContentBlock, 0, len(pending))
	for _, t := range pending {
		blocks = append(blocks, apiclient.TextBlock{Text: t})
	}
	if n := len(history); n > 0 && history[n-1].Role == apiclient.RoleUser {
		last := history[n-1]
		merged := make([]apiclient.ContentBlock, len(last.Content), len(last.Content)+len(blocks))
		copy(merged, last.Content)
		last.Content = append(merged, blocks...)
		history[n-1] = last
		return history
	}
	return append(history, apiclient.Message{Role: apiclient.RoleUser, Content: blocks})
}

// CompactFunc summarizes a conversation into a shorter one. It is the seam the
// compaction service plugs into; the engine calls it before a turn when the
// history grows past the auto-compact threshold.
type CompactFunc func(ctx context.Context, messages []apiclient.Message) ([]apiclient.Message, error)

// UsageRecorder receives the token usage of each model call along with the model
// that produced it. It is the seam the cost tracker plugs into; usage flows as
// the apiclient type, never a backend-specific one.
type UsageRecorder func(model string, usage apiclient.Usage)

// Option customizes an Engine.
type Option func(*Engine)

// WithHooks attaches a lifecycle hook function. A nil function disables hooks.
func WithHooks(h HookFunc) Option {
	return func(e *Engine) { e.hooks = h }
}

// WithModelSelector attaches a model selector whose current value is stamped
// onto each request, so the model can change between turns (e.g. via /model).
func WithModelSelector(s *apiclient.ModelSelector) Option {
	return func(e *Engine) { e.models = s }
}

// WithUsageRecorder attaches a recorder invoked with the token usage of every
// model call. A nil recorder disables usage reporting.
func WithUsageRecorder(r UsageRecorder) Option {
	return func(e *Engine) { e.usage = r }
}

// WithSystemSuffix attaches a function whose return value is appended to the
// system prompt on every request. It is evaluated per turn, so it can reflect
// runtime state (e.g. plan-mode instructions while in plan mode). An empty
// return adds nothing.
func WithSystemSuffix(fn func() string) Option {
	return func(e *Engine) { e.sysSuffix = fn }
}

// WithAutoCompact enables automatic compaction: before a turn, if the history's
// estimated token count exceeds maxTokens, fn is called to summarize it. A nil
// fn or non-positive maxTokens disables auto-compaction.
func WithAutoCompact(maxTokens int, estimate func([]apiclient.Message) int, fn CompactFunc) Option {
	return func(e *Engine) {
		e.compactMax = maxTokens
		e.compactFn = fn
		e.estimate = estimate
	}
}

// New creates an Engine with the given inference client, tool registry,
// permission engine, and tool dependencies.
func New(client apiclient.Client, registry *tool.Registry, permEngine *perm.Engine, deps tool.Deps, opts ...Option) *Engine {
	e := &Engine{
		client:   client,
		registry: registry,
		perm:     permEngine,
		deps:     deps,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// fireHook invokes the hook function if one is attached.
func (e *Engine) fireHook(ctx context.Context, event, toolName string, input json.RawMessage) (bool, string) {
	if e.hooks == nil {
		return false, ""
	}
	return e.hooks(ctx, event, toolName, input)
}

// Run executes the agent loop starting from messages under system prompt system.
// It returns a channel of events that is closed when the loop finishes or ctx
// is cancelled. The caller must drain the channel.
func (e *Engine) Run(ctx context.Context, messages []apiclient.Message, system string) <-chan Event {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		history, err := e.run(ctx, messages, system, ch)
		if err != nil {
			send(ch, ErrorEvent{Err: err})
			return
		}
		send(ch, DoneEvent{Messages: history})
	}()
	return ch
}

// run drives the tool-calling loop and returns the full conversation history
// after the turn (the model's responses appended to the input messages), so the
// caller can carry context into the next turn.
func (e *Engine) run(ctx context.Context, messages []apiclient.Message, system string, ch chan<- Event) ([]apiclient.Message, error) {
	history := make([]apiclient.Message, len(messages))
	copy(history, messages)

	e.fireHook(ctx, HookSessionStart, "", nil)
	history = e.maybeCompact(ctx, history, ch)

	for {
		// Fold in any text the user typed mid-turn before building the request.
		history = e.drainSteering(history)
		req := e.buildRequest(ctx, history, system)
		turn, err := e.streamTurn(ctx, req, ch)
		if err != nil {
			return history, err
		}
		if e.usage != nil {
			e.usage(req.Model, turn.usage)
		}
		if len(turn.toolCalls) == 0 {
			if turn.stopReason == "max_tokens" {
				slog.Warn("response truncated: hit max output tokens")
			}
			// Record the final assistant text so the next turn has it in context.
			if turn.text != "" {
				history = append(history, apiclient.Message{
					Role:    apiclient.RoleAssistant,
					Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: turn.text}},
				})
			}
			return history, nil
		}

		// Execute all tool calls and collect results.
		assistantContent, results, err := e.executeTools(ctx, turn.toolCalls, ch)
		if err != nil {
			return history, err
		}

		// Prepend any text the model emitted alongside its tool calls so the
		// assistant turn is recorded faithfully.
		if turn.text != "" {
			assistantContent = append(
				[]apiclient.ContentBlock{apiclient.TextBlock{Text: turn.text}},
				assistantContent...,
			)
		}

		// Append the assistant turn (text + tool calls) and the tool results.
		history = append(history,
			apiclient.Message{Role: apiclient.RoleAssistant, Content: assistantContent},
			apiclient.Message{Role: apiclient.RoleUser, Content: results},
		)
	}
}

// maybeCompact summarizes history before a turn when it has grown past the
// configured token threshold. On failure it logs and keeps the original history
// (fail-open: a compaction error must not abort the turn).
func (e *Engine) maybeCompact(ctx context.Context, history []apiclient.Message, ch chan<- Event) []apiclient.Message {
	if e.compactFn == nil || e.estimate == nil || e.compactMax <= 0 {
		return history
	}
	if e.estimate(history) <= e.compactMax {
		return history
	}
	before := len(history)
	compacted, err := e.compactFn(ctx, history)
	if err != nil {
		slog.Warn("auto-compaction failed; continuing without it", "error", err)
		return history
	}
	send(ch, CompactedEvent{Before: before, After: len(compacted)})
	return compacted
}

// turnResult captures what a single model turn produced.
type turnResult struct {
	toolCalls  []apiclient.ToolCallCompleteEvent
	text       string
	stopReason string
	usage      apiclient.Usage
}

// streamTurn calls the model and streams events until the turn ends. It returns
// the tool calls accumulated during the stream, the assistant text emitted, and
// the stop reason reported by the backend.
func (e *Engine) streamTurn(ctx context.Context, req apiclient.Request, ch chan<- Event) (turnResult, error) {
	apiCh, err := e.client.Complete(ctx, req)
	if err != nil {
		return turnResult{}, fmt.Errorf("complete: %w", err)
	}

	var (
		res  turnResult
		text strings.Builder
	)
	for evt := range apiCh {
		if ctx.Err() != nil {
			return turnResult{}, ctx.Err()
		}
		switch v := evt.(type) {
		case apiclient.TextDeltaEvent:
			text.WriteString(v.Text)
			send(ch, TextEvent{Text: v.Text})
		case apiclient.ToolCallCompleteEvent:
			res.toolCalls = append(res.toolCalls, v)
		case apiclient.MessageCompleteEvent:
			res.stopReason = v.StopReason
			res.usage = v.Usage
		case apiclient.ErrorEvent:
			return turnResult{}, v.Err
		}
	}
	res.text = text.String()
	return res, nil
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
// engine, and executes it if allowed. Every outcome (including denial and hook
// blocks) is surfaced as a ToolResultEvent so the UI reflects what happened.
func (e *Engine) dispatchTool(ctx context.Context, call apiclient.ToolCallCompleteEvent, ch chan<- Event) tool.Result {
	t, ok := e.registry.Get(call.Name)
	if !ok {
		return e.blocked(ch, call.Name, fmt.Sprintf("unknown tool %q", call.Name))
	}

	base := t.CheckPermission(ctx, call.Input, e.perm.Mode())
	outcome, err := e.perm.Resolve(ctx, perm.Request{
		ToolName: call.Name,
		Input:    call.Input,
		Base:     base,
	})
	if err != nil {
		return e.blocked(ch, call.Name, fmt.Sprintf("permission resolution for %q failed: %v", call.Name, err))
	}
	if outcome == perm.OutcomeDenied {
		return e.blocked(ch, call.Name, fmt.Sprintf("tool %q was not permitted in the current permission mode", call.Name))
	}

	// PreToolUse hooks may veto the call before it runs.
	if block, reason := e.fireHook(ctx, HookPreToolUse, call.Name, call.Input); block {
		if reason == "" {
			reason = "blocked by a PreToolUse hook"
		}
		return e.blocked(ch, call.Name, reason)
	}

	send(ch, ToolStartEvent{Name: call.Name, Input: call.Input})

	result, err := t.Execute(ctx, call.Input, e.deps)
	if err != nil {
		result = tool.Result{Content: err.Error(), IsError: true}
	}

	e.fireHook(ctx, HookPostToolUse, call.Name, call.Input)

	send(ch, ToolResultEvent{Name: call.Name, Result: result})
	return result
}

// blocked emits an error ToolResultEvent and returns the matching result for a
// call that never executed (unknown tool, denied, or hook-blocked).
func (e *Engine) blocked(ch chan<- Event, name, reason string) tool.Result {
	result := tool.Result{Content: reason, IsError: true}
	send(ch, ToolResultEvent{Name: name, Result: result})
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
	if e.sysSuffix != nil {
		if suffix := e.sysSuffix(); suffix != "" {
			system += "\n\n" + suffix
		}
	}
	req := apiclient.Request{
		System:   system,
		Messages: history,
		Tools:    toolDefs,
	}
	if e.models != nil {
		req.Model = e.models.Get()
	}
	return req
}

func send(ch chan<- Event, e Event) {
	select {
	case ch <- e:
	default:
	}
}
