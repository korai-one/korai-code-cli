package repomap

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind classifies a defined symbol.
type Kind string

// Symbol kinds recognized across the supported languages.
const (
	KindFunc      Kind = "func"
	KindMethod    Kind = "method"
	KindType      Kind = "type"
	KindInterface Kind = "interface"
	KindConst     Kind = "const"
	KindVar       Kind = "var"
	KindClass     Kind = "class"
)

// Symbol is one defined symbol in a source file.
type Symbol struct {
	Name     string
	Kind     Kind
	Line     int  // 1-based line of the definition
	Exported bool // public/visible from outside the file (heuristic per language)
}

// reIdent matches a single identifier token used for reference tokenization.
var reIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// langOf returns a normalized language tag for the given file extension.
func langOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "py"
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return "js"
	case ".rs":
		return "rs"
	case ".java":
		return "java"
	case ".rb":
		return "rb"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".hpp", ".cxx", ".hh":
		return "cpp"
	default:
		return ""
	}
}

// Definitions returns the top-level / public symbols defined in content, where
// path's extension selects the language. Unknown extensions return nil.
// Results are sorted by Line (then Name). Never panics.
func Definitions(path, content string) (syms []Symbol) {
	defer func() {
		// Guard against any unexpected panic in the parser/regex paths so a
		// malformed file yields a best-effort (possibly nil) result.
		if r := recover(); r != nil {
			syms = nil
		}
	}()

	switch langOf(path) {
	case "go":
		// Go uses the standard-library parser (go/ast): precise, fast, and no
		// cgo for this repo's dominant language.
		syms = extractGoDefs(path, content)
	case "":
		return nil
	default:
		// Every other supported language is parsed with tree-sitter (treesitter.go).
		syms = tsDefinitions(path, content)
	}
	sortSymbols(syms)
	return syms
}

// References returns the distinct identifiers that content refers to (for
// building a cross-file reference graph), excluding the names this file itself
// defines. Sorted, deduplicated. Unknown extensions still return a best-effort
// token list. Never panics.
func References(path, content string) (refs []string) {
	defer func() {
		if r := recover(); r != nil {
			refs = nil
		}
	}()

	lang := langOf(path)
	defined := definedNames(path, content)

	if lang == "go" {
		if r, ok := extractGoRefs(content, defined); ok {
			return r
		}
		// fall through to token-based extraction on parse failure
	}

	stop := stopwordsFor(lang)
	seen := map[string]struct{}{}
	for _, tok := range reIdent.FindAllString(content, -1) {
		if _, bad := stop[tok]; bad {
			continue
		}
		if _, own := defined[tok]; own {
			continue
		}
		seen[tok] = struct{}{}
	}
	refs = keysSorted(seen)
	return refs
}

// definedNames returns the set of symbol names defined by content, used to
// exclude self-references.
func definedNames(path, content string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range Definitions(path, content) {
		out[s.Name] = struct{}{}
	}
	return out
}

// --- Go ---------------------------------------------------------------------

// extractGoDefs parses Go source and returns its top-level definitions. On a
// parse error it falls back to the line/regex extractor.
func extractGoDefs(path, content string) []Symbol {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Base(path), content, 0)
	if err != nil || file == nil {
		return extractGoDefsRegex(content)
	}

	var syms []Symbol
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil {
				continue
			}
			kind := KindFunc
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = KindMethod
			}
			syms = append(syms, Symbol{
				Name:     d.Name.Name,
				Kind:     kind,
				Line:     fset.Position(d.Pos()).Line,
				Exported: ast.IsExported(d.Name.Name),
			})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name == nil {
						continue
					}
					kind := KindType
					if _, ok := s.Type.(*ast.InterfaceType); ok {
						kind = KindInterface
					}
					syms = append(syms, Symbol{
						Name:     s.Name.Name,
						Kind:     kind,
						Line:     fset.Position(s.Pos()).Line,
						Exported: ast.IsExported(s.Name.Name),
					})
				case *ast.ValueSpec:
					vk := KindVar
					if d.Tok == token.CONST {
						vk = KindConst
					}
					for _, name := range s.Names {
						if name == nil || name.Name == "_" {
							continue
						}
						syms = append(syms, Symbol{
							Name:     name.Name,
							Kind:     vk,
							Line:     fset.Position(name.Pos()).Line,
							Exported: ast.IsExported(name.Name),
						})
					}
				}
			}
		}
	}
	return syms
}

// extractGoRefs walks the Go AST and collects referenced identifiers and
// selector names, excluding defined names, keywords and predeclared builtins.
// The boolean result is false when the source could not be parsed.
func extractGoRefs(content string, defined map[string]struct{}) ([]string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "ref.go", content, 0)
	if err != nil || file == nil {
		return nil, false
	}

	stop := stopwordsFor("go")
	seen := map[string]struct{}{}
	// The package name is a declaration, not a cross-file reference.
	pkgName := ""
	if file.Name != nil {
		pkgName = file.Name.Name
	}
	add := func(name string) {
		if name == "" || name == "_" || name == pkgName {
			return
		}
		if _, bad := stop[name]; bad {
			return
		}
		if _, own := defined[name]; own {
			return
		}
		seen[name] = struct{}{}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			add(x.Name)
		case *ast.SelectorExpr:
			if x.Sel != nil {
				add(x.Sel.Name)
			}
		}
		return true
	})
	return keysSorted(seen), true
}

// extractGoDefsRegex is the regex fallback for Go source that fails to parse.
func extractGoDefsRegex(content string) []Symbol {
	var (
		syms       []Symbol
		reGoFunc   = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?(\w+)`)
		reGoMethod = regexp.MustCompile(`^func\s+\([^)]*\)\s*(\w+)`)
		reGoType   = regexp.MustCompile(`^type\s+(\w+)\s+(.*)$`)
	)
	for i, line := range splitLines(content) {
		switch {
		case reGoMethod.MatchString(line):
			m := reGoMethod.FindStringSubmatch(line)
			syms = append(syms, Symbol{Name: m[1], Kind: KindMethod, Line: i + 1, Exported: ast.IsExported(m[1])})
		case reGoFunc.MatchString(line):
			m := reGoFunc.FindStringSubmatch(line)
			syms = append(syms, Symbol{Name: m[1], Kind: KindFunc, Line: i + 1, Exported: ast.IsExported(m[1])})
		case reGoType.MatchString(line):
			m := reGoType.FindStringSubmatch(line)
			k := KindType
			if strings.HasPrefix(strings.TrimSpace(m[2]), "interface") {
				k = KindInterface
			}
			syms = append(syms, Symbol{Name: m[1], Kind: k, Line: i + 1, Exported: ast.IsExported(m[1])})
		}
	}
	return syms
}

// --- helpers ----------------------------------------------------------------

// splitLines splits content into lines without retaining the newline runes.
func splitLines(content string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// sortSymbols orders symbols by line, then by name for stable output.
func sortSymbols(syms []Symbol) {
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Line != syms[j].Line {
			return syms[i].Line < syms[j].Line
		}
		return syms[i].Name < syms[j].Name
	})
}

// keysSorted returns the keys of set as a sorted slice.
func keysSorted(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stopwordsFor returns the keyword/predeclared stoplist for a language. The
// shared baseline is augmented per language; an empty language returns the
// shared baseline only.
func stopwordsFor(lang string) map[string]struct{} {
	switch lang {
	case "go":
		return goStop
	case "py":
		return pyStop
	case "js":
		return jsStop
	case "rs":
		return rsStop
	default:
		return sharedStop
	}
}

// toSet builds a set from a whitespace-separated word list.
func toSet(words string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.Fields(words) {
		out[w] = struct{}{}
	}
	return out
}

var (
	sharedStop = toSet(`if else for while return break continue switch case default
		true false null nil this self new class function def var let const import
		from export public private protected static void int float double string bool`)

	goStop = toSet(`break case chan const continue default defer else fallthrough for
		func go goto if import interface map package range return select struct switch
		type var append cap clear close complex copy delete imag len make max min new
		panic print println real recover bool byte comparable complex64 complex128
		error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16
		uint32 uint64 uintptr true false iota nil any`)

	pyStop = toSet(`False None True and as assert async await break class continue def del
		elif else except finally for from global if import in is lambda nonlocal not or
		pass raise return try while with yield self cls print len range int str float
		bool list dict set tuple`)

	jsStop = toSet(`break case catch class const continue debugger default delete do else
		export extends finally for function if import in instanceof let new return super
		switch this throw try typeof var void while with yield async await of static get
		set true false null undefined console log require module exports`)

	rsStop = toSet(`as async await break const continue crate dyn else enum extern false fn
		for if impl in let loop match mod move mut pub ref return self Self static struct
		super trait true type unsafe use where while bool char str String Vec Option Some
		None Result Ok Err usize isize u8 u16 u32 u64 i8 i16 i32 i64 f32 f64 println print`)
)
