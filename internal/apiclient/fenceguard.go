package apiclient

import (
	"encoding/json"
	"strings"
)

// Malformed-fence guard: helpers the engine uses to detect a turn whose tool
// payload the fence layer could not parse cleanly, quote the offending
// fragments back to the model, and record the turn faithfully in history.
//
// They live in apiclient — not the engine — because they are fence-dialect
// knowledge: the engine stays structured (ToolCallBlock in, ToolResultBlock
// out) and only sees opaque quoted fragments plus a ready-made corrective
// notice. See fence.go for the dialect itself.

// maxFragmentQuote caps how much of a malformed fragment is quoted back to the
// model, so a runaway unterminated fence cannot balloon the corrective notice.
const maxFragmentQuote = 400

// MalformedFences inspects a model turn as the engine sees it — the cleaned
// reply text and the parsed tool calls — and returns the malformed fragments
// it finds, quoted for a corrective notice:
//
//   - an unterminated (or otherwise unparsable) fence left in the text by
//     parseToolFences, which deliberately keeps such remnants visible rather
//     than dropping them;
//   - any parsed call whose body is not a valid JSON object, re-rendered as
//     the fence the model emitted.
//
// A nil return means the turn is clean. The engine uses a non-empty return to
// trigger its one-shot malformed-fence retry.
func MalformedFences(text string, calls []ToolCallCompleteEvent) []string {
	var frags []string
	if idx := strings.Index(text, fenceOpenPrefix); idx >= 0 {
		frags = append(frags, truncateFragment(text[idx:]))
	}
	for _, c := range calls {
		if !ValidToolInput(c.Input) {
			frags = append(frags, truncateFragment(renderToolCallFence(c.Name, c.Input)))
		}
	}
	return frags
}

// ValidToolInput reports whether a tool call's raw input is well-formed enough
// to dispatch: a valid JSON object (or empty, which normalizes to {}). It
// deliberately checks only the JSON shape — unknown fields are tolerated,
// matching the tolerant json.Unmarshal every tool already performs — so it
// gates garbage without duplicating any tool's own validation.
func ValidToolInput(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return true // fenceBodyToInput normalizes an empty body to {}
	}
	return strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed))
}

// FenceCorrectionNotice builds the user-role corrective message for a
// malformed-fence retry: it quotes the malformed fragments and restates the
// exact fence format, so the model can re-emit the call correctly (or answer
// without tools).
func FenceCorrectionNotice(fragments []string) string {
	var b strings.Builder
	b.WriteString("[system] Your previous reply contained a malformed tool call that could not be executed:\n")
	for _, f := range fragments {
		b.WriteString("\n```\n")
		b.WriteString(f)
		b.WriteString("\n```\n")
	}
	b.WriteString("\nTo call a tool, emit the block EXACTLY in this format — the body must be a single valid JSON object matching the tool's input schema, and the closing tag is required:\n\n")
	b.WriteString(fenceOpenPrefix)
	b.WriteString("tool_name>{\"param\": \"value\"}")
	b.WriteString(fenceClose)
	b.WriteString("\n\nEmit the corrected tool call now, or answer directly without tools.")
	return b.String()
}

// RenderAssistantTurnText flattens a turn's prose and parsed tool calls back
// into a single fence-dialect string. The engine uses it to record a malformed
// turn in history as plain text: an unexecuted ToolCallBlock would demand a
// matching result block, and a malformed body may not even be valid JSON —
// which json.RawMessage refuses to re-marshal, so it must not enter a block.
// On the fence backends this is wire-equivalent to how an assistant turn's
// blocks are flattened anyway (see convertToKoraiMessages).
func RenderAssistantTurnText(text string, calls []ToolCallCompleteEvent) string {
	var b strings.Builder
	b.WriteString(text)
	for _, c := range calls {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderToolCallFence(c.Name, c.Input))
	}
	return b.String()
}

// truncateFragment caps a quoted fragment at maxFragmentQuote bytes on a rune
// boundary, appending an ellipsis when cut.
func truncateFragment(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxFragmentQuote {
		return s
	}
	cut := maxFragmentQuote
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// isRuneStart reports whether b can start a UTF-8 encoded rune.
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
