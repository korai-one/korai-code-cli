// Package plan implements the ExitPlanMode tool: while in plan mode the agent
// investigates read-only, then calls ExitPlanMode to present a plan for the
// user to approve. On approval the session leaves plan mode and execution
// proceeds; on rejection the agent stays in plan mode and revises.
package plan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Approver presents a plan to the user and reports whether it was approved.
// The TUI implements this interactively; headless runs use a policy.
type Approver interface {
	ApprovePlan(ctx context.Context, plan string) (approved bool, err error)
}

// Input is the structured input for ExitPlanMode.
type Input struct {
	// Plan is the proposed plan, presented to the user for approval.
	Plan string `json:"plan" jsonschema:"required,description=The concrete plan you intend to carry out, for the user to approve"`
}

// Tool implements tool.Tool for exiting plan mode with an approved plan.
type Tool struct {
	modes    *perm.ModeSelector
	approver Approver
}

// New returns an ExitPlanMode tool bound to the shared mode selector and an
// approver. On approval it switches the session to acceptEdits so the agent can
// carry out the plan.
func New(modes *perm.ModeSelector, approver Approver) *Tool {
	return &Tool{modes: modes, approver: approver}
}

// Name returns "ExitPlanMode".
func (t *Tool) Name() string { return "ExitPlanMode" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(context.Context) string {
	return "Present your plan for approval and, if approved, leave plan mode to " +
		"carry it out. Only call this while in plan mode, after investigating."
}

// InputSchema returns the JSON schema for ExitPlanMode's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema { return tool.Schema[Input]() }

// ReadOnly is false: approval changes the session's permission mode.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe is false (it mutates session mode).
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission always allows the call to reach Execute; the Approver is the
// real gate, presenting the plan and deciding the outcome.
func (t *Tool) CheckPermission(context.Context, json.RawMessage, perm.Mode) perm.Decision {
	return perm.DecisionAllow
}

// Execute presents the plan for approval. On approval it switches the session
// out of plan mode (to acceptEdits) and reports success; on rejection it leaves
// the mode unchanged and tells the agent to revise.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("exitplanmode: invalid input: %w", err)
	}
	if in.Plan == "" {
		return tool.Result{Content: "plan is required", IsError: true}, nil
	}
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	approved, err := t.approver.ApprovePlan(ctx, in.Plan)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("plan approval failed: %v", err), IsError: true}, nil
	}
	if !approved {
		return tool.Result{Content: "The user rejected the plan. Revise it and call ExitPlanMode again, or ask for clarification. You remain in plan mode."}, nil
	}

	t.modes.Set(perm.ModeAcceptEdits)
	return tool.Result{Content: "Plan approved. You have left plan mode (acceptEdits); carry out the plan now."}, nil
}
