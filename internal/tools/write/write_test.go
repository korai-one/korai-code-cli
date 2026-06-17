package write_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/write"
)

func TestExecuteWritesNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wt := write.New()
	in, _ := json.Marshal(write.Input{Path: "note.txt", Content: "hello"})

	res, err := wt.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if diff := cmp.Diff("hello", string(got)); diff != "" {
		t.Errorf("file contents mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteOverwritesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("old contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt := write.New()
	in, _ := json.Marshal(write.Input{Path: "note.txt", Content: "new"})

	res, err := wt.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if diff := cmp.Diff("new", string(got)); diff != "" {
		t.Errorf("file contents mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteCreatesNestedParents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wt := write.New()
	in, _ := json.Marshal(write.Input{Path: "a/b/c/note.txt", Content: "deep"})

	res, err := wt.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "note.txt"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if diff := cmp.Diff("deep", string(got)); diff != "" {
		t.Errorf("file contents mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteEmptyPath(t *testing.T) {
	t.Parallel()

	wt := write.New()
	in, _ := json.Marshal(write.Input{Path: "", Content: "x"})

	res, err := wt.Execute(context.Background(), in, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty path")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	wt := write.New()
	_, err := wt.Execute(context.Background(), json.RawMessage(`{bad`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected hard error for invalid JSON input")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	wt := write.New()
	if wt.Name() != "Write" {
		t.Errorf("Name = %q, want Write", wt.Name())
	}
	if wt.ReadOnly() {
		t.Error("Write should not be ReadOnly")
	}
	if wt.ConcurrencySafe() {
		t.Error("Write should not be ConcurrencySafe")
	}
}

func TestCheckPermission(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mode perm.Mode
		want perm.Decision
	}{
		{"bypass", perm.ModeBypassPermissions, perm.DecisionAllow},
		{"acceptEdits", perm.ModeAcceptEdits, perm.DecisionAllow},
		{"plan", perm.ModePlan, perm.DecisionDeny},
		{"default", perm.ModeDefault, perm.DecisionAsk},
	}

	wt := write.New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := wt.CheckPermission(context.Background(), nil, tc.mode); got != tc.want {
				t.Errorf("CheckPermission(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
