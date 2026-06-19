package perm

import "sync"

// ModeSelector holds the active permission mode and is safe for concurrent use.
// The permission engine reads it on every resolution; the /plan command and the
// TUI's shift+tab handler write it, so access is mutex-guarded.
type ModeSelector struct {
	mu      sync.RWMutex
	mode    Mode
	prePlan Mode // mode active immediately before plan mode was entered
}

// NewModeSelector returns a selector initialized to mode.
func NewModeSelector(mode Mode) *ModeSelector {
	return &ModeSelector{mode: mode}
}

// Get returns the current mode.
func (s *ModeSelector) Get() Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// Set updates the current mode. Entering plan mode from another mode records the
// previous mode (see PrePlan), so it can be restored when the plan is approved.
func (s *ModeSelector) Set(mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == ModePlan && s.mode != ModePlan {
		s.prePlan = s.mode
	}
	s.mode = mode
}

// PrePlan returns the mode that was active before plan mode was last entered, so
// approving a plan can restore where the user left off. It is ModeDefault until
// plan mode is entered.
func (s *ModeSelector) PrePlan() Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prePlan
}

// cycleOrder is the interactive shift+tab rotation. bypassPermissions is left
// out deliberately — it is reachable only via flag or config, never by an
// accidental keypress.
var cycleOrder = []Mode{ModeDefault, ModeAcceptEdits, ModePlan}

// Cycle advances to the next mode in the interactive rotation
// (default → acceptEdits → plan → default) and returns it. A mode outside the
// rotation (e.g. bypassPermissions) cycles to default.
func (s *ModeSelector) Cycle() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := ModeDefault
	for i, m := range cycleOrder {
		if m == s.mode {
			next = cycleOrder[(i+1)%len(cycleOrder)]
			break
		}
	}
	if next == ModePlan && s.mode != ModePlan {
		s.prePlan = s.mode
	}
	s.mode = next
	return next
}
