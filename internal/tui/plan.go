package tui

import (
	"context"

	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
)

// planReply is the user's resolution of a plan-approval request.
type planReply struct {
	decision plantool.Decision
	feedback string // relayed to the model on Reject
}

// planRequest pairs a proposed plan with the channel its decision is delivered on.
type planRequest struct {
	plan  string
	reply chan planReply
}

// PlanApprover is the interactive plan.Approver for the TUI. The engine
// goroutine calls ApprovePlan synchronously; it hands the plan to the UI over an
// unbuffered channel and blocks until the model replies (or ctx is cancelled) —
// the same bridge pattern as the permission Asker.
type PlanApprover struct {
	requests chan planRequest
}

// NewPlanApprover creates an interactive plan approver.
func NewPlanApprover() *PlanApprover {
	return &PlanApprover{requests: make(chan planRequest)}
}

// ApprovePlan implements plan.Approver. It blocks until the UI resolves the plan,
// returning the chosen decision and any feedback the user typed.
func (a *PlanApprover) ApprovePlan(ctx context.Context, plan string) (plantool.Decision, string, error) {
	reply := make(chan planReply, 1)
	select {
	case a.requests <- planRequest{plan: plan, reply: reply}:
	case <-ctx.Done():
		return plantool.Reject, "", ctx.Err()
	}
	select {
	case r := <-reply:
		return r.decision, r.feedback, nil
	case <-ctx.Done():
		return plantool.Reject, "", ctx.Err()
	}
}
