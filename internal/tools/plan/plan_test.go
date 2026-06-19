package plan_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
)

type fakeApprover struct {
	decision plantool.Decision
	feedback string
	err      error
	gotPlan  string
}

func (a *fakeApprover) ApprovePlan(_ context.Context, p string) (plantool.Decision, string, error) {
	a.gotPlan = p
	return a.decision, a.feedback, a.err
}

func input(t *testing.T, plan string) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(plantool.Input{Plan: plan})
	return raw
}

func TestApproveRestoresPrePlanMode(t *testing.T) {
	t.Parallel()
	// User was in acceptEdits, then entered plan mode; approval restores it.
	modes := perm.NewModeSelector(perm.ModeAcceptEdits)
	modes.Set(perm.ModePlan)
	ap := &fakeApprover{decision: plantool.Approve}
	tl := plantool.New(modes, ap)

	res, err := tl.Execute(context.Background(), input(t, "do X then Y"), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if ap.gotPlan != "do X then Y" {
		t.Errorf("approver got plan %q", ap.gotPlan)
	}
	if modes.Get() != perm.ModeAcceptEdits {
		t.Errorf("mode = %v, want acceptEdits restored after approval", modes.Get())
	}
}

func TestApproveRestoresDefault(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModeDefault)
	modes.Set(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{decision: plantool.Approve})

	if _, err := tl.Execute(context.Background(), input(t, "p"), tool.Deps{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if modes.Get() != perm.ModeDefault {
		t.Errorf("mode = %v, want default restored", modes.Get())
	}
}

func TestApproveAcceptEdits(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModeDefault)
	modes.Set(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{decision: plantool.ApproveAcceptEdits})

	if _, err := tl.Execute(context.Background(), input(t, "p"), tool.Deps{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if modes.Get() != perm.ModeAcceptEdits {
		t.Errorf("mode = %v, want acceptEdits", modes.Get())
	}
}

func TestRejectRelaysFeedback(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{decision: plantool.Reject, feedback: "use a worker pool"})

	res, err := tl.Execute(context.Background(), input(t, "plan"), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("rejection should be non-error feedback, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "use a worker pool") {
		t.Errorf("feedback not relayed to the model: %q", res.Content)
	}
	if modes.Get() != perm.ModePlan {
		t.Errorf("mode = %v, want plan retained after rejection", modes.Get())
	}
}

func TestApproveSavesPlanFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	modes := perm.NewModeSelector(perm.ModeDefault)
	modes.Set(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{decision: plantool.Approve})

	res, err := tl.Execute(context.Background(), input(t, "Build the thing\nstep 1\nstep 2"), tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	plansDir := filepath.Join(dir, ".korai", "plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one saved plan file, got %v (err %v)", entries, err)
	}
	if !strings.Contains(res.Content, ".korai/plans/") {
		t.Errorf("result should mention the saved plan path: %q", res.Content)
	}
	data, _ := os.ReadFile(filepath.Join(plansDir, entries[0].Name()))
	if !strings.Contains(string(data), "step 1") {
		t.Errorf("saved plan content = %q", data)
	}
}

func TestApproverError(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{err: errors.New("boom")})

	res, err := tl.Execute(context.Background(), input(t, "plan"), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("approver error should produce an error result")
	}
	if modes.Get() != perm.ModePlan {
		t.Error("mode must not change on approver error")
	}
}

func TestEmptyPlanAndBadInput(t *testing.T) {
	t.Parallel()
	tl := plantool.New(perm.NewModeSelector(perm.ModePlan), &fakeApprover{decision: plantool.Approve})

	res, err := tl.Execute(context.Background(), input(t, ""), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("empty plan should be a soft error")
	}

	if _, err := tl.Execute(context.Background(), json.RawMessage(`{bad`), tool.Deps{}); err == nil {
		t.Error("invalid JSON should be a hard error")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()
	tl := plantool.New(perm.NewModeSelector(perm.ModeDefault), &fakeApprover{})
	if tl.Name() != "ExitPlanMode" {
		t.Errorf("name = %q", tl.Name())
	}
	if tl.ReadOnly() || tl.ConcurrencySafe() {
		t.Error("ExitPlanMode must be fail-closed (not read-only/concurrency-safe)")
	}
	if d := tl.CheckPermission(context.Background(), nil, perm.ModePlan); d != perm.DecisionAllow {
		t.Errorf("CheckPermission = %v, want allow", d)
	}
}
