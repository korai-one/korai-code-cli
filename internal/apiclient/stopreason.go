package apiclient

import "strings"

// Canonical stop reasons. Every backend normalizes its provider-specific finish
// reason to one of these via NormalizeStopReason before it emits a
// MessageCompleteEvent, so code above the apiclient boundary can reason about
// why a turn ended without knowing which backend is live.
const (
	// StopEndTurn is a natural completion — the model finished its reply.
	StopEndTurn = "end_turn"
	// StopMaxTokens means the response was cut off at the output token limit.
	// Any tool calls in that turn may have truncated arguments and must not be
	// executed blindly (see the engine's truncation guard).
	StopMaxTokens = "max_tokens"
	// StopToolUse means the model stopped in order to call tools.
	StopToolUse = "tool_use"
	// StopCanceled means generation was canceled (e.g. by the caller).
	StopCanceled = "canceled"
)

// NormalizeStopReason maps a backend's raw finish reason onto the canonical
// vocabulary above. OpenAI-compatible endpoints report "length" for a
// token-limit truncation and "tool_calls" for a tool stop; the Korai worker
// reports "stop" / "length" / "cancelled". An empty reason is treated as a
// natural end. An unrecognized value is returned trimmed and lowercased so no
// information is lost — this is why the engine matches StopMaxTokens rather than
// the raw "max_tokens" string it previously (and ineffectively) checked.
func NormalizeStopReason(raw string) string {
	switch s := strings.ToLower(strings.TrimSpace(raw)); s {
	case "", "stop", "end_turn", "end", "eos":
		return StopEndTurn
	case "length", "max_tokens", "max_output_tokens", "model_length":
		return StopMaxTokens
	case "tool_calls", "tool_use", "function_call", "tool":
		return StopToolUse
	case "cancelled", "canceled", "abort", "aborted":
		return StopCanceled
	default:
		return s
	}
}
