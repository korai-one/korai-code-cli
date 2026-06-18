package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/agent"
)

// fakeSpawner is a hermetic Spawner used by the tests. It records the prompt it
// is called with and returns a configured output or error.
type fakeSpawner struct {
	out          string
	err          error
	gotPrompt    string
	called       bool
	checkContext bool
}

func (f *fakeSpawner) Spawn(ctx context.Context, prompt string) (string, error) {
	f.called = true
	f.gotPrompt = prompt
	if f.checkContext {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	return f.out, f.err
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling input: %v", err)
	}
	return b
}

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()

	fs := &fakeSpawner{out: "done"}
	tl := agent.New(fs)
	raw := mustMarshal(t, agent.Input{Description: "do a thing", Prompt: "the full instruction"})

	got, err := tl.Execute(context.Background(), raw, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	want := tool.Result{Content: "done"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
	if !fs.called {
		t.Error("spawner was not called")
	}
	if fs.gotPrompt != "the full instruction" {
		t.Errorf("spawner got prompt %q, want %q", fs.gotPrompt, "the full instruction")
	}
}

func TestExecuteEmptyPrompt(t *testing.T) {
	t.Parallel()

	fs := &fakeSpawner{out: "should not be used"}
	tl := agent.New(fs)
	raw := mustMarshal(t, agent.Input{Description: "no prompt", Prompt: ""})

	got, err := tl.Execute(context.Background(), raw, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !got.IsError {
		t.Errorf("expected IsError result, got %+v", got)
	}
	if fs.called {
		t.Error("spawner should not be called for an empty prompt")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	t.Parallel()

	fs := &fakeSpawner{}
	tl := agent.New(fs)

	got, err := tl.Execute(context.Background(), json.RawMessage("{not json"), tool.Deps{})
	if err == nil {
		t.Fatalf("expected a hard error for invalid JSON, got result %+v", got)
	}
	if got != (tool.Result{}) {
		t.Errorf("expected zero result on hard error, got %+v", got)
	}
	if fs.called {
		t.Error("spawner should not be called on invalid JSON")
	}
}

func TestExecuteSpawnerError(t *testing.T) {
	t.Parallel()

	fs := &fakeSpawner{err: errors.New("backend exploded")}
	tl := agent.New(fs)
	raw := mustMarshal(t, agent.Input{Description: "boom", Prompt: "do it"})

	got, err := tl.Execute(context.Background(), raw, tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned unexpected hard error: %v", err)
	}
	if !got.IsError {
		t.Errorf("expected IsError result, got %+v", got)
	}
	if want := "backend exploded"; !strings.Contains(got.Content, want) {
		t.Errorf("result content %q does not contain %q", got.Content, want)
	}
}

func TestExecuteContextCancelled(t *testing.T) {
	t.Parallel()

	fs := &fakeSpawner{out: "should not be used", checkContext: true}
	tl := agent.New(fs)
	raw := mustMarshal(t, agent.Input{Description: "cancel", Prompt: "do it"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := tl.Execute(ctx, raw, tool.Deps{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got err=%v result=%+v", err, got)
	}
	if fs.called {
		t.Error("spawner should not be called when context is already cancelled")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	tl := agent.New(&fakeSpawner{})

	if got := tl.Name(); got != "Task" {
		t.Errorf("Name() = %q, want %q", got, "Task")
	}
	if tl.ReadOnly() {
		t.Error("ReadOnly() = true, want false")
	}
	if tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = true, want false")
	}
	if tl.Description(context.Background()) == "" {
		t.Error("Description() is empty")
	}
	if tl.InputSchema() == nil {
		t.Error("InputSchema() is nil")
	}

	if got := tl.CheckPermission(context.Background(), nil, perm.ModeDefault); got != perm.DecisionAsk {
		t.Errorf("CheckPermission(default) = %v, want %v", got, perm.DecisionAsk)
	}
	if got := tl.CheckPermission(context.Background(), nil, perm.ModeBypassPermissions); got != perm.DecisionAllow {
		t.Errorf("CheckPermission(bypass) = %v, want %v", got, perm.DecisionAllow)
	}
}
