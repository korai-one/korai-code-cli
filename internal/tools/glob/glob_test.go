package glob_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/glob"
)

// buildTree creates a hermetic nested file tree under a fresh temp dir and
// returns its path:
//
//	a.go
//	sub/b.go
//	sub/deep/c.go
//	sub/notes.txt
//	.git/x
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"a.go",
		"sub/b.go",
		"sub/deep/c.go",
		"sub/notes.txt",
		".git/x",
	}
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", f, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return root
}

func run(t *testing.T, root string, in glob.Input) tool.Result {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := glob.New().Execute(context.Background(), raw, tool.Deps{WorkDir: root})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	return res
}

func lines(res tool.Result) []string {
	if res.Content == "" {
		return nil
	}
	return strings.Split(res.Content, "\n")
}

func TestExecute_Matching(t *testing.T) {
	root := buildTree(t)

	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "top-level go only",
			pattern: "*.go",
			want:    []string{"a.go"},
		},
		{
			name:    "recursive go",
			pattern: "**/*.go",
			want:    []string{"a.go", "sub/b.go", "sub/deep/c.go"},
		},
		{
			name:    "single subdir go",
			pattern: "sub/*.go",
			want:    []string{"sub/b.go"},
		},
		{
			name:    "recursive deep go",
			pattern: "**/deep/*.go",
			want:    []string{"sub/deep/c.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := run(t, root, glob.Input{Pattern: tc.pattern})
			if res.IsError {
				t.Fatalf("unexpected IsError; content=%q", res.Content)
			}
			if diff := cmp.Diff(tc.want, lines(res)); diff != "" {
				t.Errorf("matches mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExecute_GitSkipped(t *testing.T) {
	root := buildTree(t)
	// "**/x" would match .git/x if .git were not skipped.
	res := run(t, root, glob.Input{Pattern: "**/x"})
	if res.IsError {
		t.Fatalf("unexpected IsError; content=%q", res.Content)
	}
	for _, m := range lines(res) {
		if strings.HasPrefix(m, ".git/") {
			t.Errorf("expected .git to be skipped, but matched %q", m)
		}
	}
}

func TestExecute_NoMatchMessage(t *testing.T) {
	root := buildTree(t)
	res := run(t, root, glob.Input{Pattern: "*.rs"})
	if res.IsError {
		t.Fatalf("no-match should not be an error; content=%q", res.Content)
	}
	if res.Content == "" || !strings.Contains(res.Content, "no files match") {
		t.Errorf("expected a clear no-match message, got %q", res.Content)
	}
}

func TestExecute_BadPattern(t *testing.T) {
	root := buildTree(t)
	// Unterminated character class is a malformed pattern.
	res := run(t, root, glob.Input{Pattern: "[a-"})
	if !res.IsError {
		t.Fatalf("expected IsError for malformed pattern, got content=%q", res.Content)
	}
}

func TestExecute_EmptyPattern(t *testing.T) {
	root := buildTree(t)
	res := run(t, root, glob.Input{Pattern: ""})
	if !res.IsError {
		t.Fatalf("expected IsError for empty pattern, got content=%q", res.Content)
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	_, err := glob.New().Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected a hard error for invalid JSON, got nil")
	}
}

func TestExecute_PathOption(t *testing.T) {
	root := buildTree(t)
	// Relative Path resolves against WorkDir; search scoped to sub/.
	res := run(t, root, glob.Input{Pattern: "*.go", Path: "sub"})
	if res.IsError {
		t.Fatalf("unexpected IsError; content=%q", res.Content)
	}
	if diff := cmp.Diff([]string{"b.go"}, lines(res)); diff != "" {
		t.Errorf("matches mismatch (-want +got):\n%s", diff)
	}
}

func TestDeclarations(t *testing.T) {
	tl := glob.New()
	if got := tl.Name(); got != "Glob" {
		t.Errorf("Name() = %q, want %q", got, "Glob")
	}
	if !tl.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	if !tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = false, want true")
	}
	modes := []perm.Mode{
		perm.ModeDefault,
		perm.ModePlan,
		perm.ModeAcceptEdits,
		perm.ModeBypassPermissions,
	}
	for _, m := range modes {
		if got := tl.CheckPermission(context.Background(), nil, m); got != perm.DecisionAllow {
			t.Errorf("CheckPermission(mode=%d) = %d, want DecisionAllow", m, got)
		}
	}
}
