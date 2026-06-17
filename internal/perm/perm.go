// Package perm defines the permission decision types threaded through the engine.
// Values are passed explicitly; there are no package-level globals.
package perm

// Mode controls how the engine resolves tool permission prompts.
type Mode int

const (
	// ModeDefault asks the user before any mutating tool call.
	ModeDefault Mode = iota
	// ModePlan allows read-only tools silently; asks before any write.
	ModePlan
	// ModeAcceptEdits allows file edits silently; asks before shell execution.
	ModeAcceptEdits
	// ModeBypassPermissions allows everything without prompting. Use with care.
	ModeBypassPermissions
)

// Decision is what CheckPermission returns for a specific tool invocation.
type Decision int

const (
	// DecisionAllow proceeds without prompting.
	DecisionAllow Decision = iota
	// DecisionAsk pauses and surfaces a prompt to the user.
	DecisionAsk
	// DecisionDeny blocks execution and returns an error to the model.
	DecisionDeny
)
