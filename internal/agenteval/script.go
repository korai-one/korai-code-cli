package agenteval

import (
	"context"
	"fmt"
	"sync"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// scriptClient implements apiclient.Client by replaying scripted turns keyed by
// call index. A call past the end of the script returns an ErrorEvent so the
// engine terminates and the scenario fails loudly instead of hanging or
// panicking.
type scriptClient struct {
	turns []Turn

	mu   sync.Mutex
	call int
}

// Complete replays the next scripted turn as a closed, pre-filled channel.
func (s *scriptClient) Complete(_ context.Context, _ apiclient.Request) (<-chan apiclient.Event, error) {
	s.mu.Lock()
	n := s.call
	s.call++
	s.mu.Unlock()

	var events []apiclient.Event
	if n < len(s.turns) {
		events = s.turns[n].events()
	} else {
		events = []apiclient.Event{apiclient.ErrorEvent{
			Err: fmt.Errorf("agenteval: script exhausted: model call %d beyond %d scripted turns", n+1, len(s.turns)),
		}}
	}
	ch := make(chan apiclient.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// recordingClient wraps any apiclient.Client and records every request it
// forwards, so the harness can derive request-side metrics (retries, wrap-ups,
// sampling) for scripted and live backends alike.
type recordingClient struct {
	inner apiclient.Client

	mu   sync.Mutex
	reqs []apiclient.Request
}

// Complete records req and forwards to the wrapped client.
func (r *recordingClient) Complete(ctx context.Context, req apiclient.Request) (<-chan apiclient.Event, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.inner.Complete(ctx, req)
}

// requests returns a copy of the recorded requests in order.
func (r *recordingClient) requests() []apiclient.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]apiclient.Request, len(r.reqs))
	copy(out, r.reqs)
	return out
}
