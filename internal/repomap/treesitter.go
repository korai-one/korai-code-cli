package repomap

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// tsDef binds a tree-sitter grammar to the query that extracts its top-level
// definitions. The query captures each definition's name node under a capture
// name (@func, @method, @type, @interface, @class, @const) that maps to a Kind.
type tsDef struct {
	tag      string
	language *sitter.Language
	pattern  string
}

// tsDefs is the per-tag grammar + definition-query table. Node-type names track
// the grammar versions bundled with smacker/go-tree-sitter.
var tsDefs = map[string]tsDef{
	"python": {tag: "python", language: python.GetLanguage(), pattern: `
		(function_definition name: (identifier) @func)
		(class_definition name: (identifier) @class)`},
	"javascript": {tag: "javascript", language: javascript.GetLanguage(), pattern: `
		(function_declaration name: (identifier) @func)
		(class_declaration name: (identifier) @class)
		(variable_declarator name: (identifier) @func value: [(arrow_function) (function_expression)])`},
	"typescript": {tag: "typescript", language: typescript.GetLanguage(), pattern: tsPattern},
	"tsx":        {tag: "tsx", language: tsx.GetLanguage(), pattern: tsPattern},
	"rust": {tag: "rust", language: rust.GetLanguage(), pattern: `
		(function_item name: (identifier) @func)
		(struct_item name: (type_identifier) @type)
		(enum_item name: (type_identifier) @type)
		(union_item name: (type_identifier) @type)
		(trait_item name: (type_identifier) @interface)
		(type_item name: (type_identifier) @type)
		(const_item name: (identifier) @const)
		(static_item name: (identifier) @const)
		(mod_item name: (identifier) @type)`},
	"java": {tag: "java", language: java.GetLanguage(), pattern: `
		(class_declaration name: (identifier) @class)
		(interface_declaration name: (identifier) @interface)
		(enum_declaration name: (identifier) @type)
		(method_declaration name: (identifier) @method)`},
	"ruby": {tag: "ruby", language: ruby.GetLanguage(), pattern: `
		(method name: (identifier) @method)
		(singleton_method name: (identifier) @method)
		(class name: (constant) @class)
		(module name: (constant) @class)`},
	"c": {tag: "c", language: c.GetLanguage(), pattern: `
		(function_definition declarator: (function_declarator declarator: (identifier) @func))
		(struct_specifier name: (type_identifier) @type)
		(enum_specifier name: (type_identifier) @type)
		(union_specifier name: (type_identifier) @type)
		(type_definition declarator: (type_identifier) @type)`},
	"cpp": {tag: "cpp", language: cpp.GetLanguage(), pattern: `
		(function_definition declarator: (function_declarator declarator: (identifier) @func))
		(class_specifier name: (type_identifier) @class)
		(struct_specifier name: (type_identifier) @type)
		(enum_specifier name: (type_identifier) @type)
		(union_specifier name: (type_identifier) @type)
		(type_definition declarator: (type_identifier) @type)
		(namespace_definition name: (namespace_identifier) @type)`},
}

// tsPattern is shared by the TypeScript and TSX grammars (same node types).
const tsPattern = `
	(function_declaration name: (identifier) @func)
	(class_declaration name: (type_identifier) @class)
	(abstract_class_declaration name: (type_identifier) @class)
	(interface_declaration name: (type_identifier) @interface)
	(type_alias_declaration name: (type_identifier) @type)
	(enum_declaration name: (identifier) @type)
	(variable_declarator name: (identifier) @func value: [(arrow_function) (function_expression)])`

// tsExtTag maps a file extension to the grammar tag used for extraction.
var tsExtTag = map[string]string{
	".py": "python",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "tsx",
	".rs":   "rust",
	".java": "java",
	".rb":   "ruby",
	".c":    "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
}

// compiledQuery caches the compiled *sitter.Query per grammar tag. Queries are
// read-only and safe to share across goroutines; query cursors are not, so a
// fresh cursor is created per extraction.
var (
	queryMu    sync.Mutex
	queryCache = map[string]*sitter.Query{}
)

// grammarFor returns the grammar and compiled definition query for path's
// extension. ok is false for unsupported extensions or a query that fails to
// compile against the bundled grammar (in which case extraction is skipped
// rather than crashing).
func grammarFor(path string) (tsDef, *sitter.Query, bool) {
	tag, ok := tsExtTag[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return tsDef{}, nil, false
	}
	def := tsDefs[tag]

	queryMu.Lock()
	defer queryMu.Unlock()
	if q, cached := queryCache[tag]; cached {
		return def, q, q != nil
	}
	q, err := sitter.NewQuery([]byte(def.pattern), def.language)
	if err != nil {
		queryCache[tag] = nil // remember the failure; don't recompile each call
		return def, nil, false
	}
	queryCache[tag] = q
	return def, q, true
}

// tsDefinitions extracts top-level definitions from content using tree-sitter,
// selecting the grammar by path's extension. Returns nil for unsupported
// languages or on any parse/query failure (callers treat nil as "no symbols").
func tsDefinitions(path, content string) []Symbol {
	def, query, ok := grammarFor(path)
	if !ok {
		return nil
	}
	src := []byte(content)

	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(def.language)
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	var syms []Symbol
	seen := map[string]struct{}{}
	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}
		for _, capt := range match.Captures {
			name := capt.Node.Content(src)
			if name == "" {
				continue
			}
			line := int(capt.Node.StartPoint().Row) + 1
			key := name + "\x00" + strconv.Itoa(line)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			syms = append(syms, Symbol{
				Name:     name,
				Kind:     kindForCapture(query.CaptureNameForId(capt.Index)),
				Line:     line,
				Exported: tsExported(def.tag, capt.Node, src),
			})
		}
	}
	return syms
}

// kindForCapture maps a query capture name to a Symbol Kind.
func kindForCapture(capture string) Kind {
	switch capture {
	case "func":
		return KindFunc
	case "method":
		return KindMethod
	case "type":
		return KindType
	case "interface":
		return KindInterface
	case "class":
		return KindClass
	case "const":
		return KindConst
	default:
		return KindVar
	}
}

// tsExported applies each language's visibility convention to decide whether a
// definition is visible outside its file.
func tsExported(tag string, nameNode *sitter.Node, src []byte) bool {
	switch tag {
	case "python", "ruby":
		return !strings.HasPrefix(nameNode.Content(src), "_")
	case "javascript", "typescript", "tsx":
		return hasAncestorType(nameNode, "export_statement")
	case "rust":
		return hasChildType(nameNode.Parent(), "visibility_modifier")
	case "java":
		return modifiersContain(nameNode.Parent(), "public", src)
	default: // c, cpp and anything else: no module-visibility concept
		return true
	}
}

// hasAncestorType reports whether any ancestor of n has the given node type.
func hasAncestorType(n *sitter.Node, typ string) bool {
	for a := n.Parent(); a != nil; a = a.Parent() {
		if a.Type() == typ {
			return true
		}
	}
	return false
}

// hasChildType reports whether n has a direct child of the given node type.
func hasChildType(n *sitter.Node, typ string) bool {
	if n == nil {
		return false
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.Child(i).Type() == typ {
			return true
		}
	}
	return false
}

// modifiersContain reports whether n has a "modifiers" child whose text
// contains word (used for Java's public/private detection).
func modifiersContain(n *sitter.Node, word string, src []byte) bool {
	if n == nil {
		return false
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.Type() == "modifiers" && strings.Contains(c.Content(src), word) {
			return true
		}
	}
	return false
}
