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

func TestExecuteFact(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)
	in, _ := json.Marshal(memtool.Input{Note: "neovim", Kind: "fact", Key: "editor"})

	res, err := rt.Execute(context.Background(), in, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	f, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Facts) != 1 || f.Facts[0].Key != "editor" || f.Facts[0].Value != "neovim" {
		t.Errorf("facts = %+v, want editor: neovim", f.Facts)
	}

	// A bare key (no kind) is enough to imply a fact, and setting the same
	// key supersedes the value.
	in, _ = json.Marshal(memtool.Input{Note: "helix", Key: "editor"})
	if res, err = rt.Execute(context.Background(), in, tool.Deps{}); err != nil || res.IsError {
		t.Fatalf("Execute supersede: %v / %s", err, res.Content)
	}
	f, _ = st.Load()
	if len(f.Facts) != 1 || f.Facts[0].Value != "helix" {
		t.Errorf("facts after supersede = %+v, want a single editor: helix", f.Facts)
	}
}

func TestExecuteKindValidation(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)

	cases := []memtool.Input{
		{Note: "v", Kind: "fact"},            // fact without key
		{Note: "v", Kind: "note", Key: "k"},  // key on a note
		{Note: "v", Kind: "banana", Key: ""}, // unknown kind
	}
	for _, in := range cases {
		raw, _ := json.Marshal(in)
		res, err := rt.Execute(context.Background(), raw, tool.Deps{})
		if err != nil {
			t.Fatalf("Execute(%+v): %v", in, err)
		}
		if !res.IsError {
			t.Errorf("Execute(%+v) succeeded, want soft error", in)
		}
	}
}

func TestExecuteTurnCapSoftError(t *testing.T) {
	t.Parallel()

	st := store.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	rt := memtool.New(st)
	for i := 0; i < store.MaxNoteWritesPerTurn; i++ {
		raw, _ := json.Marshal(memtool.Input{Note: "note " + string(rune('a'+i))})
		if res, err := rt.Execute(context.Background(), raw, tool.Deps{}); err != nil || res.IsError {
			t.Fatalf("Execute %d: %v / %s", i, err, res.Content)
		}
	}
	raw, _ := json.Marshal(memtool.Input{Note: "one too many"})
	res, err := rt.Execute(context.Background(), raw, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute over cap: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "cap") {
		t.Errorf("over-cap result = %+v, want soft cap error", res)
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
