// Package apiclient is the sole point of contact with the inference backend.
// No backend SDK types (korai-sdk-go) may cross this package boundary; all
// callers use the types defined here.
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

	// Sampling carries optional decoding parameters; the zero value means
	// "backend defaults" throughout.
	Sampling Sampling

	// Grammar is a raw GBNF grammar constraining the completion. Wins over
	// JSONSchema when both are set. Backends without grammar support ignore it
	// silently, degrading to unconstrained decoding.
	Grammar string

	// JSONSchema constrains the completion to structured output. Ignored when
	// Grammar is set.
	JSONSchema json.RawMessage

	// ConstrainTools opts this turn into grammar-enforced tool fences: when set
	// (and Grammar/JSONSchema are empty) a transport that can, attaches — or
	// has the worker generate — a closed-alternation GBNF grammar over Tools,
	// so the model is syntactically incapable of a malformed fence. Opt-in per
	// turn ON PURPOSE: a fence grammar forces the model to emit a tool call,
	// which would strangle prose turns. The engine sets it only on the
	// malformed-fence retry turn.
	ConstrainTools bool
}

// Sampling carries optional per-request decoding parameters. Pointer semantics
// mirror the Korai wire contract: nil means "absent — backend default", a
// non-nil pointer to zero is a deliberate zero that must survive the wire.
// (Temperature and TopP ride OpenAI-compatible non-pointer wire fields with
// omitempty on some transports, where an explicit zero is indistinguishable
// from absent — a known wire limitation shared with the Korai repo.)
type Sampling struct {
	// Temperature scales the sampling distribution (0 = greedy-ish).
	Temperature *float64
	// TopP is nucleus sampling: keep the smallest set of tokens whose
	// cumulative probability exceeds TopP.
	TopP *float64
	// TopK limits sampling to the K most likely tokens.
	TopK *int
	// MinP discards tokens below MinP × p(top token).
	MinP *float64
	// Seed pins the sampling RNG for reproducible generations.
	Seed *int
	// RepeatPenalty is the llama.cpp-style repetition penalty (1.0 = off).
	RepeatPenalty *float64
	// FrequencyPenalty / PresencePenalty are the OpenAI-style penalties.
	FrequencyPenalty *float64
	PresencePenalty  *float64
}

// isZero reports whether every sampling parameter is absent.
func (s Sampling) isZero() bool {
	return s.Temperature == nil && s.TopP == nil && s.TopK == nil && s.MinP == nil &&
		s.Seed == nil && s.RepeatPenalty == nil && s.FrequencyPenalty == nil && s.PresencePenalty == nil
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

// ImageBlock holds an image to send to a vision-capable model. Source is a data
// URI ("data:image/png;base64,<...>") or an https URL. Backends that cannot
// take images convert or drop it at the edge (the Korai path forwards it).
type ImageBlock struct{ Source string }

func (TextBlock) contentBlock()       {}
func (ToolCallBlock) contentBlock()   {}
func (ToolResultBlock) contentBlock() {}
func (ImageBlock) contentBlock()      {}

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
