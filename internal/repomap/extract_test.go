package repomap

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDefinitionsGo(t *testing.T) {
	src := `package sample

import "fmt"

const Pi = 3.14

type Shape interface {
	Area() float64
}

type Rect struct {
	W, H float64
}

func (r Rect) Area() float64 {
	return r.W * r.H
}

func New(w, h float64) Rect {
	return Rect{W: w, H: h}
}

func main() {
	fmt.Println(New(1, 2).Area())
}
`
	got := Definitions("sample.go", src)
	want := []Symbol{
		{Name: "Pi", Kind: KindConst, Line: 5, Exported: true},
		{Name: "Shape", Kind: KindInterface, Line: 7, Exported: true},
		{Name: "Rect", Kind: KindType, Line: 11, Exported: true},
		{Name: "Area", Kind: KindMethod, Line: 15, Exported: true},
		{Name: "New", Kind: KindFunc, Line: 19, Exported: true},
		{Name: "main", Kind: KindFunc, Line: 23, Exported: false},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Definitions(go) mismatch (-want +got):\n%s", diff)
	}
}

func TestDefinitionsPython(t *testing.T) {
	src := "class Widget:\n" +
		"    def render(self):\n" +
		"        pass\n" +
		"\n" +
		"def _private():\n" +
		"    return 1\n" +
		"\n" +
		"def public():\n" +
		"    return 2\n"

	got := Definitions("thing.py", src)
	want := []Symbol{
		{Name: "Widget", Kind: KindClass, Line: 1, Exported: true},
		{Name: "render", Kind: KindFunc, Line: 2, Exported: true},
		{Name: "_private", Kind: KindFunc, Line: 5, Exported: false},
		{Name: "public", Kind: KindFunc, Line: 8, Exported: true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Definitions(py) mismatch (-want +got):\n%s", diff)
	}
}

func TestDefinitionsUnknownExtension(t *testing.T) {
	if got := Definitions("notes.txt", "anything here\nfunc nope() {}\n"); got != nil {
		t.Errorf("Definitions(.txt) = %v, want nil", got)
	}
}

func TestDefinitionsGoMalformed(t *testing.T) {
	// Must not panic; best-effort regex fallback is acceptable.
	src := "package x\nfunc Foo( {\n  broken\n"
	got := Definitions("broken.go", src)
	found := false
	for _, s := range got {
		if s.Name == "Foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected best-effort to recover Foo from malformed Go, got %v", got)
	}
}

func TestReferencesExcludesOwnAndDedups(t *testing.T) {
	src := `package sample

func helper() int { return 0 }

func Use() int {
	return helper() + helper() + other()
}
`
	got := References("sample.go", src)

	// Own-defined names must be excluded.
	for _, name := range []string{"helper", "Use", "sample"} {
		if contains(got, name) {
			t.Errorf("References included own-defined name %q: %v", name, got)
		}
	}
	// Cross-file reference is captured exactly once (deduped).
	count := 0
	for _, r := range got {
		if r == "other" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected %q exactly once, got %d in %v", "other", count, got)
	}
	// Result must be sorted.
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("References not sorted: %v", got)
			break
		}
	}
}

func TestReferencesGoSelector(t *testing.T) {
	src := `package sample

import "fmt"

func Run() {
	fmt.Println("hi")
}
`
	got := References("sample.go", src)
	if !contains(got, "Println") {
		t.Errorf("expected selector name Println in references, got %v", got)
	}
}

func TestReferencesUnknownExtensionBestEffort(t *testing.T) {
	got := References("notes.txt", "alpha beta alpha gamma")
	want := []string{"alpha", "beta", "gamma"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("References(.txt) mismatch (-want +got):\n%s", diff)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
