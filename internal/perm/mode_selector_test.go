package perm_test

import (
	"sync"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
)

func TestModeSelectorGetSet(t *testing.T) {
	t.Parallel()
	s := perm.NewModeSelector(perm.ModeDefault)
	if s.Get() != perm.ModeDefault {
		t.Errorf("Get = %v, want default", s.Get())
	}
	s.Set(perm.ModePlan)
	if s.Get() != perm.ModePlan {
		t.Errorf("Get = %v, want plan", s.Get())
	}
}

func TestModeSelectorCycle(t *testing.T) {
	t.Parallel()
	s := perm.NewModeSelector(perm.ModeDefault)
	// default → acceptEdits → plan → default
	for _, want := range []perm.Mode{perm.ModeAcceptEdits, perm.ModePlan, perm.ModeDefault} {
		if got := s.Cycle(); got != want {
			t.Errorf("Cycle() = %v, want %v", got, want)
		}
	}
}

func TestModeSelectorCycleFromBypass(t *testing.T) {
	t.Parallel()
	s := perm.NewModeSelector(perm.ModeBypassPermissions)
	// A mode outside the rotation cycles back to default.
	if got := s.Cycle(); got != perm.ModeDefault {
		t.Errorf("Cycle() from bypass = %v, want default", got)
	}
}

func TestModeSelectorConcurrent(t *testing.T) {
	t.Parallel()
	s := perm.NewModeSelector(perm.ModeDefault)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Cycle() }()
		go func() { defer wg.Done(); _ = s.Get() }()
	}
	wg.Wait()
}
