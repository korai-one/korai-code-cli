package tui

import (
	"context"

	"github.com/Nevaero/korai-code-cli/internal/perm"
)

// permRequest pairs a permission request with the channel its decision is
// delivered on.
type permRequest struct {
	req   perm.Request
	reply chan perm.Decision
}

// Asker is the interactive perm.Asker for the TUI. The engine goroutine calls
// Ask synchronously; Ask hands the request to the UI over an unbuffered channel
// and blocks until the model replies (or ctx is cancelled). This is the bridge
// between the engine's blocking permission call and Bubble Tea's Elm loop — all
// the model ever does is read requests and write decisions via tea.Cmds.
type Asker struct {
	requests chan permRequest
}

// NewAsker creates an interactive asker.
func NewAsker() *Asker {
	return &Asker{requests: make(chan permRequest)}
}

// Ask implements perm.Asker. It blocks until the UI resolves the request.
func (a *Asker) Ask(ctx context.Context, req perm.Request) (perm.Decision, error) {
	reply := make(chan perm.Decision, 1)
	select {
	case a.requests <- permRequest{req: req, reply: reply}:
	case <-ctx.Done():
		return perm.DecisionDeny, ctx.Err()
	}
	select {
	case d := <-reply:
		return d, nil
	case <-ctx.Done():
		return perm.DecisionDeny, ctx.Err()
	}
}
