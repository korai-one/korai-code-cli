package apiclient

import (
	"context"
	"log/slog"
)

// ContextSizer is an optional interface a Client may implement to report the
// context window of a model. Callers must type-assert: the base Client
// interface stays frozen, and not every transport can know the answer.
//
// Per transport:
//   - KoraiClient (orchestrator HTTP): /v1/models exposes context_len on
//     canonical model rows — implemented below.
//   - KoraiClient pointed at a worker's loopback HTTP endpoint: the worker's
//     /v1/models returns rows without context_len, so discovery yields 0.
//   - LocalWorkerClient (direct binary channel): the localproto/hostproto
//     ReadyPayload carries only version + model ids, no context info, so it
//     deliberately does not implement ContextSizer.
type ContextSizer interface {
	// ContextLen reports the model's max context window in tokens, or 0 when
	// it cannot be determined. It may perform network I/O; callers should
	// bound ctx.
	ContextLen(ctx context.Context, model string) int
}

// ContextLen implements ContextSizer for the Korai HTTP backend by querying
// /v1/models through the SDK. An exact id match wins; when model is a routing
// alias (auto / fast / …) the orchestrator may serve any canonical model, so
// the smallest advertised positive context_len is returned as the conservative
// bound. 0 means unknown (endpoint unreachable, or a worker-loopback endpoint
// that does not advertise context_len) and callers keep their default.
func (c *KoraiClient) ContextLen(ctx context.Context, model string) int {
	infos, err := c.inner.ListModelsDetailed(ctx)
	if err != nil {
		slog.Debug("korai: context-length discovery failed", "error", err)
		return 0
	}
	if model == "" {
		model = c.model
	}
	smallest := 0
	for _, m := range infos {
		if m.ContextLen <= 0 {
			continue
		}
		if m.ID == model {
			return m.ContextLen
		}
		if smallest == 0 || m.ContextLen < smallest {
			smallest = m.ContextLen
		}
	}
	return smallest
}

// ContextLen forwards to the active backend when it can report a context
// window, so threshold discovery follows /worker_mode. A backend that does not
// implement ContextSizer yields 0 (unknown).
func (s *ClientSelector) ContextLen(ctx context.Context, model string) int {
	s.mu.RLock()
	c := s.local
	if s.active == WorkerRemote {
		c = s.remote
	}
	s.mu.RUnlock()
	if sizer, ok := c.(ContextSizer); ok {
		return sizer.ContextLen(ctx, model)
	}
	return 0
}
