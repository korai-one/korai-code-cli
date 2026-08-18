package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// without either side knowing about the other. Only KoraiClient goes through
// here — a backend with native structured tool calls would not need it.

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
	// fenceClosePrefix is the start every closing tag shares. The model is told
	// to emit a bare "</tool>", but open-weight models frequently mirror the
	// open tag and emit a NAMED close "</tool:NAME>" instead. Accept both (see
	// findFenceClose) so a named close does not leave the call unparsed — an
	// unparsed call is never executed, so the user would see a tool block with
	// no result.
	fenceClosePrefix = "</tool"
)

// findFenceClose returns the index and length of the next closing tag in s,
// accepting both the bare "</tool>" and the named "</tool:NAME>" forms. It
// skips false matches like "</toolbox>" where "</tool" is not followed by ">"
// or ":". Returns (-1, 0) when there is no closing tag.
func findFenceClose(s string) (idx, length int) {
	from := 0
	for {
		rel := strings.Index(s[from:], fenceClosePrefix)
		if rel < 0 {
			return -1, 0
		}
		i := from + rel
		after := i + len(fenceClosePrefix)
		if after >= len(s) {
			return -1, 0
		}
		switch s[after] {
		case '>': // bare "</tool>"
			return i, len(fenceClosePrefix) + 1
		case ':': // named "</tool:NAME>" — scan to the terminating '>'
			gt := strings.Index(s[after:], ">")
			if gt >= 0 {
				return i, (after - i) + gt + 1
			}
			// no terminator: treat as not a closer, keep scanning
			from = after
		default: // e.g. "</toolbox" — not a closer
			from = after
		}
	}
}

// renderToolInstructions produces the system-prompt addendum that teaches a
// fence model how to call the given tools. It returns "" when there are no
// tools. The body of each call must be a JSON object matching the tool's input
// schema, so the parsed fence body can be handed straight to the tool.
//
// Arguments are rendered as a compact signature (renderSchema) rather than raw
// JSON Schema. This block is the single largest fixed cost in the system prompt
// — it is re-sent on every request, ahead of the user's first word — and the
// schema JSON was more than a third of it while being mostly punctuation and
// boilerplate ("additionalProperties":false on every object, a "type":"object"
// wrapper the fence format already implies). The model needs each argument's
// name, type, whether it is required, and what it means; it is not validating
// documents. KEEP THIS IN LOCKSTEP with the worker's own renderer
// (korai internal/inference/localsock/fence.go) — the direct binary channel
// renders the block worker-side, so a change here alone would leave the two
// transports teaching the model different dialects.
func renderToolInstructions(tools []ToolDef) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Tools\n\nCall a tool by emitting exactly:\n\n")
	b.WriteString(fenceOpenPrefix)
	b.WriteString("tool_name>{\"param\": \"value\"}")
	b.WriteString(fenceClose)
	b.WriteString("\n\n")
	b.WriteString("- The body is one JSON object; use {} when a tool takes no arguments.\n")
	b.WriteString("- You may call several tools in one reply.\n")
	b.WriteString("- Each call returns a [TOOL RESULT: name] message. Never invent results.\n")
	b.WriteString("- Inspect real files with tools before answering; never guess their contents.\n")
	b.WriteString("- Args read as name:type — meaning. * marks required; omit the rest.\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		b.WriteString("\n## ")
		b.WriteString(t.Name)
		b.WriteByte('\n')
		if t.Description != "" {
			b.WriteString(t.Description)
			b.WriteByte('\n')
		}
		if args := renderSchema(t.InputSchema); args != "" {
			b.WriteString("Args: ")
			b.WriteString(args)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ToolInstructionSize returns the size in bytes of the fence tool-instruction
// block that will be added to the system prompt for the given tools. Token
// estimators use it to account for prompt overhead the engine's message
// history does not show (the block is rendered inside the client, at the
// boundary). It is exact for the fence transports and a close upper bound for
// a hypothetical native-tools backend.
func ToolInstructionSize(tools []ToolDef) int {
	return len(renderToolInstructions(tools))
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

		closeIdx, closeLen := findFenceClose(afterOpen)
		if closeIdx < 0 {
			// No closing tag: malformed, keep the remainder as text.
			out.WriteString(rest)
			break
		}
		body := afterOpen[:closeIdx]
		if name != "" {
			calls = append(calls, fenceCall{Name: name, Input: fenceBodyToInput(body)})
		}
		remaining = afterOpen[closeIdx+closeLen:]
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

// schemaNode is the subset of JSON Schema the compact renderer understands:
// enough to state each argument's name, type, requiredness, and meaning.
// Anything else in the document (additionalProperties, $schema, title, …) is
// deliberately dropped — it costs prompt tokens and tells a calling model
// nothing it can act on.
type schemaNode struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Properties  json.RawMessage `json:"properties"`
	Required    []string        `json:"required"`
	Items       *schemaNode     `json:"items"`
	Enum        []any           `json:"enum"`
}

// renderSchema renders a tool's input schema as a compact argument signature —
// `name*:type — meaning` clauses joined by "; " — or "" when the schema has no
// properties (a no-argument tool, where the old rendering still spent 60-odd
// bytes saying so). See renderToolInstructions for why this is not raw JSON
// Schema.
func renderSchema(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return ""
	}
	return n.fields()
}

// fields renders a node's properties in schema order. Order is preserved
// because it mirrors the tool's Input struct, which reads far better than the
// shuffle encoding/json would give us from a map.
func (n *schemaNode) fields() string {
	keys := orderedKeys(n.Properties)
	if len(keys) == 0 {
		return ""
	}
	var props map[string]*schemaNode
	if err := json.Unmarshal(n.Properties, &props); err != nil {
		return ""
	}
	required := make(map[string]bool, len(n.Required))
	for _, r := range n.Required {
		required[r] = true
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		p := props[k]
		if p == nil {
			continue
		}
		var b strings.Builder
		b.WriteString(k)
		if required[k] {
			b.WriteByte('*')
		}
		b.WriteByte(':')
		b.WriteString(p.compactType())
		// Clauses are joined by "; ", so a description that ends in a period
		// would render as ".; " — drop it.
		if d := strings.TrimRight(strings.TrimSpace(p.Description), "."); d != "" {
			b.WriteString(" — ")
			b.WriteString(d)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "; ")
}

// compactType renders a node's type in the signature's short vocabulary:
// string/int/bool/number, T[] for arrays, {…} for nested objects, and an
// inline "one of" list for enums.
func (n *schemaNode) compactType() string {
	var t string
	switch n.Type {
	case "integer":
		t = "int"
	case "boolean":
		t = "bool"
	case "array":
		if n.Items == nil {
			return "array"
		}
		t = n.Items.compactType() + "[]"
	case "object":
		if f := n.fields(); f != "" {
			return "{" + f + "}"
		}
		t = "object"
	case "":
		t = "any"
	default:
		t = n.Type
	}
	if len(n.Enum) > 0 {
		vals := make([]string, 0, len(n.Enum))
		for _, e := range n.Enum {
			vals = append(vals, fmt.Sprint(e))
		}
		t += "(" + strings.Join(vals, "|") + ")"
	}
	return t
}

// orderedKeys returns the keys of a JSON object in document order.
// encoding/json decodes objects into unordered maps, so the order has to be
// recovered from the raw bytes with a token scan.
func orderedKeys(raw json.RawMessage) []string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // opening '{'
		return nil
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		k, ok := t.(string)
		if !ok {
			return keys
		}
		keys = append(keys, k)
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return keys
		}
	}
	return keys
}
