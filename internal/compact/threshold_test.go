package compact_test

import (
	"context"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/compact"
)

// sizerClient is a fake backend that implements apiclient.ContextSizer.
type sizerClient struct {
	fakeClient
	nctx int
}

func (s *sizerClient) ContextLen(context.Context, string) int { return s.nctx }

func TestThresholdDefault(t *testing.T) {
	t.Parallel()
	th := compact.NewThreshold()
	if th.Value() != compact.DefaultThreshold {
		t.Errorf("Value = %d, want the default %d", th.Value(), compact.DefaultThreshold)
	}
}

func TestThresholdDiscoveryFallbackChain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		client apiclient.Client
		want   int
	}{
		{
			// A client without ContextSizer (the direct worker channel: its
			// handshake carries no context info) keeps the default.
			name:   "no sizer keeps default",
			client: &fakeClient{},
			want:   compact.DefaultThreshold,
		},
		{
			// A sizer that cannot discover (worker loopback /v1/models has no
			// context_len; or the endpoint is unreachable) keeps the default.
			name:   "unknown n_ctx keeps default",
			client: &sizerClient{nctx: 0},
			want:   compact.DefaultThreshold,
		},
		{
			// A small window lowers the threshold to the configured fraction.
			name:   "small n_ctx lowers threshold",
			client: &sizerClient{nctx: 40_000},
			want:   30_000, // 40k × 0.75
		},
		{
			// A huge window never raises the threshold above the default.
			name:   "large n_ctx capped at default",
			client: &sizerClient{nctx: 1_000_000},
			want:   compact.DefaultThreshold,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			th := compact.NewThreshold()
			th.Discover(context.Background(), tc.client, "auto")
			if th.Value() != tc.want {
				t.Errorf("Value after discovery = %d, want %d", th.Value(), tc.want)
			}
		})
	}
}
