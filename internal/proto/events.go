// Package proto defines the JSON wire protocol spoken over the WebSocket
// connection between a `korai serve` process and any thin client (the desktop
// webview, the browser, or a mobile app). Messages are newline-free JSON
// objects, one per WebSocket text frame; the frame boundary is the message
// boundary, so no in-band framing or delimiter is needed.
//
// The protocol is intentionally small and stable: it is the single contract
// every surface shares, so a feature shipped in the Go engine reaches every
// client without a client-side reimplementation. Server→client events mirror
// engine.Event; client→server messages drive the session (user input,
// permission answers, slash commands, abort).
//
// This package has no dependencies beyond the standard library so it can be the
// shared vocabulary without dragging the engine into a client build.
package proto

import "encoding/json"

// Server→client event type tags. Each ServerEvent carries one of these in its
// "type" field so the client can route before decoding the rest.
const (
	// TypeText is an incremental fragment of assistant text.
	TypeText = "text"
	// TypeToolStart announces a tool is about to run.
	TypeToolStart = "tool_start"
	// TypeToolResult carries the outcome of a completed tool.
	TypeToolResult = "tool_result"
	// TypePermReq asks the client to approve or reject a tool call.
	TypePermReq = "perm_req"
	// TypeCompact reports the conversation was auto-compacted.
	TypeCompact = "compact"
	// TypeError reports an in-band error; the turn ends after it.
	TypeError = "error"
	// TypeDone marks the end of a turn.
	TypeDone = "done"
)

// ServerEvent is any message the server sends to the client. The marker method
// keeps the send path type-safe: only the concrete events below satisfy it.
type ServerEvent interface{ isServerEvent() }

// TextEvent is an incremental fragment of assistant text. Clients append Delta
// to the in-progress assistant message.
type TextEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

// Text builds a TextEvent.
func Text(delta string) TextEvent { return TextEvent{Type: TypeText, Delta: delta} }

// ToolStartEvent announces a tool is about to execute. ID correlates this start
// with its later ToolResultEvent so the client can update the matching chip.
type ToolStartEvent struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolStart builds a ToolStartEvent.
func ToolStart(id, name string, input json.RawMessage) ToolStartEvent {
	return ToolStartEvent{Type: TypeToolStart, ID: id, Name: name, Input: input}
}

// ToolResultEvent carries the outcome of a completed tool. ID matches the
// ToolStartEvent it completes (or is fresh for a tool that never started, e.g.
// one denied or blocked before execution).
type ToolResultEvent struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ToolResult builds a ToolResultEvent.
func ToolResult(id, name, content string, isError bool) ToolResultEvent {
	return ToolResultEvent{Type: TypeToolResult, ID: id, Name: name, Content: content, IsError: isError}
}

// PermReqEvent asks the client to approve or reject a tool call. The client
// answers with a PermRes message carrying the same ID. Tool and Input let the
// client render a meaningful prompt (e.g. the bash command, or a diff).
type PermReqEvent struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

// PermReq builds a PermReqEvent.
func PermReq(id, tool string, input json.RawMessage) PermReqEvent {
	return PermReqEvent{Type: TypePermReq, ID: id, Tool: tool, Input: input}
}

// CompactEvent reports the conversation was auto-compacted before a turn.
// Before and After are message counts.
type CompactEvent struct {
	Type   string `json:"type"`
	Before int    `json:"before"`
	After  int    `json:"after"`
}

// Compact builds a CompactEvent.
func Compact(before, after int) CompactEvent {
	return CompactEvent{Type: TypeCompact, Before: before, After: after}
}

// ErrorEvent reports an in-band error (e.g. a model or tool-loop failure). The
// turn ends after it; the connection stays open for the next message.
type ErrorEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Error builds an ErrorEvent.
func Error(message string) ErrorEvent { return ErrorEvent{Type: TypeError, Message: message} }

// DoneEvent marks the end of a turn. The client re-enables input.
type DoneEvent struct {
	Type string `json:"type"`
}

// Done builds a DoneEvent.
func Done() DoneEvent { return DoneEvent{Type: TypeDone} }

func (TextEvent) isServerEvent()       {}
func (ToolStartEvent) isServerEvent()  {}
func (ToolResultEvent) isServerEvent() {}
func (PermReqEvent) isServerEvent()    {}
func (CompactEvent) isServerEvent()    {}
func (ErrorEvent) isServerEvent()      {}
func (DoneEvent) isServerEvent()       {}

// Client→server message type tags.
const (
	// TypeMessage is user input that starts a turn.
	TypeMessage = "message"
	// TypePermRes answers a PermReqEvent.
	TypePermRes = "perm_res"
	// TypeSlash runs a slash command (without the leading slash).
	TypeSlash = "slash"
	// TypeAbort cancels the in-flight turn.
	TypeAbort = "abort"
)

// ClientMsg is any message the client sends to the server. A single struct
// covers every variant because the server unmarshals then switches on Type;
// unused fields stay zero. Only the fields relevant to a given Type are read.
type ClientMsg struct {
	Type string `json:"type"`
	// Text is the user input (Message) or the slash argument string (Slash).
	Text string `json:"text,omitempty"`
	// Cmd is the slash command name without its leading slash (Slash).
	Cmd string `json:"cmd,omitempty"`
	// ID is the permission request being answered (PermRes).
	ID string `json:"id,omitempty"`
	// Approved is the answer to a permission request (PermRes).
	Approved bool `json:"approved,omitempty"`
}
