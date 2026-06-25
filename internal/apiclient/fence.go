package apiclient

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Korai hosts open-weight models that are not trained for OpenAI-style
// structured tool calls. The whole Korai stack — the orchestrator's tool loop
// and the local worker alike — uses a prompt-based text-fence dialect instead:
// the model is told to emit
//
//	<tool:NAME>{"arg":"value"}</tool>
//
// and the client parses those fences back out of the reply text. This file is
// the anti-corruption layer that lets the structured engine (which speaks
// apiclient's ToolDef / ToolCallBlock / ToolResultBlock) talk to a fence model
// without either side knowing about the other. The Anthropic backend keeps
// using real structured tool calls; only KoraiClient goes through here.

// fenceCall is one tool invocation parsed out of a model reply. Input is the
// raw JSON body of the fence, passed verbatim to the tool (whose own schema
// validation surfaces a malformed body as a tool error).
type fenceCall struct {
	Name  string
	Input json.RawMessage
}

const (
	fenceOpenPrefix = "<tool:"
	fenceClose      = "</tool>"
)

// renderToolInstructions produces the system-prompt addendum that teaches a
// fence model how to call the given tools. It returns "" when there are no
// tools. The body of each call must be a JSON object matching the tool's input
// schema, so the parsed fence body can be handed straight to the tool.
func renderToolInstructions(tools []ToolDef) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Tools\n\n")
	b.WriteString("You can use tools. To call one, emit a fenced block EXACTLY in this ")
	b.WriteString("format, where the body is a single JSON object matching the tool's ")
	b.WriteString("input schema:\n\n")
	b.WriteString(fenceOpenPrefix)
	b.WriteString("tool_name>{\"param\": \"value\"}")
	b.WriteString(fenceClose)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Emit the block exactly; the body must be valid JSON (use {} for no arguments).\n")
	b.WriteString("- You may call several tools in one reply.\n")
	b.WriteString("- After each call you receive a [TOOL RESULT: name] message. Never invent results.\n")
	b.WriteString("- Inspect real files with tools before answering; do not guess their contents.\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		b.WriteString("\n## ")
		b.WriteString(t.Name)
		b.WriteByte('\n')
		if t.Description != "" {
			b.WriteString(t.Description)
			b.WriteByte('\n')
		}
		if schema := compactJSON(t.InputSchema); schema != "" && schema != "null" {
			b.WriteString("Input schema: ")
			b.WriteString(schema)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// parseToolFences splits a model reply into its plain text (with every fence
// removed) and the ordered list of tool calls it contained. It is tolerant of
// malformed input: an unterminated fence is left in the text rather than
// dropped, so a partial or mis-emitted block is visible to the user instead of
// silently swallowed.
func parseToolFences(text string) (clean string, calls []fenceCall) {
	var out strings.Builder
	remaining := text
	for {
		start := strings.Index(remaining, fenceOpenPrefix)
		if start < 0 {
			out.WriteString(remaining)
			break
		}
		out.WriteString(remaining[:start])
		rest := remaining[start:]

		nameEnd := strings.Index(rest, ">")
		if nameEnd < 0 {
			// No closing '>' for the open tag: malformed, keep as text.
			out.WriteString(rest)
			break
		}
		name := strings.TrimSpace(rest[len(fenceOpenPrefix):nameEnd])
		afterOpen := rest[nameEnd+1:]

		closeIdx := strings.Index(afterOpen, fenceClose)
		if closeIdx < 0 {
			// No closing tag: malformed, keep the remainder as text.
			out.WriteString(rest)
			break
		}
		body := afterOpen[:closeIdx]
		if name != "" {
			calls = append(calls, fenceCall{Name: name, Input: fenceBodyToInput(body)})
		}
		remaining = afterOpen[closeIdx+len(fenceClose):]
	}
	return strings.TrimSpace(out.String()), calls
}

// renderToolCallFence renders a structured tool call back into the fence text a
// fence model expects to see in the assistant's prior turn. Used when replaying
// conversation history to the model.
func renderToolCallFence(name string, input json.RawMessage) string {
	body := strings.TrimSpace(string(input))
	if body == "" {
		body = "{}"
	}
	return fenceOpenPrefix + name + ">" + body + fenceClose
}

// renderToolResultText renders a tool result as the plain-text feedback message
// a fence model expects (it has no role="tool" concept). name is the tool that
// produced it; an error result is labelled so the model can react.
func renderToolResultText(name, content string, isError bool) string {
	if name == "" {
		name = "tool"
	}
	label := "[TOOL RESULT: " + name + "]"
	if isError {
		label = "[TOOL ERROR: " + name + "]"
	}
	return label + "\n" + content
}

// fenceBodyToInput normalizes a fence body into a JSON input. An empty body
// becomes "{}" (a no-argument call); anything else is passed through verbatim
// for the tool's own schema validation to judge.
func fenceBodyToInput(body string) json.RawMessage {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

// compactJSON removes insignificant whitespace from a JSON document so a schema
// renders on one line in the prompt. Invalid or empty input is returned
// trimmed, unchanged.
func compactJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err != nil {
		return trimmed
	}
	return buf.String()
}
