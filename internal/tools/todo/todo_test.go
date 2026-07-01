package todo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	store "github.com/Nevaero/korai-code-cli/internal/todo"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	todotool "github.com/Nevaero/korai-code-cli/internal/tools/todo"
)

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()

	list := &store.List{}
	rt := todotool.New(list)
	in, _ := json.Marshal(todotool.Input{Todos: []todotool.Entry{
		{Content: "Write code", Status: "completed"},
		{Content: "Run tests", Status: "in_progress", ActiveForm: "Running tests"},
		{Content: "Ship it", Status: "pending"},
	}})

	res, err := rt.Execute(context.Background(), in, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	want := "[x] Write code\n[~] Running tests\n[ ] Ship it"
	if res.Content != want {
		t.Errorf("rendered result mismatch:\ngot:\n%s\nwant:\n%s", res.Content, want)
	}

	gotItems := list.Items()
	wantItems := []store.Item{
		{Content: "Write code", Status: store.StatusCompleted},
		{Content: "Run tests", Status: store.StatusInProgress, ActiveForm: "Running tests"},
		{Content: "Ship it", Status: store.StatusPending},
	}
	if diff := cmp.Diff(wantItems, gotItems); diff != "" {
		t.Errorf("list contents mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteInvalidStatus(t *testing.T) {
	t.Parallel()

	list := &store.List{}
	rt := todotool.New(list)
	in, _ := json.Marshal(todotool.Input{Todos: []todotool.Entry{
		{Content: "Do a thing", Status: "done"},
	}})

	res, err := rt.Execute(context.Background(), in, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for invalid status")
	}
	if len(list.Items()) != 0 {
		t.Error("list must not be modified when a status is invalid")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	rt := todotool.New(&store.List{})

	_, err := rt.Execute(context.Background(), json.RawMessage(`{bad`), tool.Deps{})
	if err == nil {
		t.Fatal("expected hard error for invalid JSON input")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	rt := todotool.New(&store.List{})

	if rt.Name() != "TodoWrite" {
		t.Errorf("Name = %q, want TodoWrite", rt.Name())
	}
	if rt.ReadOnly() {
		t.Error("TodoWrite should not be ReadOnly")
	}
	if rt.ConcurrencySafe() {
		t.Error("TodoWrite should not be ConcurrencySafe")
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

	rt := todotool.New(&store.List{})
	if rt.InputSchema() == nil {
		t.Error("InputSchema returned nil")
	}
}
