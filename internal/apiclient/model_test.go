package apiclient_test

import (
	"sync"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

func TestModelSelector(t *testing.T) {
	t.Parallel()
	s := apiclient.NewModelSelector("a")
	if s.Get() != "a" {
		t.Errorf("Get = %q, want a", s.Get())
	}
	s.Set("b")
	if s.Get() != "b" {
		t.Errorf("Get = %q, want b", s.Get())
	}
}

// TestModelSelectorConcurrent exercises the mutex under the race detector.
func TestModelSelectorConcurrent(t *testing.T) {
	t.Parallel()
	s := apiclient.NewModelSelector("start")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Set("x") }()
		go func() { defer wg.Done(); _ = s.Get() }()
	}
	wg.Wait()
}
