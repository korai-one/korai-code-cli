package apiclient

import (
	"context"
	"fmt"
	"sync"
)

// WorkerMode names an inference locality: a co-located or LAN worker ("local"),
// or the networked backend ("remote").
type WorkerMode string

// The two inference localities a ClientSelector switches between.
const (
	// WorkerLocal routes inference to a co-located or LAN worker.
	WorkerLocal WorkerMode = "local"
	// WorkerRemote routes inference to the networked backend.
	WorkerRemote WorkerMode = "remote"
)

// ClientSelector is a Client that forwards Complete to one of two underlying
// backends — a local worker and a remote (networked) backend — and lets the
// active one be switched at runtime (the /worker_mode command). Either backend
// may be absent (nil) when it could not be constructed for the session; SetMode
// refuses to switch to an absent one. It is safe for concurrent use: the engine
// reads the active client per turn while the UI goroutine may switch it, so
// access is mutex-guarded (the same pattern as ModelSelector).
type ClientSelector struct {
	mu     sync.RWMutex
	active WorkerMode
	local  Client
	remote Client
}

// NewClientSelector returns a selector starting in active mode. local and remote
// may be nil when the corresponding backend is unavailable for the session; the
// caller must ensure the initial active mode's client is non-nil.
func NewClientSelector(active WorkerMode, local, remote Client) *ClientSelector {
	return &ClientSelector{active: active, local: local, remote: remote}
}

// Complete forwards the request to the active backend. It returns an error when
// the active backend is not configured (which SetMode prevents switching into).
func (s *ClientSelector) Complete(ctx context.Context, req Request) (<-chan Event, error) {
	s.mu.RLock()
	c := s.local
	if s.active == WorkerRemote {
		c = s.remote
	}
	s.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("no %s inference backend configured", s.active)
	}
	return c.Complete(ctx, req)
}

// Mode returns the active mode as a string ("local" or "remote").
func (s *ClientSelector) Mode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(s.active)
}

// SetMode switches the active backend. It returns an error when mode is not a
// known worker mode or the requested backend is not configured for this session.
func (s *ClientSelector) SetMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch WorkerMode(mode) {
	case WorkerLocal:
		if s.local == nil {
			return fmt.Errorf("no local worker available (start one, or pass --local-worker-addr / set KORAI_LOCAL_WORKER_ADDR)")
		}
		s.active = WorkerLocal
	case WorkerRemote:
		if s.remote == nil {
			return fmt.Errorf("no remote backend available (set KORAI_API_KEY)")
		}
		s.active = WorkerRemote
	default:
		return fmt.Errorf("unknown worker mode %q (want local or remote)", mode)
	}
	return nil
}

// Available returns the modes that have a configured backend, in a stable order
// (local before remote), so the /worker_mode listing is deterministic.
func (s *ClientSelector) Available() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	if s.local != nil {
		out = append(out, string(WorkerLocal))
	}
	if s.remote != nil {
		out = append(out, string(WorkerRemote))
	}
	return out
}
