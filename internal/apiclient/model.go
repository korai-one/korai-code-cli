package apiclient

import "sync"

// ModelSelector holds the active model identifier and is safe for concurrent
// use. The engine reads it when building each request; the /model command
// writes it from the UI goroutine, so access is mutex-guarded.
type ModelSelector struct {
	mu    sync.RWMutex
	model string
}

// NewModelSelector returns a selector initialized to model.
func NewModelSelector(model string) *ModelSelector {
	return &ModelSelector{model: model}
}

// Get returns the current model.
func (s *ModelSelector) Get() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

// Set updates the current model.
func (s *ModelSelector) Set(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = model
}
