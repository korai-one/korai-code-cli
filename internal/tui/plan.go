package tui

import "context"

// planRequest pairs a proposed plan with the channel its decision is delivered on.
type planRequest struct {
	plan  string
	reply chan bool
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

// ApprovePlan implements plan.Approver. It blocks until the UI resolves the plan.
func (a *PlanApprover) ApprovePlan(ctx context.Context, plan string) (bool, error) {
	reply := make(chan bool, 1)
	select {
	case a.requests <- planRequest{plan: plan, reply: reply}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case ok := <-reply:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
