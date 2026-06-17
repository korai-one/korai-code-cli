package edit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/edit"
)

// writeFile creates a file with the given content under dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	et := edit.New()
	if got := et.Name(); got != "Edit" {
		t.Errorf("Name() = %q, want %q", got, "Edit")
	}
	if et.ReadOnly() {
		t.Error("ReadOnly() = true, want false")
	}
	if et.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = true, want false")
	}
	if et.Description(context.Background()) == "" {
		t.Error("Description() is empty")
	}
	if et.InputSchema() == nil {
		t.Error("InputSchema() is nil")
	}
}

func TestCheckPermission(t *testing.T) {
	t.Parallel()

	et := edit.New()
	cases := []struct {
		mode perm.Mode
		want perm.Decision
	}{
		{perm.ModeBypassPermissions, perm.DecisionAllow},
		{perm.ModeAcceptEdits, perm.DecisionAllow},
		{perm.ModePlan, perm.DecisionDeny},
		{perm.ModeDefault, perm.DecisionAsk},
	}
	for _, c := range cases {
		if got := et.CheckPermission(context.Background(), nil, c.mode); got != c.want {
			t.Errorf("CheckPermission(mode=%d) = %d, want %d", c.mode, got, c.want)
		}
	}
}

func TestExecuteSingleReplace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "hello world")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "world", NewString: "there"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if diff := cmp.Diff("hello there", string(got)); diff != "" {
		t.Errorf("file content mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteReplaceAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a a")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "a", NewString: "b", ReplaceAll: true})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if diff := cmp.Diff("b b b", string(got)); diff != "" {
		t.Errorf("file content mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteIdenticalStrings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "same")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "x", NewString: "x"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for identical strings")
	}
}

func TestExecuteMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "nope.txt", OldString: "a", NewString: "b"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for missing file")
	}
}

func TestExecuteNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "hello")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "missing", NewString: "b"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when old_string not found")
	}
}

func TestExecuteNonUniqueWithoutReplaceAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "a", NewString: "b"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for non-unique old_string without replace_all")
	}

	// File must be unchanged.
	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if diff := cmp.Diff("a a", string(got)); diff != "" {
		t.Errorf("file should be unchanged (-want +got):\n%s", diff)
	}
}

func TestExecuteNonUniqueWithReplaceAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a")

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "a", NewString: "b", ReplaceAll: true})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if diff := cmp.Diff("b b", string(got)); diff != "" {
		t.Errorf("file content mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteEmptyPath(t *testing.T) {
	t.Parallel()

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "", OldString: "a", NewString: "b"})

	res, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for empty path")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	et := edit.New()
	_, err := et.Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Error("expected hard error for invalid JSON")
	}
}

func TestExecutePreservesPerms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	et := edit.New()
	in, _ := json.Marshal(edit.Input{Path: "f.txt", OldString: "hello", NewString: "bye"})

	if _, err := et.Execute(context.Background(), in, tool.Deps{WorkDir: dir}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("perms = %o, want %o", got, 0o600)
	}
}
