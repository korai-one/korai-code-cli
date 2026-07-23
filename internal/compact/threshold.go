package compact

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// DefaultContextFraction is the share of a discovered model context window
// budgeted for history before auto-compaction triggers; the remainder is
// headroom for the reply and estimator error.
const DefaultContextFraction = 0.75

// Threshold is the effective auto-compaction threshold in estimated tokens.
// It starts at DefaultThreshold and can be lowered by Discover when the
// backend reports the model's real context window — it is never raised above
// the default, which exists to bound latency and summary quality, not just to
// fit the window. Value is safe for concurrent use (the TUI reads it per
// frame while Discover may still be running).
type Threshold struct {
	value atomic.Int64
}

// NewThreshold returns a Threshold at the static default.
func NewThreshold() *Threshold {
	t := &Threshold{}
	t.value.Store(DefaultThreshold)
	return t
}

// Value returns the current effective threshold in estimated tokens.
func (t *Threshold) Value() int { return int(t.value.Load()) }

// Discover asks the client for the active model's context window and, when one
// is reported, sets the threshold to min(DefaultThreshold,
// DefaultContextFraction × n_ctx). The fallback chain is deliberate: a client
// that does not implement apiclient.ContextSizer (the direct worker channel —
// its handshake carries no context info), an unreachable endpoint, or a
// backend that does not advertise context_len (the worker loopback HTTP
// endpoint) all yield 0 and keep the default. Callers typically run it once in
// a background goroutine at session start with a bounded ctx.
func (t *Threshold) Discover(ctx context.Context, client apiclient.Client, model string) {
	sizer, ok := client.(apiclient.ContextSizer)
	if !ok {
		return
	}
	nctx := sizer.ContextLen(ctx, model)
	if nctx <= 0 {
		return
	}
	eff := int(float64(nctx) * DefaultContextFraction)
	if eff > DefaultThreshold {
		eff = DefaultThreshold
	}
	if eff <= 0 {
		return
	}
	t.value.Store(int64(eff))
	slog.Debug("compact threshold discovered", "n_ctx", nctx, "threshold", eff)
}
