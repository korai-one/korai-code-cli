package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	store "github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
)

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)
	in, _ := json.Marshal(memtool.Input{Note: "remember the milk"})

	res, err := rt.Execute(context.Background(), in, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if res.Content != "remembered" {
		t.Errorf("content = %q, want %q", res.Content, "remembered")
	}

	got, err := st.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(got, "remember the milk") {
		t.Errorf("store contents %q missing appended note", got)
	}
}

func TestExecuteEmptyNote(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)
	in, _ := json.Marshal(memtool.Input{Note: ""})

	res, err := rt.Execute(context.Background(), in, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty note")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)

	_, err := rt.Execute(context.Background(), json.RawMessage(`{bad`), tool.Deps{})
	if err == nil {
		t.Fatal("expected hard error for invalid JSON input")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)

	if rt.Name() != "Remember" {
		t.Errorf("Name = %q, want Remember", rt.Name())
	}
	if rt.ReadOnly() {
		t.Error("Remember should not be ReadOnly")
	}
	if rt.ConcurrencySafe() {
		t.Error("Remember should not be ConcurrencySafe")
	}
	modes := []perm.Mode{
		perm.ModeDefault,
		perm.ModePlan,
		perm.ModeAcceptEdits,
		perm.ModeBypassPermissions,
	}
	for _, m := range modes {
		if d := rt.CheckPermission(context.Background(), nil, m); d != perm.DecisionAllow {
			t.Errorf("CheckPermission(%v) = %v, want allow", m, d)
		}
	}
}

func TestInputSchemaNonNil(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)
	if rt.InputSchema() == nil {
		t.Error("InputSchema returned nil")
	}
}
