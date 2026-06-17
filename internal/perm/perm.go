// Package perm defines the permission decision types and the resolution engine
// threaded through the agent loop. Values are passed explicitly; there are no
// package-level globals.
package perm

import "fmt"

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

// String returns the canonical name of the mode.
func (m Mode) String() string {
	switch m {
	case ModeDefault:
		return "default"
	case ModePlan:
		return "plan"
	case ModeAcceptEdits:
		return "acceptEdits"
	case ModeBypassPermissions:
		return "bypassPermissions"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// ParseMode converts a mode name to a Mode. It is the inverse of Mode.String.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "default":
		return ModeDefault, nil
	case "plan":
		return ModePlan, nil
	case "acceptEdits":
		return ModeAcceptEdits, nil
	case "bypassPermissions":
		return ModeBypassPermissions, nil
	default:
		return ModeDefault, fmt.Errorf("unknown permission mode %q", s)
	}
}

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
