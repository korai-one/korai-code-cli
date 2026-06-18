package perm

import "context"

// Outcome is the final, resolved result of a permission request.
type Outcome int

const (
	// OutcomeAllowed means the tool call may proceed.
	OutcomeAllowed Outcome = iota
	// OutcomeDenied means the tool call must not run.
	OutcomeDenied
)

// Engine resolves permission requests against the active mode, explicit rules,
// and an Asker. The mode lives in a ModeSelector so it can change at runtime
// (e.g. /plan or shift+tab) and be shared with the UI. Safe to share across
// goroutines (the Asker must be concurrency-safe if used concurrently).
type Engine struct {
	modes *ModeSelector
	rules Rules
	asker Asker
}

// NewEngine builds a permission engine. A nil asker defaults to DenyAsker so
// resolution is fail-closed; a nil selector defaults to ModeDefault.
func NewEngine(modes *ModeSelector, rules Rules, asker Asker) *Engine {
	if asker == nil {
		asker = DenyAsker{}
	}
	if modes == nil {
		modes = NewModeSelector(ModeDefault)
	}
	return &Engine{modes: modes, rules: rules, asker: asker}
}

// Mode returns the active permission mode. The caller passes this to a tool's
// CheckPermission to obtain the base decision for a Request.
func (e *Engine) Mode() Mode { return e.modes.Get() }

// Resolve decides whether a tool call may proceed. Resolution order (fail-closed):
//
//  1. ModeBypassPermissions short-circuits to allowed.
//  2. A matching deny rule forces denial.
//  3. The tool's base decision: Deny -> denied, Allow -> allowed.
//  4. Base Ask: a matching allow rule allows; otherwise the Asker decides.
func (e *Engine) Resolve(ctx context.Context, req Request) (Outcome, error) {
	if e.modes.Get() == ModeBypassPermissions {
		return OutcomeAllowed, nil
	}
	if e.rules.DeniesTool(req.ToolName) {
		return OutcomeDenied, nil
	}

	switch req.Base {
	case DecisionDeny:
		return OutcomeDenied, nil
	case DecisionAllow:
		return OutcomeAllowed, nil
	case DecisionAsk:
		if e.rules.AllowsTool(req.ToolName) {
			return OutcomeAllowed, nil
		}
		decision, err := e.asker.Ask(ctx, req)
		if err != nil {
			return OutcomeDenied, err
		}
		if decision == DecisionAllow {
			return OutcomeAllowed, nil
		}
		return OutcomeDenied, nil
	default:
		// Unknown decision: fail closed.
		return OutcomeDenied, nil
	}
}
