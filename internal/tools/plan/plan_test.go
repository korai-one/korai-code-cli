package plan_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
)

type fakeApprover struct {
	approve bool
	err     error
	gotPlan string
}

func (a *fakeApprover) ApprovePlan(_ context.Context, p string) (bool, error) {
	a.gotPlan = p
	return a.approve, a.err
}

func input(t *testing.T, plan string) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(plantool.Input{Plan: plan})
	return raw
}

func TestApproveSwitchesOutOfPlanMode(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModePlan)
	ap := &fakeApprover{approve: true}
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
		t.Errorf("mode = %v, want acceptEdits after approval", modes.Get())
	}
}

func TestRejectKeepsPlanMode(t *testing.T) {
	t.Parallel()
	modes := perm.NewModeSelector(perm.ModePlan)
	tl := plantool.New(modes, &fakeApprover{approve: false})

	res, err := tl.Execute(context.Background(), input(t, "plan"), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("rejection should be non-error feedback, got error: %s", res.Content)
	}
	if modes.Get() != perm.ModePlan {
		t.Errorf("mode = %v, want plan retained after rejection", modes.Get())
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
	tl := plantool.New(perm.NewModeSelector(perm.ModePlan), &fakeApprover{approve: true})

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
