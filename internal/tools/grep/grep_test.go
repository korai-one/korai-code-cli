package grep_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/grep"
)

// setupTree builds a hermetic directory tree under t.TempDir() and returns its
// root. It includes a subdirectory and a .git directory to confirm .git is
// skipped during the walk.
func setupTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"a.go":           "package main\nfunc Foo() {}\n// needle here\n",
		"b.go":           "package main\nvar x = 1\n",
		"c.txt":          "needle in text\nplain line\n",
		"sub/d.go":       "package sub\n// another needle\n",
		".git/config.go": "// needle inside git should be skipped\n",
		".git/HEAD":      "needle\n",
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// run executes the Grep tool with the given input against root and returns the
// Result and error.
func run(t *testing.T, root string, in grep.Input) (tool.Result, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return grep.New().Execute(context.Background(), raw, tool.Deps{WorkDir: root})
}

// lines splits Result.Content into a sorted slice for order-independent
// comparison (WalkDir order is deterministic, but sorting keeps assertions
// robust to tree layout).
func lines(content string) []string {
	if content == "" {
		return nil
	}
	out := strings.Split(content, "\n")
	sort.Strings(out)
	return out
}

func TestExecute_BasicMatchAcrossFiles(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "needle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError, content: %q", res.Content)
	}

	want := []string{
		"a.go:3:// needle here",
		"c.txt:1:needle in text",
		filepath.Join("sub", "d.go") + ":2:// another needle",
	}
	sort.Strings(want)

	if diff := cmp.Diff(want, lines(res.Content)); diff != "" {
		t.Errorf("matches mismatch (-want +got):\n%s", diff)
	}
}

func TestExecute_SkipsGitDirectory(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "needle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(res.Content, ".git") {
		t.Errorf(".git contents leaked into results:\n%s", res.Content)
	}
}

func TestExecute_GlobFiltering(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "needle", Glob: "*.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError, content: %q", res.Content)
	}

	want := []string{
		"a.go:3:// needle here",
		filepath.Join("sub", "d.go") + ":2:// another needle",
	}
	sort.Strings(want)

	if diff := cmp.Diff(want, lines(res.Content)); diff != "" {
		t.Errorf("glob-filtered matches mismatch (-want +got):\n%s", diff)
	}
}

func TestExecute_NoMatchMessage(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "zzz-not-present"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("zero matches must not be an error, content: %q", res.Content)
	}
	if res.Content != "no matches found" {
		t.Errorf("got %q, want %q", res.Content, "no matches found")
	}
}

func TestExecute_PathRelative(t *testing.T) {
	root := setupTree(t)
	// Search only the "sub" subtree via a relative Path against WorkDir.
	res, err := run(t, root, grep.Input{Pattern: "needle", Path: "sub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"d.go:2:// another needle"}
	if diff := cmp.Diff(want, lines(res.Content)); diff != "" {
		t.Errorf("relative-path matches mismatch (-want +got):\n%s", diff)
	}
}

func TestExecute_InvalidRegexIsSoftError(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "("})
	if err != nil {
		t.Fatalf("invalid regex must be a soft error, got hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for invalid regex, content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "invalid regular expression") {
		t.Errorf("content %q missing expected message", res.Content)
	}
}

func TestExecute_EmptyPatternIsSoftError(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: ""})
	if err != nil {
		t.Fatalf("empty pattern must be a soft error, got hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for empty pattern, content: %q", res.Content)
	}
}

func TestExecute_InvalidGlobIsSoftError(t *testing.T) {
	root := setupTree(t)
	res, err := run(t, root, grep.Input{Pattern: "needle", Glob: "[", Path: ""})
	if err != nil {
		t.Fatalf("invalid glob must be a soft error, got hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for invalid glob, content: %q", res.Content)
	}
}

func TestExecute_InvalidJSONIsHardError(t *testing.T) {
	res, err := grep.New().Execute(context.Background(), json.RawMessage("{not json"), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatalf("malformed JSON must be a hard error; got result: %+v", res)
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	root := setupTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	raw, _ := json.Marshal(grep.Input{Pattern: "needle"})
	_, err := grep.New().Execute(ctx, raw, tool.Deps{WorkDir: root})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got error %v, want context.Canceled", err)
	}
}

func TestDeclarations(t *testing.T) {
	tl := grep.New()

	if got := tl.Name(); got != "Grep" {
		t.Errorf("Name() = %q, want %q", got, "Grep")
	}
	if !tl.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	if !tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = false, want true")
	}
	if tl.InputSchema() == nil {
		t.Error("InputSchema() = nil, want non-nil")
	}
	if d := tl.Description(context.Background()); d == "" {
		t.Error("Description() is empty")
	}

	modes := []perm.Mode{perm.ModeDefault, perm.ModePlan, perm.ModeAcceptEdits, perm.ModeBypassPermissions}
	for _, m := range modes {
		if got := tl.CheckPermission(context.Background(), nil, m); got != perm.DecisionAllow {
			t.Errorf("CheckPermission(mode=%d) = %d, want DecisionAllow", m, got)
		}
	}
}
