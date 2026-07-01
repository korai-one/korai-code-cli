package repomap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles creates the given relative path→content files under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBuildRanksAndRenders builds a map over a tiny Go repo and checks the
// output is well-formed and surfaces the most-referenced file's symbols.
func TestBuildRanksAndRenders(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		// core is referenced by both others, so it should rank highly.
		"core/core.go": "package core\n\n" +
			"func Engine() string { return \"\" }\n" +
			"type Config struct{}\n",
		"a/a.go": "package a\n\nimport \"x/core\"\n\n" +
			"func UseA() { core.Engine() }\n",
		"b/b.go": "package b\n\nimport \"x/core\"\n\n" +
			"func UseB() { core.Engine() }\n",
		// a non-source file is ignored by the walker.
		"README.md": "# not source\n",
	})

	out, err := New(dir).Build(context.Background(), Options{TokenBudget: 4096})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.HasPrefix(out, "<repo_map>") || !strings.HasSuffix(strings.TrimRight(out, "\n"), "</repo_map>") {
		t.Fatalf("map not wrapped in <repo_map> tags:\n%s", out)
	}
	for _, want := range []string{"core/core.go", "func Engine", "type Config", "a/a.go", "b/b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("map missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("non-source file leaked into map:\n%s", out)
	}
	// The heavily-referenced core file should appear before the leaf files.
	if idx := strings.Index(out, "core/core.go"); idx >= 0 {
		if a := strings.Index(out, "a/a.go"); a >= 0 && a < idx {
			t.Errorf("expected core/core.go to outrank a/a.go:\n%s", out)
		}
	}
}

// TestBuildEmptyRepo returns an empty string (not an error) when nothing maps.
func TestBuildEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"notes.txt": "just text\n"})
	out, err := New(dir).Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty map for a source-less repo, got:\n%s", out)
	}
}

// TestBudgetTruncates keeps the map within roughly the token budget while still
// emitting at least the top file.
func TestBudgetTruncates(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for _, n := range []string{"one", "two", "three", "four", "five"} {
		files[n+"/"+n+".go"] = "package " + n + "\n\nfunc F" + n + "() {}\nfunc G" + n + "() {}\n"
	}
	writeFiles(t, dir, files)

	tight, err := New(dir).Build(context.Background(), Options{TokenBudget: 8})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tight == "" {
		t.Fatal("tight-budget map should still include the top file")
	}
	full, err := New(dir).Build(context.Background(), Options{TokenBudget: 4096})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(full) <= len(tight) {
		t.Errorf("a larger budget should yield a larger map: tight=%d full=%d", len(tight), len(full))
	}
}
