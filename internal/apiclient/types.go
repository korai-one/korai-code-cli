// Package apiclient is the sole point of contact with the inference backend.
// anthropic-sdk-go types must never cross this package boundary; all callers
// use the types defined here.
package apiclient

import "encoding/json"

// Role identifies the author of a message in a conversation.
type Role string

const (
	// RoleUser is the human turn in a conversation.
	RoleUser Role = "user"
	// RoleAssistant is the model turn in a conversation.
	RoleAssistant Role = "assistant"
)

// Request is the input to a single inference call.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int64
}

// Message is one turn in a conversation.
type Message struct {
	Role    Role
	Content []ContentBlock
}

// ContentBlock is a sealed interface over the variants that can appear in a
// message. Use a type switch to inspect the concrete type.
type ContentBlock interface{ contentBlock() }

// TextBlock holds a plain-text content segment.
type TextBlock struct{ Text string }

// ToolCallBlock holds a tool invocation produced by the model.
type ToolCallBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResultBlock carries the result of an executed tool call back to the model.
type ToolResultBlock struct {
	ToolCallID string
	Content    string
	IsError    bool
}

func (TextBlock) contentBlock()       {}
func (ToolCallBlock) contentBlock()   {}
func (ToolResultBlock) contentBlock() {}

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Name        string
	Description string
	// InputSchema is the full self-contained JSON Schema object for the tool's
	// input (top-level "type":"object", "properties", "required"). The client
	// extracts the pieces the backend needs.
	InputSchema json.RawMessage
}

// Event is emitted by Client.Complete on its output channel.
// Use a type switch over the concrete types below.
type Event interface{ apiEvent() }

// TextDeltaEvent carries an incremental text fragment from the model.
type TextDeltaEvent struct{ Text string }

// ToolCallStartEvent signals that the model has begun generating a tool call.
type ToolCallStartEvent struct {
	ID   string
	Name string
}

// ToolCallInputDeltaEvent carries an incremental JSON fragment for a tool call's input.
type ToolCallInputDeltaEvent struct {
	ID    string
	Delta string
}

// ToolCallCompleteEvent signals that a tool call's input is fully accumulated.
type ToolCallCompleteEvent struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Usage reports token counts for one model call. It is the engine/cost layer's
// own type so backend usage shapes never leak past apiclient.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// MessageCompleteEvent signals that the model has finished its response.
type MessageCompleteEvent struct {
	StopReason string
	Usage      Usage
}

// ErrorEvent wraps a terminal error from the stream.
type ErrorEvent struct{ Err error }

func (TextDeltaEvent) apiEvent()          {}
func (ToolCallStartEvent) apiEvent()      {}
func (ToolCallInputDeltaEvent) apiEvent() {}
func (ToolCallCompleteEvent) apiEvent()   {}
func (MessageCompleteEvent) apiEvent()    {}
func (ErrorEvent) apiEvent()              {}
