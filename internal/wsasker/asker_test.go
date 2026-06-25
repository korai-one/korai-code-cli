package wsasker_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/proto"
	"github.com/Nevaero/korai-code-cli/internal/wsasker"
)

// TestAskApproved verifies Ask emits a perm_req and returns DecisionAllow once
// the matching perm_res arrives via Resolve.
func TestAskApproved(t *testing.T) {
	sent := make(chan proto.PermReqEvent, 1)
	send := func(ev proto.ServerEvent) error {
		sent <- ev.(proto.PermReqEvent)
		return nil
	}
	a := wsasker.New(send)

	type result struct {
		decision perm.Decision
		err      error
	}
	done := make(chan result, 1)
	go func() {
		d, err := a.Ask(context.Background(), perm.Request{ToolName: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)})
		done <- result{d, err}
	}()

	var req proto.PermReqEvent
	select {
	case req = <-sent:
	case <-time.After(time.Second):
		t.Fatal("no perm_req emitted")
	}
	if req.Tool != "bash" {
		t.Errorf("perm_req tool = %q, want bash", req.Tool)
	}

	a.Resolve(req.ID, true)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Ask error: %v", r.err)
		}
		if r.decision != perm.DecisionAllow {
			t.Errorf("decision = %v, want allow", r.decision)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after Resolve")
	}
}

// TestAskDenied verifies a false answer resolves to DecisionDeny.
func TestAskDenied(t *testing.T) {
	sent := make(chan proto.PermReqEvent, 1)
	a := wsasker.New(func(ev proto.ServerEvent) error {
		sent <- ev.(proto.PermReqEvent)
		return nil
	})

	done := make(chan perm.Decision, 1)
	go func() {
		d, _ := a.Ask(context.Background(), perm.Request{ToolName: "write"})
		done <- d
	}()

	req := <-sent
	a.Resolve(req.ID, false)

	if got := <-done; got != perm.DecisionDeny {
		t.Errorf("decision = %v, want deny", got)
	}
}

// TestAskContextCancelled verifies a cancelled context unblocks Ask with a deny
// and the context error (fail-closed).
func TestAskContextCancelled(t *testing.T) {
	a := wsasker.New(func(proto.ServerEvent) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		d, err := a.Ask(ctx, perm.Request{ToolName: "bash"})
		if d != perm.DecisionDeny {
			t.Errorf("decision = %v, want deny on cancel", d)
		}
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after cancel")
	}
}

// TestAskSendError verifies a send failure denies immediately with the error,
// without waiting for any answer.
func TestAskSendError(t *testing.T) {
	wantErr := errors.New("connection closed")
	a := wsasker.New(func(proto.ServerEvent) error { return wantErr })

	d, err := a.Ask(context.Background(), perm.Request{ToolName: "bash"})
	if d != perm.DecisionDeny {
		t.Errorf("decision = %v, want deny", d)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

// TestResolveUnknownID verifies resolving an id with no pending Ask is a no-op
// (a late or duplicate answer must not panic or block).
func TestResolveUnknownID(t *testing.T) {
	a := wsasker.New(func(proto.ServerEvent) error { return nil })
	a.Resolve("never-issued", true) // must not panic or block
}
