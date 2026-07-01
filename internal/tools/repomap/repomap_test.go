package repomap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// TestExecuteBuildsMap drives the tool end-to-end over a tiny repo and checks it
// returns a non-error map containing the source file and its symbols.
func TestExecuteBuildsMap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Run() {}\ntype Server struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(Input{TokenBudget: 2048})
	res, err := New().Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	for _, want := range []string{"<repo_map>", "main.go", "func Run", "type Server"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("map missing %q:\n%s", want, res.Content)
		}
	}
}

// TestExecuteEmptyRepo reports a friendly message (not an error) when there is
// nothing to map.
func TestExecuteEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(Input{})
	res, err := New().Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "no source files") {
		t.Errorf("expected a no-source-files message, got: %s", res.Content)
	}
}

// TestReadOnlyAllowed confirms the tool is read-only and always permitted.
func TestReadOnlyAllowed(t *testing.T) {
	tl := New()
	if !tl.ReadOnly() || !tl.ConcurrencySafe() {
		t.Error("RepoMap should be read-only and concurrency-safe")
	}
}
