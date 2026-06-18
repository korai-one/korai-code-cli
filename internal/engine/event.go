// Package engine implements the agent loop: context assembly → LLM call →
// tool execution → repeat until no tool calls remain.
// The engine is headless-first: it emits events on a channel so both
// --print mode and the TUI can consume it without the engine knowing either.
package engine

import (
	"encoding/json"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Event is emitted by Engine.Run on its output channel.
// Use a type switch over the concrete types below.
type Event interface{ engineEvent() }

// TextEvent carries an incremental text fragment produced by the model.
type TextEvent struct{ Text string }

// ToolStartEvent signals that the engine is about to execute a tool.
type ToolStartEvent struct {
	Name  string
	Input json.RawMessage
}

// ToolResultEvent carries the result of a completed tool execution.
type ToolResultEvent struct {
	Name   string
	Result tool.Result
}

// ErrorEvent signals a terminal error. The engine stops after sending this.
type ErrorEvent struct{ Err error }

// DoneEvent signals that the agent loop has finished cleanly. Messages is the
// full conversation history after the turn, for carrying context forward.
type DoneEvent struct{ Messages []apiclient.Message }

// CompactedEvent signals that the conversation was auto-compacted before a turn.
// Before and After are the message counts.
type CompactedEvent struct{ Before, After int }

func (TextEvent) engineEvent()       {}
func (ToolStartEvent) engineEvent()  {}
func (ToolResultEvent) engineEvent() {}
func (ErrorEvent) engineEvent()      {}
func (DoneEvent) engineEvent()       {}
func (CompactedEvent) engineEvent()  {}
