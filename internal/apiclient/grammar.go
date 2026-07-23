package apiclient

// grammar.go — GBNF generation for the tool-fence dialect (atomic-port).
//
// The fence dialect carries a JSON argument object:
//
//	<tool:NAME>{"arg":"value"}</tool>
//
// which renderToolInstructions teaches by prompt alone — no enforcement. When
// the serving backend supports GBNF grammars (llama-server), attaching one can
// make a tool turn *syntactically incapable* of a malformed fence: a closed
// alternation over the registered tool names, with the body constrained to a
// complete JSON object.
//
// This mirrors the Korai worker's own generator
// (korai internal/inference/localsock/grammar.go) so the two ends of the wire
// agree on the dialect. KoraiClient attaches it locally on the HTTP path when
// Request.ConstrainTools is set; the localproto path instead forwards the
// constrain_tools flag and lets the worker generate the grammar itself.
//
// Attachment is OPT-IN per turn (Request.ConstrainTools): a fence grammar
// forces the model to emit a tool call, which would strangle prose turns.
// Backends without grammar support drop the field silently, so enforcement
// degrades to the prompt-only behavior.

import (
	"sort"
	"strings"
)

// jsonBodyGrammar is the static tail of the fence grammar: a JSON object (and
// its dependent rules), modeled on llama.cpp's grammars/json.gbnf so the
// dialect is known-good for llama-server.
const jsonBodyGrammar = `object ::= "{" ws ( string ":" ws value ( "," ws string ":" ws value )* )? "}"
value ::= object | array | string | number | ("true" | "false" | "null") ws
array ::= "[" ws ( value ( "," ws value )* )? "]" ws
string ::= "\"" ( [^"\\\x7F\x00-\x1F] | "\\" (["\\bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F]) )* "\"" ws
number ::= ("-"? ([0-9] | [1-9] [0-9]*)) ("." [0-9]+)? ([eE] [-+]? [0-9]+)? ws
ws ::= [ \t\n]*
`

// toolFenceGrammar builds a GBNF grammar (llama.cpp dialect) whose language is
// exactly one valid tool fence:
//
//	<tool:NAME>{…json object…}</tool>
//
// NAME is a closed alternation over the given tool defs (sorted for
// determinism). Names that are empty after trimming, or that contain a '"' or
// '\' (unquotable in a GBNF literal), are skipped.
//
// Returns "" when no usable tool name remains — callers must treat that as
// "no grammar available" and dispatch unconstrained.
func toolFenceGrammar(tools []ToolDef) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" || strings.ContainsAny(name, `"\`) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("root ::= \"<tool:\" toolname \">\" object \"</tool>\"\n")
	b.WriteString("toolname ::= ")
	for i, n := range names {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("\"" + n + "\"")
	}
	b.WriteString("\n")
	b.WriteString(jsonBodyGrammar)
	return b.String()
}
