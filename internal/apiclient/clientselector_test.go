package apiclient

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// recordingClient is a Client that records that it was called and returns a
// closed event channel, so tests can tell which backend Complete forwarded to.
type recordingClient struct{ called *bool }

func (r recordingClient) Complete(context.Context, Request) (<-chan Event, error) {
	*r.called = true
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

func TestClientSelectorForwardsToActive(t *testing.T) {
	t.Parallel()
	var localCalled, remoteCalled bool
	sel := NewClientSelector(WorkerLocal,
		recordingClient{&localCalled}, recordingClient{&remoteCalled})

	if _, err := sel.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete (local): %v", err)
	}
	if !localCalled || remoteCalled {
		t.Fatalf("active local: localCalled=%v remoteCalled=%v", localCalled, remoteCalled)
	}

	localCalled, remoteCalled = false, false
	if err := sel.SetMode("remote"); err != nil {
		t.Fatalf("SetMode(remote): %v", err)
	}
	if got := sel.Mode(); got != "remote" {
		t.Fatalf("Mode() = %q, want remote", got)
	}
	if _, err := sel.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete (remote): %v", err)
	}
	if localCalled || !remoteCalled {
		t.Fatalf("active remote: localCalled=%v remoteCalled=%v", localCalled, remoteCalled)
	}
}

func TestClientSelectorSetModeUnavailable(t *testing.T) {
	t.Parallel()
	var localCalled bool
	// Remote backend absent (nil).
	sel := NewClientSelector(WorkerLocal, recordingClient{&localCalled}, nil)

	if err := sel.SetMode("remote"); err == nil {
		t.Fatal("SetMode(remote) with no remote backend: want error, got nil")
	}
	// The active mode must be unchanged after a rejected switch.
	if got := sel.Mode(); got != "local" {
		t.Fatalf("Mode() after rejected switch = %q, want local", got)
	}
}

func TestClientSelectorSetModeUnknown(t *testing.T) {
	t.Parallel()
	var localCalled bool
	sel := NewClientSelector(WorkerLocal, recordingClient{&localCalled}, nil)
	if err := sel.SetMode("bogus"); err == nil {
		t.Fatal("SetMode(bogus): want error, got nil")
	}
}

func TestClientSelectorAvailable(t *testing.T) {
	t.Parallel()
	var a, b bool
	cases := []struct {
		name          string
		local, remote Client
		want          []string
	}{
		{"both", recordingClient{&a}, recordingClient{&b}, []string{"local", "remote"}},
		{"local only", recordingClient{&a}, nil, []string{"local"}},
		{"remote only", nil, recordingClient{&b}, []string{"remote"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			active := WorkerLocal
			if tc.local == nil {
				active = WorkerRemote
			}
			sel := NewClientSelector(active, tc.local, tc.remote)
			if diff := cmp.Diff(tc.want, sel.Available()); diff != "" {
				t.Errorf("Available() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
