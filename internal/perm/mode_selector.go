package perm

import "sync"

// ModeSelector holds the active permission mode and is safe for concurrent use.
// The permission engine reads it on every resolution; the /plan command and the
// TUI's shift+tab handler write it, so access is mutex-guarded.
type ModeSelector struct {
	mu   sync.RWMutex
	mode Mode
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

// Set updates the current mode.
func (s *ModeSelector) Set(mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
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
	s.mode = next
	return next
}
