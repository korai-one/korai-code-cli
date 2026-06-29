package repomap

import "testing"

// findSym returns the first symbol with the given name.
func findSym(syms []Symbol, name string) (Symbol, bool) {
	for _, s := range syms {
		if s.Name == name {
			return s, true
		}
	}
	return Symbol{}, false
}

// wantSym asserts a symbol of the given name exists with the expected kind and
// exported flag.
func wantSym(t *testing.T, syms []Symbol, name string, kind Kind, exported bool) {
	t.Helper()
	s, ok := findSym(syms, name)
	if !ok {
		t.Errorf("symbol %q not found in %+v", name, syms)
		return
	}
	if s.Kind != kind {
		t.Errorf("%q kind = %q, want %q", name, s.Kind, kind)
	}
	if s.Exported != exported {
		t.Errorf("%q exported = %v, want %v", name, s.Exported, exported)
	}
}

// TestTreeSitterPerLanguage proves each grammar's definition query compiles and
// extracts the expected top-level symbols (catches a wrong node-type name, which
// would otherwise silently yield no symbols).
func TestTreeSitterPerLanguage(t *testing.T) {
	t.Run("javascript", func(t *testing.T) {
		syms := Definitions("a.js", "export function foo() {}\nclass Bar {}\nconst baz = () => {}\n")
		wantSym(t, syms, "foo", KindFunc, true)
		wantSym(t, syms, "Bar", KindClass, false)
		wantSym(t, syms, "baz", KindFunc, false)
	})

	t.Run("typescript", func(t *testing.T) {
		src := "export interface Greeter { greet(): string }\n" +
			"export class Impl implements Greeter { greet() { return \"\" } }\n" +
			"type ID = string\n" +
			"function helper() {}\n"
		syms := Definitions("a.ts", src)
		wantSym(t, syms, "Greeter", KindInterface, true)
		wantSym(t, syms, "Impl", KindClass, true)
		wantSym(t, syms, "ID", KindType, false)
		wantSym(t, syms, "helper", KindFunc, false)
	})

	t.Run("tsx", func(t *testing.T) {
		syms := Definitions("a.tsx", "export const App = () => { return null }\ninterface Props {}\n")
		wantSym(t, syms, "App", KindFunc, true)
		wantSym(t, syms, "Props", KindInterface, false)
	})

	t.Run("rust", func(t *testing.T) {
		syms := Definitions("a.rs", "pub fn run() {}\nstruct Config {}\npub trait Service {}\n")
		wantSym(t, syms, "run", KindFunc, true)
		wantSym(t, syms, "Config", KindType, false)
		wantSym(t, syms, "Service", KindInterface, true)
	})

	t.Run("java", func(t *testing.T) {
		syms := Definitions("A.java", "public class Server {}\ninterface Handler {}\n")
		wantSym(t, syms, "Server", KindClass, true)
		wantSym(t, syms, "Handler", KindInterface, false)
	})

	t.Run("ruby", func(t *testing.T) {
		syms := Definitions("a.rb", "class Widget\n  def render\n  end\nend\n")
		wantSym(t, syms, "Widget", KindClass, true)
		wantSym(t, syms, "render", KindMethod, true)
	})

	t.Run("c", func(t *testing.T) {
		syms := Definitions("a.c", "struct Point { int x; };\nint add(int a, int b) { return a; }\n")
		wantSym(t, syms, "Point", KindType, true)
		wantSym(t, syms, "add", KindFunc, true)
	})

	t.Run("cpp", func(t *testing.T) {
		syms := Definitions("a.cpp", "class Engine { public: void run(); };\nnamespace ns {}\n")
		wantSym(t, syms, "Engine", KindClass, true)
		wantSym(t, syms, "ns", KindType, true)
	})
}
