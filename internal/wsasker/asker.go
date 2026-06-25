// Package wsasker implements perm.Asker for `korai serve`: instead of blocking
// on a terminal prompt (the TUI's Asker) or a fixed policy (the headless
// DenyAsker/AllowAsker), it asks the connected client. It emits a perm_req
// event over the WebSocket and blocks the engine goroutine until the client
// answers with a matching perm_res, which the serve read loop delivers via
// Resolve.
//
// Ask runs on the turn goroutine; Resolve runs on the read-loop goroutine. The
// two never share state except the pending map, which is mutex-guarded. The
// read loop must keep reading while a turn runs, or a perm_res can never arrive
// and Ask deadlocks — see cmd/korai/serve.go for that separation.
package wsasker

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/proto"
)

// Asker resolves an "ask" permission decision by round-tripping to the client.
type Asker struct {
	send func(proto.ServerEvent) error

	mu      sync.Mutex
	pending map[string]chan bool
}

// New returns an Asker that sends permission requests via send. send must be
// safe to call from the turn goroutine (the serve handler serializes writes).
func New(send func(proto.ServerEvent) error) *Asker {
	return &Asker{send: send, pending: make(map[string]chan bool)}
}

// Ask implements perm.Asker. It sends a perm_req keyed by a fresh id and blocks
// until the matching perm_res arrives or ctx is cancelled. It returns
// DecisionAllow or DecisionDeny per the Asker contract; on cancellation or a
// send failure it denies (fail-closed) and returns the error.
func (a *Asker) Ask(ctx context.Context, req perm.Request) (perm.Decision, error) {
	id := uuid.NewString()
	ch := make(chan bool, 1)

	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
	}()

	if err := a.send(proto.PermReq(id, req.ToolName, req.Input)); err != nil {
		return perm.DecisionDeny, err
	}

	select {
	case approved := <-ch:
		if approved {
			return perm.DecisionAllow, nil
		}
		return perm.DecisionDeny, nil
	case <-ctx.Done():
		return perm.DecisionDeny, ctx.Err()
	}
}

// Resolve delivers the client's answer to the pending request id. A late or
// unknown id (already timed out, cancelled, or never issued) is ignored. The
// send on the pending channel never blocks: the channel is buffered and each id
// is resolved at most once before Ask deletes it.
func (a *Asker) Resolve(id string, approved bool) {
	a.mu.Lock()
	ch, ok := a.pending[id]
	a.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- approved:
	default:
	}
}
