// Package localproto is the wire contract for the direct local channel between
// the CLI and a co-located Korai worker — hop 1 of the local fast path. When the
// CLI and worker run on the same machine, this replaces the loopback OpenAI-HTTP
// hop with a persistent, streaming, length-prefixed binary framing over a
// Unix-domain socket.
//
// It is a byte-for-byte mirror of the worker repo's
// internal/inference/hostproto: the worker reuses one frame vocabulary for both
// the CLI↔worker (north) and worker↔engine (south) boundaries. KEEP THE TWO IN
// LOCKSTEP — a change here needs the same change there.
//
// # Frame format
//
//	[1 byte type][4 bytes big-endian payload length][payload...]
//
// Control frames carry a JSON payload; generated Token frames carry the raw
// UTF-8 bytes of the piece (no JSON wrapping) so the streaming loop pays no
// per-token serialization tax.
package localproto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is exchanged in the Hello/Ready handshake; a mismatch is
// refused rather than misinterpreted.
const ProtocolVersion = 1

// MaxPayloadLen bounds a single frame's payload so a corrupt length prefix can't
// drive an unbounded allocation.
const MaxPayloadLen = 64 << 20

// FrameType identifies a frame. CLI→worker types are < 16; worker→CLI types are
// >= 16.
type FrameType uint8

const (
	// FrameHello is the CLI's opening handshake (HelloPayload).
	FrameHello FrameType = 1
	// FrameOpen carries a full turn (system, tools, messages, sampling) and
	// triggers a generation (OpenPayload).
	FrameOpen FrameType = 2
	// FrameAppend adds messages to the open session and triggers a generation
	// (AppendPayload). Reserved for a future client-side delta optimization.
	FrameAppend FrameType = 3
	// FrameCancel aborts the in-flight generation. No payload.
	FrameCancel FrameType = 4
	// FrameCount asks for a prompt-token count without generating (CountPayload).
	FrameCount FrameType = 5

	// FrameReady is the worker's handshake reply (ReadyPayload).
	FrameReady FrameType = 16
	// FrameToken carries one generated text piece as raw UTF-8.
	FrameToken FrameType = 17
	// FrameToolCall carries a structured tool invocation (ToolCallPayload) — the
	// local path never needs the <tool:…> fence dialect.
	FrameToolCall FrameType = 18
	// FrameUsage carries token accounting (UsagePayload).
	FrameUsage FrameType = 19
	// FrameDone terminates a generation cleanly (DonePayload).
	FrameDone FrameType = 20
	// FrameError terminates a generation with a failure (ErrorPayload).
	FrameError FrameType = 21
)

// String renders a FrameType for logs and errors.
func (t FrameType) String() string {
	switch t {
	case FrameHello:
		return "hello"
	case FrameOpen:
		return "open"
	case FrameAppend:
		return "append"
	case FrameCancel:
		return "cancel"
	case FrameCount:
		return "count"
	case FrameReady:
		return "ready"
	case FrameToken:
		return "token"
	case FrameToolCall:
		return "tool_call"
	case FrameUsage:
		return "usage"
	case FrameDone:
		return "done"
	case FrameError:
		return "error"
	default:
		return fmt.Sprintf("frame(%d)", uint8(t))
	}
}

const headerLen = 5

// ErrPayloadTooLarge is returned by ReadFrame when a frame's declared length
// exceeds MaxPayloadLen.
var ErrPayloadTooLarge = errors.New("localproto: frame payload exceeds maximum")

// WriteFrame writes one frame (type + length-prefixed payload) to w. Callers
// with concurrent writers must serialize their WriteFrame calls.
func WriteFrame(w io.Writer, t FrameType, payload []byte) error {
	if len(payload) > MaxPayloadLen {
		return ErrPayloadTooLarge
	}
	var hdr [headerLen]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("localproto: write header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("localproto: write payload: %w", err)
		}
	}
	return nil
}

// WriteJSON marshals v and writes it as a frame of type t.
func WriteJSON(w io.Writer, t FrameType, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("localproto: marshal %s: %w", t, err)
	}
	return WriteFrame(w, t, payload)
}

// ReadFrame reads one frame from r. io.EOF is returned verbatim on a clean end
// at a frame boundary so callers can detect a closed connection.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, nil, io.EOF
		}
		return 0, nil, fmt.Errorf("localproto: read header: %w", err)
	}
	t := FrameType(hdr[0])
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxPayloadLen {
		return t, nil, ErrPayloadTooLarge
	}
	if n == 0 {
		return t, nil, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return t, nil, fmt.Errorf("localproto: read payload (%s, %d bytes): %w", t, n, err)
	}
	return t, payload, nil
}

// --- payload types -------------------------------------------------------

// HelloPayload is the CLI's handshake.
type HelloPayload struct {
	Version int `json:"version"`
}

// ReadyPayload is the worker's handshake reply.
type ReadyPayload struct {
	Version int      `json:"version"`
	Models  []string `json:"models,omitempty"`
}

// Msg is one chat turn on the wire. Content is raw JSON (a plain string or an
// OpenAI content-parts array) so multimodal prompts survive.
type Msg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ToolDef describes a tool the model may call, carried in OpenPayload.Tools.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Sampling carries per-generation decoding parameters. Zero values mean "worker
// default".
type Sampling struct {
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature float64  `json:"temperature,omitempty"`
	TopP        float64  `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// OpenPayload carries a full turn and triggers a generation.
type OpenPayload struct {
	SessionID string    `json:"session_id"`
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Tools     []ToolDef `json:"tools,omitempty"`
	Messages  []Msg     `json:"messages"`
	Sampling  Sampling  `json:"sampling"`
}

// AppendPayload adds messages to the open session (reserved for future use).
type AppendPayload struct {
	Messages []Msg    `json:"messages"`
	Sampling Sampling `json:"sampling"`
}

// CountPayload requests a prompt-token count without generating.
type CountPayload struct {
	Model    string    `json:"model"`
	System   string    `json:"system,omitempty"`
	Tools    []ToolDef `json:"tools,omitempty"`
	Messages []Msg     `json:"messages"`
}

// ToolCallPayload is a structured tool invocation emitted by the model.
type ToolCallPayload struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// UsagePayload reports token accounting.
type UsagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// DonePayload terminates a generation. FinishReason is "stop" | "length" |
// "cancelled".
type DonePayload struct {
	FinishReason string `json:"finish_reason"`
}

// ErrorPayload terminates a generation with a failure message.
type ErrorPayload struct {
	Message string `json:"message"`
}

// Decode unmarshals a JSON control-frame payload into v.
func Decode(payload []byte, v any) error {
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("localproto: decode payload: %w", err)
	}
	return nil
}
