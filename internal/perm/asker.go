package perm

import "context"

// Request describes a single tool-permission question presented for resolution.
type Request struct {
	// ToolName is the name of the tool being invoked.
	ToolName string
	// Input is the raw JSON input the tool was called with.
	Input []byte
	// Base is the tool's own CheckPermission decision for the active mode.
	Base Decision
}

// Asker resolves an "ask" decision into a concrete allow or deny. The TUI
// implements this with an interactive prompt (Phase 4); headless runs use a
// non-interactive policy.
//
// Ask must return either DecisionAllow or DecisionDeny.
type Asker interface {
	Ask(ctx context.Context, req Request) (Decision, error)
}

// AskFunc adapts a function to the Asker interface.
type AskFunc func(ctx context.Context, req Request) (Decision, error)

// Ask calls the underlying function.
func (f AskFunc) Ask(ctx context.Context, req Request) (Decision, error) {
	return f(ctx, req)
}

// DenyAsker resolves every "ask" to a denial. It is the safe default for
// non-interactive (headless) runs: a tool that would prompt is blocked rather
// than silently allowed.
type DenyAsker struct{}

// Ask always denies.
func (DenyAsker) Ask(context.Context, Request) (Decision, error) {
	return DecisionDeny, nil
}

// AllowAsker resolves every "ask" to an allowance. Use only when the operator
// has explicitly opted into running without prompts.
type AllowAsker struct{}

// Ask always allows.
func (AllowAsker) Ask(context.Context, Request) (Decision, error) {
	return DecisionAllow, nil
}
