package readfile_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
)

func TestExecuteReadsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := readfile.New()
	in, _ := json.Marshal(readfile.Input{Path: "note.txt"})

	res, err := rt.Execute(context.Background(), in, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if res.Content != "contents" {
		t.Errorf("content = %q, want %q", res.Content, "contents")
	}
}

func TestExecuteMissingFile(t *testing.T) {
	t.Parallel()

	rt := readfile.New()
	in, _ := json.Marshal(readfile.Input{Path: "does-not-exist.txt"})

	res, err := rt.Execute(context.Background(), in, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute returned hard error, want soft error result: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for missing file")
	}
}

func TestExecuteEmptyPath(t *testing.T) {
	t.Parallel()

	rt := readfile.New()
	in, _ := json.Marshal(readfile.Input{Path: ""})

	res, err := rt.Execute(context.Background(), in, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty path")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	rt := readfile.New()
	_, err := rt.Execute(context.Background(), json.RawMessage(`{bad`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected hard error for invalid JSON input")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	rt := readfile.New()
	if rt.Name() != "ReadFile" {
		t.Errorf("Name = %q, want ReadFile", rt.Name())
	}
	if !rt.ReadOnly() {
		t.Error("ReadFile should be ReadOnly")
	}
	if !rt.ConcurrencySafe() {
		t.Error("ReadFile should be ConcurrencySafe")
	}
	if d := rt.CheckPermission(context.Background(), nil, perm.ModeDefault); d != perm.DecisionAllow {
		t.Errorf("CheckPermission = %v, want allow", d)
	}
}
